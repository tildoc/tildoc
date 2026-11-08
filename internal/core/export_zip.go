package core

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"path"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/tildoc/tildoc/internal/blob"
)

// ExportScope selects what an export contains.
type ExportScope struct {
	Kind string // space | folder | collection
	ID   string
}

// CollectionFileNames returns unique file names for collection items in
// order: clashing names become "name (2).ext". The same names are used in
// /@collections and in exports.
func CollectionFileNames(items []CollectionItem) []string {
	used := map[string]bool{}
	out := make([]string, len(items))
	for i, it := range items {
		name := it.Name
		ext := path.Ext(name)
		for n := 2; used[name]; n++ {
			name = fmt.Sprintf("%s (%d)%s", strings.TrimSuffix(it.Name, ext), n, ext)
		}
		used[name] = true
		out[i] = name
	}
	return out
}

type zipItem struct {
	path string // relative, folders end with "/"
	sha  blob.SHA256
	size int64
	mime string
	mod  time.Time
}

// archiveRootName makes a display name usable as the top folder of an archive.
func archiveRootName(name string) string {
	name = strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r < 0x20 {
			return '-'
		}
		return r
	}, strings.TrimSpace(name))
	if name == "" || name == "." || name == ".." {
		name = "export"
	}
	return name
}

// ExportName returns the suggested file name for an export.
func (s *Service) ExportName(ctx context.Context, p *Principal, scope ExportScope, ext string) (string, error) {
	_, name, err := s.exportRoot(ctx, p, scope)
	if err != nil {
		return "", err
	}
	return archiveRootName(name) + ext, nil
}

// exportRoot checks permissions and returns the start folder (empty for
// collections) and the display name.
func (s *Service) exportRoot(ctx context.Context, p *Principal, scope ExportScope) (string, string, error) {
	switch scope.Kind {
	case "space":
		sp, err := s.GetSpace(ctx, p, scope.ID)
		if err != nil {
			return "", "", err
		}
		if err := s.requireCap(ctx, s.DB, p, sp.ID, CapExport); err != nil {
			return "", "", err
		}
		return sp.RootFolderID, sp.Name, nil
	case "folder":
		f, err := s.GetFolder(ctx, p, scope.ID)
		if err != nil {
			return "", "", err
		}
		if err := s.requireCap(ctx, s.DB, p, f.SpaceID, CapExport); err != nil {
			return "", "", err
		}
		name := f.Name
		if f.ParentID == nil {
			sp, err := s.GetSpace(ctx, p, f.SpaceID)
			if err != nil {
				return "", "", err
			}
			name = sp.Name
		}
		return f.ID, name, nil
	case "collection":
		c, err := s.GetCollection(ctx, p, scope.ID)
		if err != nil {
			return "", "", err
		}
		return "", c.Name, nil
	}
	return "", "", errf(ErrInvalid, "unknown export scope %q", scope.Kind)
}

// ExportZip writes the current files of a space, folder or collection as a
// ZIP archive: the folder structure and names as shown, current versions
// only, no trash. Collections are flat with their shown names.
func (s *Service) ExportZip(ctx context.Context, p *Principal, scope ExportScope, w io.Writer, sealer Sealer) error {
	start, name, err := s.exportRoot(ctx, p, scope)
	if err != nil {
		return err
	}
	var items []zipItem
	if scope.Kind == "collection" {
		ci, err := s.CollectionItems(ctx, p, scope.ID)
		if err != nil {
			return err
		}
		names := CollectionFileNames(ci)
		allowed := map[string]bool{}
		for i, it := range ci {
			ok, seen := allowed[it.SpaceID]
			if !seen {
				ok = s.requireCap(ctx, s.DB, p, it.SpaceID, CapExport) == nil
				allowed[it.SpaceID] = ok
			}
			if ok {
				items = append(items, zipItem{path: names[i], sha: it.SHA256, size: it.Size, mime: it.MIME, mod: it.ModifiedAt})
			}
		}
	} else if err := s.snapshot(ctx, func(tx pgx.Tx) error {
		items, err = folderItems(ctx, tx, start)
		return err
	}); err != nil {
		return err
	}

	release, err := s.holdBlobs(ctx)
	if err != nil {
		return err
	}
	defer release()
	out, err := seal(w, sealer)
	if err != nil {
		return err
	}
	zw := zip.NewWriter(out)
	root := archiveRootName(name) + "/"
	if _, err := zw.CreateHeader(&zip.FileHeader{Name: root, Modified: time.Now()}); err != nil {
		return err
	}
	for _, it := range items {
		hdr := &zip.FileHeader{Name: root + it.path, Modified: it.mod}
		if strings.HasSuffix(it.path, "/") {
			if _, err := zw.CreateHeader(hdr); err != nil {
				return err
			}
			continue
		}
		hdr.Method = zip.Store
		if compressible(it.mime) {
			hdr.Method = zip.Deflate
		}
		hdr.SetMode(0o644)
		fw, err := zw.CreateHeader(hdr)
		if err != nil {
			return err
		}
		f, err := s.Blobs.Open(it.sha)
		if err != nil {
			return fmt.Errorf("open %s: %w", it.path, err)
		}
		_, err = io.Copy(fw, f)
		f.Close()
		if err != nil {
			return err
		}
	}
	if err := zw.Close(); err != nil {
		return err
	}
	return out.Close()
}

func compressible(mime string) bool {
	return strings.HasPrefix(mime, "text/") || strings.Contains(mime, "json") || strings.Contains(mime, "xml") ||
		strings.Contains(mime, "csv") || strings.Contains(mime, "svg")
}

// folderItems lists the folders and live files below a folder.
func folderItems(ctx context.Context, tx pgx.Tx, start string) ([]zipItem, error) {
	rows, err := tx.Query(ctx, `
		WITH RECURSIVE tree(id, path) AS (
			SELECT id, ''::text FROM entries WHERE id = $1 AND kind = 'folder'
			UNION ALL
			SELECT e.id, t.path || e.name || '/' FROM entries e JOIN tree t ON e.parent_id = t.id AND e.kind = 'folder'
		)
		SELECT t.path, coalesce(l.name, ''), d.sha256, coalesce(b.size, 0), coalesce(b.mime, ''), coalesce(d.updated_at, now())
		FROM tree t
		LEFT JOIN entries l ON l.parent_id = t.id AND l.kind = 'link' AND l.deleted_at IS NULL
		LEFT JOIN document_heads d ON d.id = l.document_id
		LEFT JOIN blobs b ON b.sha256 = d.sha256
		ORDER BY t.path COLLATE "C", l.name COLLATE "C"`, start)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []zipItem
	seenDir := map[string]bool{"": true}
	for rows.Next() {
		var it zipItem
		var dir, name string
		var sha []byte
		if err := rows.Scan(&dir, &name, &sha, &it.size, &it.mime, &it.mod); err != nil {
			return nil, err
		}
		if !seenDir[dir] {
			seenDir[dir] = true
			out = append(out, zipItem{path: dir, mod: it.mod})
		}
		if name == "" {
			continue
		}
		it.path = dir + name
		it.mime = displayMIME(name, it.mime)
		copy(it.sha[:], sha)
		out = append(out, it)
	}
	return out, rows.Err()
}
