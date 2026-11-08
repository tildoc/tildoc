package core

import (
	"context"
	"encoding/hex"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/tildoc/tildoc/internal/blob"
)

// Document is the identity of a piece of content in a space.
type Document struct {
	ID            string        `json:"id"`
	SpaceID       string        `json:"space_id"`
	Version       int           `json:"version"`
	SHA256        blob.SHA256   `json:"sha256"`
	MD5           string        `json:"md5"`
	Size          int64         `json:"size"`
	MIME          string        `json:"mime"`
	Status        string        `json:"status"`
	Kind          string        `json:"kind"`
	Pages         *int          `json:"pages"`
	ProcessError  string        `json:"process_error,omitempty"`
	CreatedAt     time.Time     `json:"created_at"`
	UpdatedAt     time.Time     `json:"updated_at"`
	DeletedAt     *time.Time    `json:"deleted_at"`
	ForkedFrom    *string       `json:"forked_from"`
	HasText       bool          `json:"has_text"`
	TextMethod    string        `json:"text_method,omitempty"`
	HasSearchable bool          `json:"has_searchable"`
	Versions      []VersionInfo `json:"versions"`
	Links         []LinkRef     `json:"links"`
	Name          string        `json:"name"` // name of the first live link (or last deleted)
}

// VersionInfo describes one version.
type VersionInfo struct {
	Version   int               `json:"version"`
	SHA256    blob.SHA256       `json:"sha256"`
	Size      int64             `json:"size"`
	MIME      string            `json:"mime"`
	CreatedAt time.Time         `json:"created_at"`
	CreatedBy string            `json:"created_by"`
	Source    string            `json:"source"`
	Meta      map[string]string `json:"meta,omitempty"`
}

// LinkRef is a place where a document appears.
type LinkRef struct {
	ID           string    `json:"id"`
	FolderID     string    `json:"folder_id"`
	Name         string    `json:"name"`
	Path         string    `json:"path"`
	OriginalPath string    `json:"original_path"`
	ImportID     *string   `json:"import_id"`
	CreatedAt    time.Time `json:"created_at"`
}

// GetDocument returns a document with versions and links (read permission).
func (s *Service) GetDocument(ctx context.Context, p *Principal, id string) (*Document, error) {
	if !looksLikeUUID(id) {
		return nil, errf(ErrNotFound, "document not found")
	}
	d := &Document{ID: id}
	var sha, md5 []byte
	err := s.DB.QueryRow(ctx, `
		SELECT d.space_id::text, d.current_version, d.current_sha256, b.md5, b.size, b.mime,
		       coalesce(bp.status,'pending'), coalesce(bp.kind,''), bp.pages, coalesce(bp.error,''),
		       d.created_at, d.updated_at, d.deleted_at, d.forked_from::text,
		       coalesce(t.method,''), t.sha256 IS NOT NULL,
		       EXISTS(SELECT 1 FROM blob_derivatives x WHERE x.source_sha256=d.current_sha256 AND x.kind='searchable_pdf')
		FROM documents d JOIN blobs b ON b.sha256=d.current_sha256
		LEFT JOIN blob_processing bp ON bp.sha256=d.current_sha256
		LEFT JOIN blob_texts t ON t.sha256=d.current_sha256
		WHERE d.id=$1`, id).Scan(&d.SpaceID, &d.Version, &sha, &md5, &d.Size, &d.MIME, &d.Status, &d.Kind, &d.Pages,
		&d.ProcessError, &d.CreatedAt, &d.UpdatedAt, &d.DeletedAt, &d.ForkedFrom, &d.TextMethod, &d.HasText, &d.HasSearchable)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errf(ErrNotFound, "document not found")
	}
	if err != nil {
		return nil, err
	}
	if err := s.requirePerm(ctx, s.DB, p, d.SpaceID, PermRead); err != nil {
		return nil, errf(ErrNotFound, "document not found")
	}
	copy(d.SHA256[:], sha)
	d.MD5 = hex.EncodeToString(md5)

	rows, err := s.DB.Query(ctx, `SELECT v.version, v.sha256, b.size, b.mime, v.created_at, coalesce(u.username,''), v.source, v.meta
		FROM document_versions v JOIN blobs b ON b.sha256=v.sha256 LEFT JOIN users u ON u.id=v.created_by
		WHERE v.document_id=$1 ORDER BY v.version DESC`, id)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var v VersionInfo
		var vs []byte
		if err := rows.Scan(&v.Version, &vs, &v.Size, &v.MIME, &v.CreatedAt, &v.CreatedBy, &v.Source, &v.Meta); err != nil {
			rows.Close()
			return nil, err
		}
		copy(v.SHA256[:], vs)
		d.Versions = append(d.Versions, v)
	}
	rows.Close()

	rows, err = s.DB.Query(ctx, `SELECT id::text, coalesce(folder_id::text,''), name,
		CASE WHEN deleted_at IS NULL THEN folder_path(folder_id) || '/' || name ELSE coalesce(deleted_path,'') END,
		original_path, import_id::text, created_at, deleted_at IS NOT NULL
		FROM links WHERE document_id=$1 AND (deleted_at IS NULL OR $2) ORDER BY created_at`, id, d.DeletedAt != nil)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	d.Links = []LinkRef{}
	var lastDeleted string
	for rows.Next() {
		var l LinkRef
		var deleted bool
		if err := rows.Scan(&l.ID, &l.FolderID, &l.Name, &l.Path, &l.OriginalPath, &l.ImportID, &l.CreatedAt, &deleted); err != nil {
			return nil, err
		}
		if deleted {
			lastDeleted = l.Name
			continue
		}
		if d.Name == "" {
			d.Name = l.Name
		}
		d.Links = append(d.Links, l)
	}
	if d.Name == "" {
		d.Name = lastDeleted
	}
	d.MIME = displayMIME(d.Name, d.MIME)
	return d, rows.Err()
}

// ContentInfo describes content returned by OpenDocument.
type ContentInfo struct {
	Name    string
	Size    int64
	MIME    string
	SHA256  blob.SHA256
	MD5     string
	ModTime time.Time
}

// OpenDocument opens the content of a document version. version 0 means
// current; variant "searchable" returns the derived OCR PDF if present.
func (s *Service) OpenDocument(ctx context.Context, p *Principal, docID string, version int, variant string) (blob.File, *ContentInfo, error) {
	d, err := s.GetDocument(ctx, p, docID)
	if err != nil {
		return nil, nil, err
	}
	ci := &ContentInfo{Name: d.Name, ModTime: d.UpdatedAt}
	sha := d.SHA256
	if version != 0 && version != d.Version {
		found := false
		for _, v := range d.Versions {
			if v.Version == version {
				sha, ci.ModTime, found = v.SHA256, v.CreatedAt, true
			}
		}
		if !found {
			return nil, nil, errf(ErrNotFound, "version not found")
		}
	}
	if variant == "searchable" {
		var derived []byte
		err := s.DB.QueryRow(ctx, `SELECT derived_sha256 FROM blob_derivatives WHERE source_sha256=$1 AND kind='searchable_pdf'`,
			sha[:]).Scan(&derived)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, errf(ErrNotFound, "no searchable version available")
		}
		if err != nil {
			return nil, nil, err
		}
		copy(sha[:], derived)
	}
	return s.openBlob(ctx, sha, ci)
}

func (s *Service) openBlob(ctx context.Context, sha blob.SHA256, ci *ContentInfo) (blob.File, *ContentInfo, error) {
	var md5 []byte
	var mt string
	if err := s.DB.QueryRow(ctx, `SELECT md5, size, mime FROM blobs WHERE sha256=$1`, sha[:]).Scan(&md5, &ci.Size, &mt); err != nil {
		return nil, nil, err
	}
	ci.SHA256, ci.MD5, ci.MIME = sha, hex.EncodeToString(md5), displayMIME(ci.Name, mt)
	f, err := s.Blobs.Open(sha)
	if err != nil {
		return nil, nil, err
	}
	return f, ci, nil
}

// OpenFile opens the current content of a link.
func (s *Service) OpenFile(ctx context.Context, p *Principal, f *FileInfo) (blob.File, error) {
	if err := s.requirePerm(ctx, s.DB, p, f.SpaceID, PermRead); err != nil {
		return nil, err
	}
	return s.Blobs.Open(f.SHA256)
}

// DocumentText returns extracted text of the current version.
func (s *Service) DocumentText(ctx context.Context, p *Principal, docID string) (string, string, error) {
	d, err := s.GetDocument(ctx, p, docID)
	if err != nil {
		return "", "", err
	}
	var content, method string
	err = s.DB.QueryRow(ctx, `SELECT content, method FROM blob_texts WHERE sha256=$1`, d.SHA256[:]).Scan(&content, &method)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", errf(ErrNotFound, "no text available")
	}
	return content, method, err
}

// Reprocess re-runs processing for a document's current content.
func (s *Service) Reprocess(ctx context.Context, p *Principal, docID string) error {
	d, err := s.GetDocument(ctx, p, docID)
	if err != nil {
		return err
	}
	if err := s.requirePerm(ctx, s.DB, p, d.SpaceID, PermWrite); err != nil {
		return err
	}
	return s.tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE blob_processing SET status='pending', error='', updated_at=now() WHERE sha256=$1`, d.SHA256[:]); err != nil {
			return err
		}
		return enqueueProcess(ctx, tx, d.SHA256.Hex(), true)
	})
}

// ReprocessAll re-queues processing for blobs with the given statuses
// (admin only). Empty statuses = all blobs.
func (s *Service) ReprocessAll(ctx context.Context, p *Principal, statuses []string) (int, error) {
	if p == nil || (!p.IsAdmin && p.Via != "system") {
		return 0, errf(ErrForbidden, "admin only")
	}
	n := 0
	err := s.tx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `UPDATE blob_processing SET status='pending', error='', updated_at=now()
			WHERE (cardinality($1::text[]) = 0 OR status = ANY($1::text[]))
			  AND sha256 NOT IN (SELECT derived_sha256 FROM blob_derivatives)
			RETURNING sha256`, statuses)
		if err != nil {
			return err
		}
		var shas [][]byte
		for rows.Next() {
			var sh []byte
			rows.Scan(&sh)
			shas = append(shas, sh)
		}
		rows.Close()
		for _, sh := range shas {
			if err := enqueueProcess(ctx, tx, hex.EncodeToString(sh), true); err != nil {
				return err
			}
		}
		n = len(shas)
		return nil
	})
	return n, err
}
