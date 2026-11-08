package core

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/tildoc/tildoc/internal/blob"
	"github.com/tildoc/tildoc/internal/db"
)

// FolderInfo describes a folder.
type FolderInfo struct {
	ID        string    `json:"id"`
	SpaceID   string    `json:"space_id"`
	ParentID  *string   `json:"parent_id"`
	Name      string    `json:"name"`
	Path      string    `json:"path"` // "/" for root, "/a/b" otherwise
	CreatedAt time.Time `json:"created_at"`
}

// FileInfo describes a link (a document's placement) together with the
// current state of its document.
type FileInfo struct {
	LinkID       string            `json:"link_id"`
	DocumentID   string            `json:"document_id"`
	SpaceID      string            `json:"space_id"`
	FolderID     string            `json:"folder_id"`
	Name         string            `json:"name"`
	Path         string            `json:"path"`
	Size         int64             `json:"size"`
	SHA256       blob.SHA256       `json:"sha256"`
	MD5          string            `json:"md5"`
	MIME         string            `json:"mime"`
	Version      int               `json:"version"`
	Status       string            `json:"status"`
	LinkCount    int               `json:"link_count"`
	CreatedAt    time.Time         `json:"created_at"`
	ModifiedAt   time.Time         `json:"modified_at"`
	OriginalPath string            `json:"original_path"`
	ImportID     *string           `json:"import_id"`
	Meta         map[string]string `json:"meta,omitempty"`
}

// ETag returns the S3/HTTP style entity tag (MD5 hex).
func (f *FileInfo) ETag() string { return `"` + f.MD5 + `"` }

const fileSelect = `
SELECT l.id::text, l.document_id::text, l.space_id::text, l.parent_id::text, l.name,
       folder_path(l.parent_id) || '/' || l.name,
       b.size, b.sha256, b.md5, b.mime, d.current_version, coalesce(bp.status, 'pending'),
       (SELECT count(*) FROM entries x WHERE x.document_id = d.id AND x.deleted_at IS NULL),
       l.created_at, d.updated_at, l.original_path, l.import_id::text, d.meta
FROM entries l
JOIN document_heads d ON d.id = l.document_id
JOIN blobs b ON b.sha256 = d.sha256
LEFT JOIN blob_processing bp ON bp.sha256 = d.sha256`

func scanFile(row pgx.Row) (*FileInfo, error) {
	f := &FileInfo{}
	var sha, md5 []byte
	var folder *string
	err := row.Scan(&f.LinkID, &f.DocumentID, &f.SpaceID, &folder, &f.Name, &f.Path, &f.Size, &sha, &md5,
		&f.MIME, &f.Version, &f.Status, &f.LinkCount, &f.CreatedAt, &f.ModifiedAt, &f.OriginalPath, &f.ImportID, &f.Meta)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errf(ErrNotFound, "file not found")
	}
	if err != nil {
		return nil, err
	}
	if folder != nil {
		f.FolderID = *folder
	}
	copy(f.SHA256[:], sha)
	f.MD5 = hex.EncodeToString(md5)
	f.MIME = displayMIME(f.Name, f.MIME)
	return f, nil
}

func scanFiles(rows pgx.Rows, err error) ([]FileInfo, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []FileInfo{}
	for rows.Next() {
		f, err := scanFile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *f)
	}
	return out, rows.Err()
}

// DetectMIME sniffs the content type from the first bytes of a file.
func DetectMIME(head []byte) string {
	if bytes.Contains(head, []byte("%PDF-")) {
		return "application/pdf"
	}
	if len(head) == 0 {
		return "application/octet-stream"
	}
	return http.DetectContentType(head)
}

// displayMIME refines generic sniffed types using the file extension.
func displayMIME(name, sniffed string) string {
	generic := sniffed == "application/octet-stream" || strings.HasPrefix(sniffed, "text/plain") ||
		sniffed == "application/zip"
	if generic {
		if byExt := mime.TypeByExtension(strings.ToLower(path.Ext(name))); byExt != "" {
			return byExt
		}
	}
	return sniffed
}

// ---------------------------------------------------------------- lookups

func (s *Service) folderInfo(ctx context.Context, q db.Querier, id string) (*FolderInfo, error) {
	f := &FolderInfo{}
	err := q.QueryRow(ctx, `SELECT id::text, space_id::text, parent_id::text, name, folder_path(id), created_at
		FROM entries WHERE id=$1 AND kind='folder'`, id).Scan(&f.ID, &f.SpaceID, &f.ParentID, &f.Name, &f.Path, &f.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errf(ErrNotFound, "folder not found")
	}
	if f.Path == "" {
		f.Path = "/"
	}
	return f, err
}

// resolveFolder walks segs from the space root. Returns the deepest
// matching folder and the number of matched segments.
func resolveFolder(ctx context.Context, q db.Querier, spaceID string, segs []string) (string, int, error) {
	if segs == nil {
		segs = []string{}
	}
	var id string
	var idx int
	err := q.QueryRow(ctx, `
		WITH RECURSIVE walk(id, idx) AS (
			SELECT id, 1 FROM entries WHERE space_id = $1 AND parent_id IS NULL AND kind = 'folder'
			UNION ALL
			SELECT f.id, w.idx + 1 FROM walk w
			JOIN entries f ON f.parent_id = w.id AND f.kind = 'folder' AND f.name = ($2::text[])[w.idx]
			WHERE w.idx <= coalesce(array_length($2::text[], 1), 0)
		) SELECT id::text, idx FROM walk ORDER BY idx DESC LIMIT 1`, spaceID, segs).Scan(&id, &idx)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", 0, errf(ErrNotFound, "space not found")
	}
	return id, idx - 1, err
}

func lockFolder(ctx context.Context, tx pgx.Tx, id string) (spaceID string, err error) {
	err = tx.QueryRow(ctx, `SELECT space_id::text FROM entries WHERE id=$1 AND kind='folder' FOR UPDATE`, id).Scan(&spaceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", errf(ErrNotFound, "folder not found")
	}
	return spaceID, err
}

// lockFolders locks several folder rows in a stable order (deadlock free).
func lockFolders(ctx context.Context, tx pgx.Tx, ids ...string) error {
	seen := map[string]bool{}
	var uniq []string
	for _, id := range ids {
		if id != "" && !seen[id] {
			seen[id] = true
			uniq = append(uniq, id)
		}
	}
	rows, err := tx.Query(ctx, `SELECT id FROM entries WHERE id = ANY($1::uuid[]) AND kind='folder' ORDER BY id FOR UPDATE`, uniq)
	if err != nil {
		return err
	}
	n := 0
	for rows.Next() {
		n++
	}
	rows.Close()
	if n != len(uniq) {
		return errf(ErrNotFound, "folder not found")
	}
	return rows.Err()
}

// child returns what occupies name inside folder: a folder id, a live link id, or nothing.
func child(ctx context.Context, q db.Querier, folderID, name string) (folder, link string, err error) {
	var id, kind string
	err = q.QueryRow(ctx, `SELECT id::text, kind FROM entries WHERE parent_id=$1 AND name=$2 AND deleted_at IS NULL`,
		folderID, name).Scan(&id, &kind)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return "", "", nil
	case err != nil:
		return "", "", err
	case kind == "folder":
		return id, "", nil
	}
	return "", id, nil
}

// GetFolder returns a folder (read permission).
func (s *Service) GetFolder(ctx context.Context, p *Principal, id string) (*FolderInfo, error) {
	f, err := s.folderInfo(ctx, s.DB, id)
	if err != nil {
		return nil, err
	}
	if err := s.requirePerm(ctx, s.DB, p, f.SpaceID, PermRead); err != nil {
		return nil, errf(ErrNotFound, "folder not found")
	}
	return f, nil
}

// FolderByPath resolves a folder inside a space.
func (s *Service) FolderByPath(ctx context.Context, p *Principal, spaceID string, segs []string) (*FolderInfo, error) {
	if err := s.requirePerm(ctx, s.DB, p, spaceID, PermRead); err != nil {
		return nil, err
	}
	id, n, err := resolveFolder(ctx, s.DB, spaceID, segs)
	if err != nil {
		return nil, err
	}
	if n != len(segs) {
		return nil, errf(ErrNotFound, "folder not found")
	}
	return s.folderInfo(ctx, s.DB, id)
}

// FileByPath resolves a file inside a space.
func (s *Service) FileByPath(ctx context.Context, p *Principal, spaceID string, segs []string) (*FileInfo, error) {
	if len(segs) == 0 {
		return nil, errf(ErrIsFolder, "path is a folder")
	}
	if err := s.requirePerm(ctx, s.DB, p, spaceID, PermRead); err != nil {
		return nil, err
	}
	return s.fileByPath(ctx, s.DB, spaceID, segs)
}

func (s *Service) fileByPath(ctx context.Context, q db.Querier, spaceID string, segs []string) (*FileInfo, error) {
	fid, n, err := resolveFolder(ctx, q, spaceID, segs[:len(segs)-1])
	if err != nil {
		return nil, err
	}
	if n != len(segs)-1 {
		return nil, errf(ErrNotFound, "file not found")
	}
	return scanFile(q.QueryRow(ctx, fileSelect+` WHERE l.parent_id=$1 AND l.name=$2 AND l.deleted_at IS NULL`,
		fid, segs[len(segs)-1]))
}

// GetFile returns a live link (read permission).
func (s *Service) GetFile(ctx context.Context, p *Principal, linkID string) (*FileInfo, error) {
	f, err := scanFile(s.DB.QueryRow(ctx, fileSelect+` WHERE l.id=$1 AND l.deleted_at IS NULL`, linkID))
	if err != nil {
		return nil, err
	}
	if err := s.requirePerm(ctx, s.DB, p, f.SpaceID, PermRead); err != nil {
		return nil, errf(ErrNotFound, "file not found")
	}
	return f, nil
}

// ListFolder returns subfolders and files of a folder.
func (s *Service) ListFolder(ctx context.Context, p *Principal, folderID string) (*FolderInfo, []FolderInfo, []FileInfo, error) {
	f, err := s.GetFolder(ctx, p, folderID)
	if err != nil {
		return nil, nil, nil, err
	}
	rows, err := s.DB.Query(ctx, `SELECT id::text, space_id::text, parent_id::text, name, created_at
		FROM entries WHERE parent_id=$1 AND kind='folder' ORDER BY name`, folderID)
	if err != nil {
		return nil, nil, nil, err
	}
	folders := []FolderInfo{}
	for rows.Next() {
		var c FolderInfo
		if err := rows.Scan(&c.ID, &c.SpaceID, &c.ParentID, &c.Name, &c.CreatedAt); err != nil {
			rows.Close()
			return nil, nil, nil, err
		}
		c.Path = strings.TrimSuffix(f.Path, "/") + "/" + c.Name
		folders = append(folders, c)
	}
	rows.Close()
	files, err := scanFiles(s.DB.Query(ctx, fileSelect+` WHERE l.parent_id=$1 AND l.deleted_at IS NULL ORDER BY l.name`, folderID))
	return f, folders, files, err
}

// ListFolderTree returns all folders of a space (for tree views / pickers).
func (s *Service) ListFolderTree(ctx context.Context, p *Principal, spaceID string) ([]FolderInfo, error) {
	if err := s.requirePerm(ctx, s.DB, p, spaceID, PermRead); err != nil {
		return nil, err
	}
	rows, err := s.DB.Query(ctx, `
		WITH RECURSIVE t(id, parent_id, name, path, created_at) AS (
			SELECT id, parent_id, name, ''::text, created_at FROM entries WHERE space_id=$1 AND parent_id IS NULL AND kind='folder'
			UNION ALL
			SELECT f.id, f.parent_id, f.name, t.path || '/' || f.name, f.created_at FROM entries f JOIN t ON f.parent_id = t.id AND f.kind='folder'
		) SELECT id::text, parent_id::text, name, path, created_at FROM t ORDER BY path COLLATE "C"`, spaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []FolderInfo{}
	for rows.Next() {
		f := FolderInfo{SpaceID: spaceID}
		if err := rows.Scan(&f.ID, &f.ParentID, &f.Name, &f.Path, &f.CreatedAt); err != nil {
			return nil, err
		}
		if f.Path == "" {
			f.Path = "/"
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------- folders

func (s *Service) mkdirTx(ctx context.Context, tx pgx.Tx, p *Principal, parentID, name string) (string, error) {
	if _, err := lockFolder(ctx, tx, parentID); err != nil {
		return "", err
	}
	fold, link, err := child(ctx, tx, parentID, name)
	if err != nil {
		return "", err
	}
	if fold != "" {
		return fold, errf(ErrConflict, "folder %q exists", name)
	}
	if link != "" {
		return "", errf(ErrConflict, "a file named %q exists", name)
	}
	var id string
	err = tx.QueryRow(ctx, `INSERT INTO entries(space_id, kind, parent_id, name, created_by)
		SELECT space_id, 'folder', id, $2, $3::uuid FROM entries WHERE id=$1 AND kind='folder' RETURNING id::text`,
		parentID, name, p.userRef()).Scan(&id)
	return id, err
}

func (s *Service) mkdirAllTx(ctx context.Context, tx pgx.Tx, p *Principal, spaceID string, segs []string) (string, error) {
	id, n, err := resolveFolder(ctx, tx, spaceID, segs)
	if err != nil {
		return "", err
	}
	for _, seg := range segs[n:] {
		nid, err := s.mkdirTx(ctx, tx, p, id, seg)
		if err != nil && !(errors.Is(err, ErrConflict) && nid != "") {
			return "", err
		}
		id = nid
	}
	return id, nil
}

// Mkdir creates a folder below parentID.
func (s *Service) Mkdir(ctx context.Context, p *Principal, parentID, name string) (*FolderInfo, error) {
	name, err := NormalizeName(name)
	if err != nil {
		return nil, err
	}
	parent, err := s.GetFolder(ctx, p, parentID)
	if err != nil {
		return nil, err
	}
	if err := s.requirePerm(ctx, s.DB, p, parent.SpaceID, PermWrite); err != nil {
		return nil, err
	}
	var id string
	err = s.tx(ctx, func(tx pgx.Tx) error {
		var err error
		id, err = s.mkdirTx(ctx, tx, p, parentID, name)
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.folderInfo(ctx, s.DB, id)
}

// MkdirAll creates all missing folders of a path inside a space.
func (s *Service) MkdirAll(ctx context.Context, p *Principal, spaceID string, segs []string) (*FolderInfo, error) {
	if err := s.requirePerm(ctx, s.DB, p, spaceID, PermWrite); err != nil {
		return nil, err
	}
	var id string
	err := s.tx(ctx, func(tx pgx.Tx) error {
		var err error
		id, err = s.mkdirAllTx(ctx, tx, p, spaceID, segs)
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.folderInfo(ctx, s.DB, id)
}

// isAncestor reports whether anc is folder or an ancestor of folder.
func isAncestor(ctx context.Context, q db.Querier, anc, folder string) (bool, error) {
	var found bool
	err := q.QueryRow(ctx, `
		WITH RECURSIVE up(id, parent_id) AS (
			SELECT id, parent_id FROM entries WHERE id=$2
			UNION ALL SELECT f.id, f.parent_id FROM entries f JOIN up ON f.id = up.parent_id
		) SELECT EXISTS(SELECT 1 FROM up WHERE id=$1)`, anc, folder).Scan(&found)
	return found, err
}

// MoveFolder moves and/or renames a folder. Cross-space moves move every
// document below (requires delete permission on the source space).
func (s *Service) MoveFolder(ctx context.Context, p *Principal, folderID, dstParentID, newName string) (*FolderInfo, error) {
	src, err := s.GetFolder(ctx, p, folderID)
	if err != nil {
		return nil, err
	}
	if src.ParentID == nil {
		return nil, errf(ErrInvalid, "cannot move the root folder")
	}
	if dstParentID == "" {
		dstParentID = *src.ParentID
	}
	if newName == "" {
		newName = src.Name
	}
	if newName, err = NormalizeName(newName); err != nil {
		return nil, err
	}
	dst, err := s.GetFolder(ctx, p, dstParentID)
	if err != nil {
		return nil, err
	}
	cross := dst.SpaceID != src.SpaceID
	need := PermWrite
	if cross {
		need = PermDelete
	}
	if err := s.requirePerm(ctx, s.DB, p, src.SpaceID, need); err != nil {
		return nil, err
	}
	if err := s.requirePerm(ctx, s.DB, p, dst.SpaceID, PermWrite); err != nil {
		return nil, err
	}
	resultID := folderID
	err = s.tx(ctx, func(tx pgx.Tx) error {
		if err := lockFolders(ctx, tx, *src.ParentID, dstParentID); err != nil {
			return err
		}
		if anc, err := isAncestor(ctx, tx, folderID, dstParentID); err != nil {
			return err
		} else if anc {
			return errf(ErrInvalid, "cannot move a folder into itself")
		}
		fold, link, err := child(ctx, tx, dstParentID, newName)
		if err != nil {
			return err
		}
		if fold == folderID {
			return nil // no-op
		}
		if fold != "" || link != "" {
			return errf(ErrConflict, "%q already exists in target folder", newName)
		}
		if !cross {
			_, err = tx.Exec(ctx, `UPDATE entries SET parent_id=$2, name=$3, updated_at=now() WHERE id=$1 AND kind='folder'`, folderID, dstParentID, newName)
			return err
		}
		resultID, err = s.moveTreeCrossSpaceTx(ctx, tx, p, folderID, dstParentID, newName)
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.folderInfo(ctx, s.DB, resultID)
}

func (s *Service) moveTreeCrossSpaceTx(ctx context.Context, tx pgx.Tx, p *Principal, srcID, dstParentID, name string) (string, error) {
	newID, err := s.mkdirTx(ctx, tx, p, dstParentID, name)
	if err != nil {
		return "", err
	}
	links, err := tx.Query(ctx, `SELECT id::text FROM entries WHERE parent_id=$1 AND kind='link' AND deleted_at IS NULL`, srcID)
	if err != nil {
		return "", err
	}
	var ids []string
	for links.Next() {
		var id string
		links.Scan(&id)
		ids = append(ids, id)
	}
	links.Close()
	for _, id := range ids {
		if _, err := s.moveLinkTx(ctx, tx, p, id, newID, "", false); err != nil {
			return "", err
		}
	}
	subs, err := tx.Query(ctx, `SELECT id::text, name FROM entries WHERE parent_id=$1 AND kind='folder'`, srcID)
	if err != nil {
		return "", err
	}
	type sub struct{ id, name string }
	var children []sub
	for subs.Next() {
		var c sub
		subs.Scan(&c.id, &c.name)
		children = append(children, c)
	}
	subs.Close()
	for _, c := range children {
		if _, err := s.moveTreeCrossSpaceTx(ctx, tx, p, c.id, newID, c.name); err != nil {
			return "", err
		}
	}
	return newID, deleteFolderTreeTx(ctx, tx, srcID)
}

// DeleteFolder deletes a folder recursively: links below are moved to the
// trash (documents without remaining links become trashed).
func (s *Service) DeleteFolder(ctx context.Context, p *Principal, folderID string) error {
	f, err := s.GetFolder(ctx, p, folderID)
	if err != nil {
		return err
	}
	if f.ParentID == nil {
		return errf(ErrInvalid, "cannot delete the root folder")
	}
	if err := s.requirePerm(ctx, s.DB, p, f.SpaceID, PermDelete); err != nil {
		return err
	}
	return s.tx(ctx, func(tx pgx.Tx) error {
		if err := lockFolders(ctx, tx, *f.ParentID, folderID); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			WITH RECURSIVE sub(id, path) AS (
				SELECT id, $2::text FROM entries WHERE id = $1 AND kind = 'folder'
				UNION ALL SELECT f.id, sub.path || '/' || f.name FROM entries f JOIN sub ON f.parent_id = sub.id AND f.kind = 'folder'
			)
			UPDATE entries l SET deleted_at = now(), deleted_path = sub.path || '/' || l.name, updated_at = now()
			FROM sub WHERE l.parent_id = sub.id AND l.kind = 'link' AND l.deleted_at IS NULL
			RETURNING l.document_id::text`, folderID, f.Path)
		if err != nil {
			return err
		}
		var docs []string
		for rows.Next() {
			var d string
			rows.Scan(&d)
			docs = append(docs, d)
		}
		rows.Close()
		if err := trashOrphans(ctx, tx, docs); err != nil {
			return err
		}
		return deleteFolderTreeTx(ctx, tx, folderID)
	})
}

// deleteFolderTreeTx removes a folder and all folders below it. Live links
// must have been moved or trashed before (the schema refuses otherwise);
// trashed links below lose their parent and keep their deleted_path.
func deleteFolderTreeTx(ctx context.Context, tx pgx.Tx, folderID string) error {
	_, err := tx.Exec(ctx, `
		WITH RECURSIVE sub(id) AS (
			SELECT id FROM entries WHERE id = $1 AND kind = 'folder'
			UNION ALL SELECT f.id FROM entries f JOIN sub ON f.parent_id = sub.id AND f.kind = 'folder'
		)
		DELETE FROM entries WHERE id IN (SELECT id FROM sub)`, folderID)
	return err
}

// trashOrphans marks documents without live links as deleted.
func trashOrphans(ctx context.Context, tx pgx.Tx, docIDs []string) error {
	if len(docIDs) == 0 {
		return nil
	}
	_, err := tx.Exec(ctx, `UPDATE documents d SET deleted_at = now()
		WHERE d.id = ANY($1::uuid[]) AND d.deleted_at IS NULL
		AND NOT EXISTS (SELECT 1 FROM entries l WHERE l.document_id = d.id AND l.deleted_at IS NULL)`, docIDs)
	return err
}
