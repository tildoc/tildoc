package core

import (
	"context"
	"encoding/hex"
	"errors"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/tildoc/tildoc/internal/blob"
)

// Document is the identity of a piece of content in a space.
type Document struct {
	ID            string          `json:"id"`
	SpaceID       string          `json:"space_id"`
	Version       int             `json:"version"`
	SHA256        blob.SHA256     `json:"sha256"`
	MD5           string          `json:"md5"`
	Size          int64           `json:"size"`
	MIME          string          `json:"mime"`
	Status        string          `json:"status"`
	Kind          string          `json:"kind"`
	Pages         *int            `json:"pages"`
	ProcessError  string          `json:"process_error,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
	DeletedAt     *time.Time      `json:"deleted_at"`
	ForkedFrom    *string         `json:"forked_from"`
	HasText       bool            `json:"has_text"`
	TextMethod    string          `json:"text_method,omitempty"`
	HasSearchable bool            `json:"has_searchable"`
	Versions      []VersionInfo   `json:"versions"`
	Links         []LinkRef       `json:"links"`
	// SameContent: places of other documents with identical current content
	// (e.g. the same file uploaded to a personal and a team space; stored and
	// processed once, but a separate document per space). Only places in
	// spaces the caller can read.
	SameContent []ContentPlace `json:"same_content"`
	Tags          []TagRef        `json:"tags"`
	Collections   []CollectionRef `json:"collections"` // the caller's collections containing it
	Variants      []Variant       `json:"variants"`    // derived variants of the current content
	ForeignOCR    bool            `json:"foreign_ocr"` // the original came with someone else's OCR layer
	OCRPreference string          `json:"ocr_preference"`
	Name          string          `json:"name"` // name of the first live link (or last deleted)
}

// Variant is a derived variant of the current content. Variants form a
// tree through Input: original -> searchable_pdf, or original ->
// image_only_pdf -> searchable_pdf.
type Variant struct {
	Kind       string         `json:"kind"` // searchable_pdf | image_only_pdf
	SHA256     blob.SHA256    `json:"sha256"`
	Input      blob.SHA256    `json:"input"`
	Engine     string         `json:"engine"`
	Meta       map[string]any `json:"meta"`
	Size       int64          `json:"size"`
	CreatedAt  time.Time      `json:"created_at"`
	SearchText bool           `json:"search_text"` // its text is used for search and the text view
}

// VersionInfo describes one version with its own variant tree.
type VersionInfo struct {
	Version   int               `json:"version"`
	SHA256    blob.SHA256       `json:"sha256"`
	Size      int64             `json:"size"`
	MIME      string            `json:"mime"`
	CreatedAt time.Time         `json:"created_at"`
	CreatedBy string            `json:"created_by"`
	Source    string            `json:"source"`
	Meta      map[string]string `json:"meta,omitempty"`
	Kind      string            `json:"kind"` // processing kind of its content (pdf-scanned, pdf-ocred, ...)
	Variants  []Variant         `json:"variants"`
	// RestoredFrom: this version re-added the content of an earlier one.
	RestoredFrom *int `json:"restored_from,omitempty"`
	// ContinuedFrom: the content equals a variant (or the original) of an
	// earlier version, e.g. tildoc's OCR PDF uploaded as a new version.
	// Derived from the content hashes, not stored.
	ContinuedFrom *Continuation `json:"continued_from,omitempty"`
}

// Continuation points at the node of the history a version continues from.
type Continuation struct {
	Version int    `json:"version"`
	Variant string `json:"variant,omitempty"` // "" = that version itself, else the variant kind
}

// linkHistory derives ContinuedFrom for versions sorted ascending: the
// nearest earlier version whose original or variant has the same content.
func linkHistory(vs []VersionInfo) {
	for k := range vs {
		if vs[k].RestoredFrom != nil {
			continue
		}
		for j := k - 1; j >= 0 && vs[k].ContinuedFrom == nil; j-- {
			if j < k-1 && vs[j].SHA256 == vs[k].SHA256 {
				vs[k].ContinuedFrom = &Continuation{Version: vs[j].Version}
				break
			}
			for _, v := range vs[j].Variants {
				if v.SHA256 == vs[k].SHA256 {
					vs[k].ContinuedFrom = &Continuation{Version: vs[j].Version, Variant: v.Kind}
					break
				}
			}
		}
	}
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

// ContentPlace is a place of another document with the same content.
type ContentPlace struct {
	LinkRef
	SpaceID    string `json:"space_id"`
	DocumentID string `json:"document_id"`
}

// sameContentPlaces lists live places of other documents whose current
// content equals sha, restricted to the spaces p can read.
func (s *Service) sameContentPlaces(ctx context.Context, p *Principal, docID string, sha blob.SHA256) ([]ContentPlace, error) {
	out := []ContentPlace{}
	spaces, err := s.readableSpaceIDs(ctx, p)
	if err != nil || len(spaces) == 0 {
		return out, err
	}
	rows, err := s.DB.Query(ctx, `
		SELECT e.id::text, e.parent_id::text, e.name, folder_path(e.parent_id) || '/' || e.name, e.original_path, e.import_id::text,
		       e.created_at, e.space_id::text, e.document_id::text
		FROM document_heads h JOIN entries e ON e.document_id = h.id AND e.kind = 'link' AND e.deleted_at IS NULL
		WHERE h.sha256 = $1 AND h.id <> $2 AND h.deleted_at IS NULL AND h.space_id = ANY($3::uuid[])
		ORDER BY e.created_at`, sha[:], docID, spaces)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var c ContentPlace
		if err := rows.Scan(&c.ID, &c.FolderID, &c.Name, &c.Path, &c.OriginalPath, &c.ImportID, &c.CreatedAt, &c.SpaceID, &c.DocumentID); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetDocument returns a document with versions and links (read permission).
func (s *Service) GetDocument(ctx context.Context, p *Principal, id string) (*Document, error) {
	if !looksLikeUUID(id) {
		return nil, errf(ErrNotFound, "document not found")
	}
	d := &Document{ID: id}
	var sha, md5 []byte
	err := s.DB.QueryRow(ctx, `
		SELECT d.space_id::text, d.current_version, d.sha256, b.md5, b.size, b.mime,
		       coalesce(bp.status,'pending'), coalesce(bp.kind,''), bp.pages, coalesce(bp.error,''),
		       d.created_at, d.updated_at, d.deleted_at, d.forked_from::text,
		       coalesce(t.method,''), t.sha256 IS NOT NULL,
		       EXISTS(SELECT 1 FROM blob_derivatives x WHERE x.source_sha256=d.sha256 AND x.kind='searchable_pdf')
		FROM document_heads d JOIN blobs b ON b.sha256=d.sha256
		LEFT JOIN blob_processing bp ON bp.sha256=d.sha256
		LEFT JOIN blob_texts t ON t.sha256=text_sha(d.sha256)
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

	rows, err := s.DB.Query(ctx, `SELECT v.version, v.sha256, b.size, b.mime, v.created_at, coalesce(u.username,''), v.source, v.meta,
		v.restored_from, coalesce(bp.kind, '')
		FROM document_versions v JOIN blobs b ON b.sha256=v.sha256 LEFT JOIN users u ON u.id=v.created_by
		LEFT JOIN blob_processing bp ON bp.sha256=v.sha256
		WHERE v.document_id=$1 ORDER BY v.version`, id)
	if err != nil {
		return nil, err
	}
	var shas [][]byte
	for rows.Next() {
		var v VersionInfo
		var vs []byte
		if err := rows.Scan(&v.Version, &vs, &v.Size, &v.MIME, &v.CreatedAt, &v.CreatedBy, &v.Source, &v.Meta, &v.RestoredFrom, &v.Kind); err != nil {
			rows.Close()
			return nil, err
		}
		copy(v.SHA256[:], vs)
		d.Versions = append(d.Versions, v)
		shas = append(shas, vs)
	}
	rows.Close()
	variants, err := s.variantsOf(ctx, shas)
	if err != nil {
		return nil, err
	}
	for i := range d.Versions {
		d.Versions[i].Variants = variants[d.Versions[i].SHA256]
		if d.Versions[i].Variants == nil {
			d.Versions[i].Variants = []Variant{}
		}
	}
	linkHistory(d.Versions)
	slices.Reverse(d.Versions) // newest first, as before

	rows, err = s.DB.Query(ctx, `SELECT id::text, coalesce(parent_id::text,''), name,
		CASE WHEN deleted_at IS NULL THEN folder_path(parent_id) || '/' || name ELSE deleted_path END,
		original_path, import_id::text, created_at, deleted_at IS NOT NULL
		FROM entries WHERE document_id=$1 AND (deleted_at IS NULL OR $2) ORDER BY created_at`, id, d.DeletedAt != nil)
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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	if d.Tags, err = documentTags(ctx, s.DB, id); err != nil {
		return nil, err
	}
	if p.UserID != "" {
		if d.Collections, err = s.documentCollections(ctx, p, id); err != nil {
			return nil, err
		}
	}
	if err := s.loadVariants(ctx, d); err != nil {
		return nil, err
	}
	if d.SameContent, err = s.sameContentPlaces(ctx, p, id, d.SHA256); err != nil {
		return nil, err
	}
	return d, nil
}

func (s *Service) loadVariants(ctx context.Context, d *Document) error {
	d.ForeignOCR = d.Kind == "pdf-ocred"
	if err := s.DB.QueryRow(ctx, `SELECT ocr_preference FROM instance_settings`).Scan(&d.OCRPreference); err != nil {
		return err
	}
	vs, err := s.variantsOf(ctx, [][]byte{d.SHA256[:]})
	if err != nil {
		return err
	}
	d.Variants = vs[d.SHA256]
	if d.Variants == nil {
		d.Variants = []Variant{}
	}
	return nil
}

// variantsOf returns the derived variants of several contents, by source.
func (s *Service) variantsOf(ctx context.Context, sources [][]byte) (map[blob.SHA256][]Variant, error) {
	out := map[blob.SHA256][]Variant{}
	rows, err := s.DB.Query(ctx, `SELECT x.source_sha256, x.kind, x.derived_sha256, x.input_sha256, x.engine, x.meta, b.size, x.created_at,
		x.derived_sha256 = text_sha(x.source_sha256)
		FROM blob_derivatives x JOIN blobs b ON b.sha256 = x.derived_sha256
		WHERE x.source_sha256 = ANY($1) ORDER BY x.created_at`, sources)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var v Variant
		var src, der, in []byte
		if err := rows.Scan(&src, &v.Kind, &der, &in, &v.Engine, &v.Meta, &v.Size, &v.CreatedAt, &v.SearchText); err != nil {
			return nil, err
		}
		copy(v.SHA256[:], der)
		copy(v.Input[:], in)
		var key blob.SHA256
		copy(key[:], src)
		out[key] = append(out[key], v)
	}
	return out, rows.Err()
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
// current; variant "searchable" returns tildoc's OCR PDF, "image_only" the
// original without its text layer.
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
	kinds := map[string]string{"searchable": "searchable_pdf", "image_only": "image_only_pdf"}
	if variant != "" {
		kind, ok := kinds[variant]
		if !ok {
			return nil, nil, errf(ErrInvalid, "unknown variant %q", variant)
		}
		var derived []byte
		err := s.DB.QueryRow(ctx, `SELECT derived_sha256 FROM blob_derivatives WHERE source_sha256=$1 AND kind=$2`,
			sha[:], kind).Scan(&derived)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, errf(ErrNotFound, "this variant does not exist")
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
	err = s.DB.QueryRow(ctx, `SELECT content, method FROM blob_texts WHERE sha256=text_sha($1)`, d.SHA256[:]).Scan(&content, &method)
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
