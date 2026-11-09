package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/tildoc/tildoc/internal/blob"
)

// userRef returns the user id for created_by columns (nil for system jobs).
func (p *Principal) userRef() *string {
	if p == nil || p.Via == "system" || p.UserID == "" {
		return nil
	}
	return &p.UserID
}

// PutOptions controls a file write.
type PutOptions struct {
	Protocol      string            // web | api | webdav | sftp | s3 ("" = api)
	ImportID      string            // explicit import batch (optional)
	ClientRoot    string            // client side root for new imports (optional)
	Meta          map[string]string // user metadata stored on the version
	CreateParents bool
}

// PutResult describes what a write did.
type PutResult struct {
	File         *FileInfo
	NewLink      bool // a new link was created
	NewVersion   bool // an existing document got a new version
	NewDocument  bool // a new document was created
	Deduplicated bool // content already existed in this space; link to existing document
	Forked       bool // copy-on-write fork of a multi-linked document
	NewBlob      bool // bytes were not stored before
}

// PutFile streams r into the space at path segs (last segment = file name).
func (s *Service) PutFile(ctx context.Context, p *Principal, spaceID string, segs []string, r io.Reader, opt PutOptions) (*PutResult, error) {
	if len(segs) == 0 {
		return nil, errf(ErrInvalid, "file name required")
	}
	if err := s.requirePerm(ctx, s.DB, p, spaceID, PermWrite); err != nil {
		return nil, err
	}
	st, err := s.Blobs.Stage(r, s.Cfg.MaxUploadBytes)
	if errors.Is(err, blob.ErrTooLarge) {
		return nil, errf(ErrTooLarge, "file exceeds the upload limit")
	}
	if err != nil {
		return nil, err
	}
	return s.PutStaged(ctx, p, spaceID, segs, st, opt)
}

// PutStaged commits an already staged blob at path segs. The staged blob is
// consumed in every case.
func (s *Service) PutStaged(ctx context.Context, p *Principal, spaceID string, segs []string, st *blob.Staged, opt PutOptions) (*PutResult, error) {
	if len(segs) == 0 {
		s.Blobs.Discard(st)
		return nil, errf(ErrInvalid, "file name required")
	}
	if err := s.requirePerm(ctx, s.DB, p, spaceID, PermWrite); err != nil {
		s.Blobs.Discard(st)
		return nil, err
	}
	if opt.Protocol == "" {
		opt.Protocol = "api"
	}
	c := s.claim(st)
	defer c.discardUnused()
	res := &PutResult{}
	var linkID string
	err := s.tx(ctx, func(tx pgx.Tx) error {
		if err := c.commit(ctx, tx); err != nil {
			return err
		}
		*res = PutResult{NewBlob: c.isNew}
		if err := registerBlobTx(ctx, tx, st); err != nil {
			return err
		}
		dirSegs, name := segs[:len(segs)-1], segs[len(segs)-1]
		var folderID string
		if opt.CreateParents {
			id, err := s.mkdirAllTx(ctx, tx, p, spaceID, dirSegs)
			if err != nil {
				return err
			}
			folderID = id
		} else {
			id, n, err := resolveFolder(ctx, tx, spaceID, dirSegs)
			if err != nil {
				return err
			}
			if n != len(dirSegs) {
				return errf(ErrNotFound, "parent folder %q not found", JoinPath(dirSegs...))
			}
			folderID = id
		}
		if _, err := lockFolder(ctx, tx, folderID); err != nil {
			return err
		}
		fold, existing, err := child(ctx, tx, folderID, name)
		if err != nil {
			return err
		}
		if fold != "" {
			return errf(ErrIsFolder, "%q is a folder", name)
		}
		if existing == "" {
			if existing, err = s.reviveTx(ctx, tx, p, folderID, name); err != nil {
				return err
			}
		}
		if existing != "" {
			linkID = existing
			changed, forked, err := s.writeVersionTx(ctx, tx, p, existing, st.SHA256, opt.Protocol, opt.Meta)
			res.NewVersion, res.Forked = changed, forked
			return err
		}
		// serialize concurrent ingests of the same content into the same space
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, spaceID+st.SHA256.Hex()); err != nil {
			return err
		}
		docID, err := findDocBySHA(ctx, tx, spaceID, st.SHA256)
		if err != nil {
			return err
		}
		if docID == "" {
			docID, err = createDocTx(ctx, tx, p, spaceID, st.SHA256, opt.Protocol, opt.Meta)
			if err != nil {
				return err
			}
			res.NewDocument = true
		} else {
			res.Deduplicated = true
		}
		importID, err := s.importTx(ctx, tx, p, spaceID, opt)
		if err != nil {
			return err
		}
		linkID, err = createLinkTx(ctx, tx, p, docID, folderID, name, importID, JoinPath(segs...))
		res.NewLink = true
		return err
	})
	if err != nil {
		return nil, err
	}
	res.File, err = scanFile(s.DB.QueryRow(ctx, fileSelect+` WHERE l.id=$1`, linkID))
	return res, err
}

// registerBlobTx records blob metadata and schedules processing for new content.
func registerBlobTx(ctx context.Context, tx pgx.Tx, st *blob.Staged) error {
	_, err := tx.Exec(ctx, `INSERT INTO blobs(sha256, md5, size, mime) VALUES($1,$2,$3,$4)
		ON CONFLICT (sha256) DO NOTHING`, st.SHA256[:], st.MD5[:], st.Size, DetectMIME(st.Head))
	if err != nil {
		return err
	}
	return ensureProcessingTx(ctx, tx, st.SHA256)
}

func ensureProcessingTx(ctx context.Context, tx pgx.Tx, sha blob.SHA256) error {
	tag, err := tx.Exec(ctx, `INSERT INTO blob_processing(sha256, status) VALUES($1, 'pending') ON CONFLICT DO NOTHING`, sha[:])
	if err != nil {
		return err
	}
	if tag.RowsAffected() > 0 {
		return enqueueProcess(ctx, tx, sha.Hex(), false)
	}
	return nil
}

func findDocBySHA(ctx context.Context, tx pgx.Tx, spaceID string, sha blob.SHA256) (string, error) {
	var id string
	err := tx.QueryRow(ctx, `SELECT d.id::text FROM documents d
		JOIN document_versions v ON v.document_id = d.id AND v.version = d.current_version
		WHERE d.space_id=$1 AND v.sha256=$2 AND d.deleted_at IS NULL
		ORDER BY d.created_at LIMIT 1 FOR UPDATE OF d`, spaceID, sha[:]).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return id, err
}

func createDocTx(ctx context.Context, tx pgx.Tx, p *Principal, spaceID string, sha blob.SHA256, source string, meta map[string]string) (string, error) {
	var id string
	if meta == nil {
		meta = map[string]string{}
	}
	err := tx.QueryRow(ctx, `INSERT INTO documents(space_id, current_version, created_by)
		VALUES($1,1,$2) RETURNING id::text`, spaceID, p.userRef()).Scan(&id)
	if err != nil {
		return "", err
	}
	_, err = tx.Exec(ctx, `INSERT INTO document_versions(document_id, version, sha256, created_by, source, meta)
		VALUES($1,1,$2,$3,$4,$5)`, id, sha[:], p.userRef(), source, meta)
	return id, err
}

func createLinkTx(ctx context.Context, tx pgx.Tx, p *Principal, docID, folderID, name string, importID *string, origPath string) (string, error) {
	var id string
	err := tx.QueryRow(ctx, `INSERT INTO entries(space_id, kind, document_id, parent_id, name, import_id, original_path, created_by)
		SELECT space_id, 'link', id, $2, $3, $4, $5, $6 FROM documents WHERE id=$1 RETURNING id::text`,
		docID, folderID, name, importID, origPath, p.userRef()).Scan(&id)
	return id, err
}

// writeVersionTx appends a version with sha to the document behind linkID.
// If the document has more than one live link, the link is forked first
// (copy-on-write). Returns whether anything changed and whether it forked.
func (s *Service) writeVersionTx(ctx context.Context, tx pgx.Tx, p *Principal, linkID string, sha blob.SHA256, source string, meta map[string]string) (bool, bool, error) {
	var docID string
	if err := tx.QueryRow(ctx, `SELECT document_id::text FROM entries WHERE id=$1 AND kind='link'`, linkID).Scan(&docID); err != nil {
		return false, false, err
	}
	var cur []byte
	var ver, links int
	var spaceID string
	err := tx.QueryRow(ctx, `SELECT v.sha256, d.current_version, d.space_id::text,
		(SELECT count(*) FROM entries WHERE document_id=d.id AND deleted_at IS NULL)
		FROM documents d JOIN document_versions v ON v.document_id = d.id AND v.version = d.current_version
		WHERE d.id=$1 FOR UPDATE OF d`, docID).Scan(&cur, &ver, &spaceID, &links)
	if err != nil {
		return false, false, err
	}
	if string(cur) == string(sha[:]) {
		return false, false, nil
	}
	if meta == nil {
		meta = map[string]string{}
	}
	forked := false
	if links > 1 {
		newDoc, err := forkDocTx(ctx, tx, p, docID, spaceID)
		if err != nil {
			return false, false, err
		}
		if _, err := tx.Exec(ctx, `UPDATE entries SET document_id=$2, updated_at=now() WHERE id=$1`, linkID, newDoc); err != nil {
			return false, false, err
		}
		docID, forked = newDoc, true
	}
	if _, err := tx.Exec(ctx, `INSERT INTO document_versions(document_id, version, sha256, created_by, source, meta)
		VALUES($1,$2,$3,$4,$5,$6)`, docID, ver+1, sha[:], p.userRef(), source, meta); err != nil {
		return false, false, err
	}
	_, err = tx.Exec(ctx, `UPDATE documents SET current_version=$2, updated_at=now() WHERE id=$1`, docID, ver+1)
	return true, forked, err
}

// forkDocTx copies a document including its full version history into
// targetSpace and returns the new document id.
func forkDocTx(ctx context.Context, tx pgx.Tx, p *Principal, docID, targetSpace string) (string, error) {
	var id string
	err := tx.QueryRow(ctx, `INSERT INTO documents(space_id, current_version, created_by, forked_from, created_at, updated_at)
		SELECT $2, current_version, $3, id, created_at, updated_at FROM documents WHERE id=$1
		RETURNING id::text`, docID, targetSpace, p.userRef()).Scan(&id)
	if err != nil {
		return "", err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO document_versions(document_id, version, sha256, created_at, created_by, source, meta, restored_from)
		SELECT $2, version, sha256, created_at, created_by, source, meta, restored_from FROM document_versions WHERE document_id=$1`, docID, id); err != nil {
		return "", err
	}
	return id, copyDocRelationsTx(ctx, tx, docID, id)
}

// importTx returns the import batch for a write: the explicit one, or the
// caller's current batch for (protocol, space), opening a new one after 10
// minutes of inactivity.
func (s *Service) importTx(ctx context.Context, tx pgx.Tx, p *Principal, spaceID string, opt PutOptions) (*string, error) {
	if p.userRef() == nil {
		return nil, nil
	}
	proto := opt.Protocol
	if proto == "" {
		proto = "api"
	}
	if opt.ImportID != "" {
		tag, err := tx.Exec(ctx, `UPDATE imports SET last_activity_at=now() WHERE id=$1 AND user_id=$2 AND space_id=$3`,
			opt.ImportID, p.UserID, spaceID)
		if err != nil {
			return nil, err
		}
		if tag.RowsAffected() == 0 {
			return nil, errf(ErrInvalid, "unknown import")
		}
		return &opt.ImportID, nil
	}
	var id string
	err := tx.QueryRow(ctx, `UPDATE imports SET last_activity_at=now() WHERE id = (
		SELECT id FROM imports WHERE user_id=$1 AND protocol=$2 AND space_id=$3 AND last_activity_at > now() - interval '10 minutes'
		ORDER BY last_activity_at DESC LIMIT 1) RETURNING id::text`, p.UserID, proto, spaceID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `INSERT INTO imports(user_id, space_id, protocol, client_root) VALUES($1,$2,$3,$4) RETURNING id::text`,
			p.UserID, spaceID, proto, opt.ClientRoot).Scan(&id)
	}
	if err != nil {
		return nil, err
	}
	return &id, nil
}

// Import is an upload batch.
type Import struct {
	ID         string    `json:"id"`
	SpaceID    string    `json:"space_id"`
	Protocol   string    `json:"protocol"`
	ClientRoot string    `json:"client_root"`
	CreatedAt  time.Time `json:"created_at"`
	LastActive time.Time `json:"last_activity_at"`
	Files      int       `json:"files"`
}

// CreateImport opens an explicit upload batch (used by the web UI).
func (s *Service) CreateImport(ctx context.Context, p *Principal, spaceID, protocol, clientRoot string) (*Import, error) {
	if err := s.requirePerm(ctx, s.DB, p, spaceID, PermWrite); err != nil {
		return nil, err
	}
	im := &Import{SpaceID: spaceID, Protocol: protocol, ClientRoot: clientRoot}
	err := s.DB.QueryRow(ctx, `INSERT INTO imports(user_id, space_id, protocol, client_root) VALUES($1,$2,$3,$4)
		RETURNING id::text, created_at, last_activity_at`, p.UserID, spaceID, protocol, clientRoot).
		Scan(&im.ID, &im.CreatedAt, &im.LastActive)
	return im, err
}

// ListImports lists the caller's imports.
func (s *Service) ListImports(ctx context.Context, p *Principal, limit int) ([]Import, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.DB.Query(ctx, `SELECT i.id::text, i.space_id::text, i.protocol, i.client_root, i.created_at, i.last_activity_at,
		(SELECT count(*) FROM entries l WHERE l.import_id = i.id)
		FROM imports i WHERE i.user_id=$1 ORDER BY i.last_activity_at DESC LIMIT $2`, p.UserID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Import{}
	for rows.Next() {
		var im Import
		if err := rows.Scan(&im.ID, &im.SpaceID, &im.Protocol, &im.ClientRoot, &im.CreatedAt, &im.LastActive, &im.Files); err != nil {
			return nil, err
		}
		out = append(out, im)
	}
	return out, rows.Err()
}

// AddDocumentVersion uploads a new version to a document itself. Unlike
// writes through a link this never forks: all links see the new version.
func (s *Service) AddDocumentVersion(ctx context.Context, p *Principal, docID string, r io.Reader, source string) (*Document, error) {
	d, err := s.GetDocument(ctx, p, docID)
	if err != nil {
		return nil, err
	}
	if d.DeletedAt != nil {
		return nil, errf(ErrInvalid, "document is in trash")
	}
	if err := s.requirePerm(ctx, s.DB, p, d.SpaceID, PermWrite); err != nil {
		return nil, err
	}
	st, err := s.Blobs.Stage(r, s.Cfg.MaxUploadBytes)
	if errors.Is(err, blob.ErrTooLarge) {
		return nil, errf(ErrTooLarge, "file exceeds the upload limit")
	}
	if err != nil {
		return nil, err
	}
	c := s.claim(st)
	defer c.discardUnused()
	err = s.tx(ctx, func(tx pgx.Tx) error {
		if err := c.commit(ctx, tx); err != nil {
			return err
		}
		if err := registerBlobTx(ctx, tx, st); err != nil {
			return err
		}
		var cur []byte
		var ver int
		if err := tx.QueryRow(ctx, `SELECT v.sha256, d.current_version FROM documents d
				JOIN document_versions v ON v.document_id = d.id AND v.version = d.current_version
				WHERE d.id=$1 FOR UPDATE OF d`, docID).Scan(&cur, &ver); err != nil {
			return err
		}
		if string(cur) == string(st.SHA256[:]) {
			return nil
		}
		if _, err := tx.Exec(ctx, `INSERT INTO document_versions(document_id, version, sha256, created_by, source)
			VALUES($1,$2,$3,$4,$5)`, docID, ver+1, st.SHA256[:], p.userRef(), source); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE documents SET current_version=$2, updated_at=now() WHERE id=$1`, docID, ver+1)
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.GetDocument(ctx, p, docID)
}

// RestoreVersion makes an old version current again (as a new version).
func (s *Service) RestoreVersion(ctx context.Context, p *Principal, docID string, version int) (*Document, error) {
	d, err := s.GetDocument(ctx, p, docID)
	if err != nil {
		return nil, err
	}
	if err := s.requirePerm(ctx, s.DB, p, d.SpaceID, PermWrite); err != nil {
		return nil, err
	}
	err = s.tx(ctx, func(tx pgx.Tx) error {
		var sha []byte
		err := tx.QueryRow(ctx, `SELECT sha256 FROM document_versions WHERE document_id=$1 AND version=$2`, docID, version).Scan(&sha)
		if errors.Is(err, pgx.ErrNoRows) {
			return errf(ErrNotFound, "version not found")
		}
		if err != nil {
			return err
		}
		var ver int
		var cur []byte
		if err := tx.QueryRow(ctx, `SELECT d.current_version, v.sha256 FROM documents d
				JOIN document_versions v ON v.document_id = d.id AND v.version = d.current_version
				WHERE d.id=$1 FOR UPDATE OF d`, docID).Scan(&ver, &cur); err != nil {
			return err
		}
		if string(cur) == string(sha) {
			return nil
		}
		if _, err := tx.Exec(ctx, `INSERT INTO document_versions(document_id, version, sha256, created_by, source, restored_from)
			VALUES($1,$2,$3,$4,'restore',$5)`, docID, ver+1, sha, p.userRef(), version); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE documents SET current_version=$2, updated_at=now() WHERE id=$1`, docID, ver+1)
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.GetDocument(ctx, p, docID)
}

// uniqueName returns name or "name (n).ext" not taken in folder.
func uniqueName(ctx context.Context, tx pgx.Tx, folderID, name, tag string) (string, error) {
	ext := path.Ext(name)
	base := strings.TrimSuffix(name, ext)
	if ext == name {
		ext, base = "", name
	}
	cand := name
	for i := 1; i < 1000; i++ {
		f, l, err := child(ctx, tx, folderID, cand)
		if err != nil {
			return "", err
		}
		if f == "" && l == "" {
			return cand, nil
		}
		if i == 1 {
			cand = fmt.Sprintf("%s (%s)%s", base, tag, ext)
		} else {
			cand = fmt.Sprintf("%s (%s %d)%s", base, tag, i, ext)
		}
	}
	return "", errf(ErrConflict, "no free name for %q", name)
}
