package core

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/tildoc/tildoc/internal/blob"
)

type linkRow struct {
	ID, DocID, FolderID, Name, SpaceID string
	CreatedAt                          time.Time
	CreatedBy                          *string
}

func loadLinkTx(ctx context.Context, q pgx.Tx, linkID string) (*linkRow, error) {
	l := &linkRow{ID: linkID}
	err := q.QueryRow(ctx, `SELECT document_id::text, parent_id::text, name, space_id::text, created_at, created_by::text
		FROM entries WHERE id=$1 AND kind='link' AND deleted_at IS NULL FOR UPDATE`, linkID).
		Scan(&l.DocID, &l.FolderID, &l.Name, &l.SpaceID, &l.CreatedAt, &l.CreatedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errf(ErrNotFound, "file not found")
	}
	return l, err
}

// softDeleteLinkTx moves a link to the trash and trashes its document when
// it was the last live link.
func softDeleteLinkTx(ctx context.Context, tx pgx.Tx, linkID string) error {
	var docID string
	err := tx.QueryRow(ctx, `UPDATE entries SET deleted_at=now(), deleted_path=folder_path(parent_id) || '/' || name, updated_at=now()
		WHERE id=$1 AND kind='link' AND deleted_at IS NULL RETURNING document_id::text`, linkID).Scan(&docID)
	if errors.Is(err, pgx.ErrNoRows) {
		return errf(ErrNotFound, "file not found")
	}
	if err != nil {
		return err
	}
	return trashOrphans(ctx, tx, []string{docID})
}

// retireLinkTx removes the source link after its content was written over
// another link (rename-over). Transient uploads (KIO ".part" files, editor
// temp files: single version, younger than the provenance window, no other
// links) are removed completely instead of filling the trash.
func (s *Service) retireLinkTx(ctx context.Context, tx pgx.Tx, l *linkRow) error {
	var others, versions int
	var created time.Time
	err := tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM entries WHERE document_id=d.id AND deleted_at IS NULL AND id<>$2),
		d.current_version, d.created_at FROM documents d WHERE d.id=$1`, l.DocID, l.ID).Scan(&others, &versions, &created)
	if err != nil {
		return err
	}
	if others == 0 && versions == 1 && s.now().Sub(created) < s.Cfg.ProvenanceWindow {
		_, err := tx.Exec(ctx, `DELETE FROM documents WHERE id=$1`, l.DocID)
		return err
	}
	return softDeleteLinkTx(ctx, tx, l.ID)
}

// fileProtocol reports whether the caller is a file protocol client
// (WebDAV, SFTP, S3). Such clients save via temp file + rename; a rename
// shortly after creation then still belongs to the upload and updates the
// link's provenance.
func (p *Principal) fileProtocol() bool { return p.Via == "accesskey" || p.Via == "ssh" }

// reviveTx handles clients that replace files via DELETE + upload (or
// DELETE + rename of a temp file): if a protocol client writes to a path
// whose link was deleted within the provenance window and whose document
// is still in the trash, that link and document are restored so the write
// becomes a new version instead of a new document. Returns the revived
// link id or "".
func (s *Service) reviveTx(ctx context.Context, tx pgx.Tx, p *Principal, folderID, name string) (string, error) {
	if !p.fileProtocol() {
		return "", nil
	}
	var linkID, docID string
	err := tx.QueryRow(ctx, `SELECT l.id::text, l.document_id::text FROM entries l JOIN documents d ON d.id = l.document_id
		WHERE l.parent_id=$1 AND l.name=$2 AND l.deleted_at > now() - make_interval(secs => $3) AND d.deleted_at IS NOT NULL
		ORDER BY l.deleted_at DESC LIMIT 1 FOR UPDATE OF l, d`, folderID, name, s.Cfg.ProvenanceWindow.Seconds()).Scan(&linkID, &docID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `UPDATE entries SET deleted_at=NULL, deleted_path=NULL, updated_at=now() WHERE id=$1`, linkID); err != nil {
		return "", err
	}
	_, err = tx.Exec(ctx, `UPDATE documents SET deleted_at=NULL WHERE id=$1`, docID)
	return linkID, err
}

// moveLinkTx moves/renames a link. With overwrite, an existing file at the
// destination receives the content as a new version (history preserved).
func (s *Service) moveLinkTx(ctx context.Context, tx pgx.Tx, p *Principal, linkID, dstFolderID, newName string, overwrite bool) (string, error) {
	l, err := loadLinkTx(ctx, tx, linkID)
	if err != nil {
		return "", err
	}
	if newName == "" {
		newName = l.Name
	}
	if err := lockFolders(ctx, tx, l.FolderID, dstFolderID); err != nil {
		return "", err
	}
	var dstSpace string
	if err := tx.QueryRow(ctx, `SELECT space_id::text FROM entries WHERE id=$1 AND kind='folder'`, dstFolderID).Scan(&dstSpace); err != nil {
		return "", err
	}
	fold, other, err := child(ctx, tx, dstFolderID, newName)
	if err != nil {
		return "", err
	}
	if fold != "" {
		return "", errf(ErrIsFolder, "%q is a folder", newName)
	}
	if other == linkID {
		return linkID, nil
	}
	if other == "" && dstSpace == l.SpaceID {
		if other, err = s.reviveTx(ctx, tx, p, dstFolderID, newName); err != nil {
			return "", err
		}
		if other != "" {
			overwrite = true
		}
	}
	if other != "" {
		if !overwrite {
			return "", errf(ErrConflict, "%q already exists", newName)
		}
		var otherDoc string
		if err := tx.QueryRow(ctx, `SELECT document_id::text FROM entries WHERE id=$1`, other).Scan(&otherDoc); err != nil {
			return "", err
		}
		if otherDoc != l.DocID {
			var sha []byte
			var meta map[string]string
			if err := tx.QueryRow(ctx, `SELECT sha256, meta FROM document_heads WHERE id=$1`, l.DocID).Scan(&sha, &meta); err != nil {
				return "", err
			}
			var h [32]byte
			copy(h[:], sha)
			if _, _, err := s.writeVersionTx(ctx, tx, p, other, h, "rename", meta); err != nil {
				return "", err
			}
		}
		return other, s.retireLinkTx(ctx, tx, l)
	}
	if dstSpace == l.SpaceID {
		_, err = tx.Exec(ctx, `UPDATE entries SET parent_id=$2, name=$3, updated_at=now(),
			original_path = CASE WHEN $6 AND created_at > now() - make_interval(secs => $4) AND created_by IS NOT DISTINCT FROM $5::uuid
				THEN folder_path($2) || '/' || $3 ELSE original_path END
			WHERE id=$1`, linkID, dstFolderID, newName, s.Cfg.ProvenanceWindow.Seconds(), p.userRef(), p.fileProtocol())
		return linkID, err
	}
	// cross-space move
	var live int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM entries WHERE document_id=$1 AND deleted_at IS NULL`, l.DocID).Scan(&live); err != nil {
		return "", err
	}
	var sha blob.SHA256
	var raw []byte
	if err := tx.QueryRow(ctx, `SELECT sha256 FROM document_heads WHERE id=$1`, l.DocID).Scan(&raw); err != nil {
		return "", err
	}
	copy(sha[:], raw)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, dstSpace+sha.Hex()); err != nil {
		return "", err
	}
	target, err := findDocBySHA(ctx, tx, dstSpace, sha)
	if err != nil {
		return "", err
	}
	if target != "" {
		// The destination already holds this content (one document per
		// content and space): the link joins that document, whose history
		// absorbs the moved document's versions.
		if err := mergeHistoryTx(ctx, tx, target, l.DocID); err != nil {
			return "", err
		}
		if err := copyDocRelationsTx(ctx, tx, l.DocID, target); err != nil {
			return "", err
		}
		if _, err := tx.Exec(ctx, `UPDATE entries SET document_id=$2, space_id=$3, parent_id=$4, name=$5, updated_at=now() WHERE id=$1`,
			linkID, target, dstSpace, dstFolderID, newName); err != nil {
			return "", err
		}
		if live == 1 {
			_, err = tx.Exec(ctx, `DELETE FROM documents WHERE id=$1`, l.DocID)
		}
		return linkID, err
	}
	docID := l.DocID
	if live == 1 {
		if _, err := tx.Exec(ctx, `DELETE FROM entries WHERE document_id=$1 AND deleted_at IS NOT NULL`, docID); err != nil {
			return "", err
		}
		if _, err := tx.Exec(ctx, `UPDATE documents SET space_id=$2 WHERE id=$1`, docID, dstSpace); err != nil {
			return "", err
		}
		// a share was given for the old space; the new space's people didn't give it
		if _, err := tx.Exec(ctx, `DELETE FROM grants WHERE document_id=$1`, docID); err != nil {
			return "", err
		}
		if _, err := tx.Exec(ctx, `UPDATE share_links SET revoked_at = now() WHERE document_id=$1 AND revoked_at IS NULL`, docID); err != nil {
			return "", err
		}
	} else {
		if docID, err = forkDocTx(ctx, tx, p, l.DocID, dstSpace); err != nil {
			return "", err
		}
	}
	_, err = tx.Exec(ctx, `UPDATE entries SET document_id=$2, space_id=$3, parent_id=$4, name=$5, updated_at=now() WHERE id=$1`,
		linkID, docID, dstSpace, dstFolderID, newName)
	return linkID, err
}

// mergeHistoryTx merges the versions of document from into document into:
// both histories are combined in chronological order and renumbered;
// versions already present (same content and time) are not duplicated. Both
// documents currently have the same content, so the newest version is that
// content in any case. from is left unchanged.
func mergeHistoryTx(ctx context.Context, tx pgx.Tx, into, from string) error {
	if _, err := tx.Exec(ctx, `SELECT 1 FROM documents WHERE id = ANY($1::uuid[]) ORDER BY id FOR UPDATE`, []string{into, from}); err != nil {
		return err
	}
	type ver struct {
		sha          []byte
		createdAt    time.Time
		createdBy    *string
		source       string
		meta         map[string]any
		doc          string // document the version came from
		version      int    // its number there
		restoredFrom *int   // in that numbering
	}
	rows, err := tx.Query(ctx, `
		SELECT sha256, created_at, created_by::text, source, meta, document_id::text, version, restored_from FROM (
			SELECT sha256, created_at, created_by, source, meta, 0 AS o, version, document_id, restored_from
			FROM document_versions WHERE document_id = $1
			UNION ALL
			SELECT sha256, created_at, created_by, source, meta || jsonb_build_object('merged_from', document_id::text), 1, version, document_id, restored_from
			FROM document_versions f WHERE document_id = $2
		) x ORDER BY created_at, o, version`, into, from)
	if err != nil {
		return err
	}
	var all []ver
	// renumbering: (document, old version) -> new version; a version of from
	// that into already has (same content and time) maps to into's copy
	type key struct {
		doc string
		v   int
	}
	newNum := map[key]int{}
	seen := map[string]int{} // sha + time of into's versions -> new number
	for rows.Next() {
		var v ver
		if err := rows.Scan(&v.sha, &v.createdAt, &v.createdBy, &v.source, &v.meta, &v.doc, &v.version, &v.restoredFrom); err != nil {
			rows.Close()
			return err
		}
		id := string(v.sha) + v.createdAt.UTC().Format(time.RFC3339Nano)
		if v.doc == from {
			if n, dup := seen[id]; dup {
				newNum[key{v.doc, v.version}] = n
				continue
			}
		}
		all = append(all, v)
		newNum[key{v.doc, v.version}] = len(all)
		if v.doc == into {
			seen[id] = len(all)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM document_versions WHERE document_id=$1`, into); err != nil {
		return err
	}
	for i, v := range all {
		var restored *int
		if v.restoredFrom != nil {
			if n, ok := newNum[key{v.doc, *v.restoredFrom}]; ok && n < i+1 {
				restored = &n
			}
		}
		source := v.source
		if source == "restore" && restored == nil {
			source = "api" // cannot happen with consistent data; keeps the constraint
		}
		if _, err := tx.Exec(ctx, `INSERT INTO document_versions(document_id, version, sha256, created_at, created_by, source, meta, restored_from)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, into, i+1, v.sha, v.createdAt, v.createdBy, source, v.meta, restored); err != nil {
			return err
		}
	}
	_, err = tx.Exec(ctx, `UPDATE documents d SET current_version=$3, updated_at=now(),
		created_at = least(d.created_at, (SELECT created_at FROM documents WHERE id=$2)) WHERE d.id=$1`, into, from, len(all))
	return err
}

// copyDocRelationsTx gives document to the tags and collection memberships
// of document from (used when a document is split or merged).
func copyDocRelationsTx(ctx context.Context, tx pgx.Tx, from, to string) error {
	if _, err := tx.Exec(ctx, `INSERT INTO document_tags(document_id, tag_id, added_by, added_at)
		SELECT $2, tag_id, added_by, added_at FROM document_tags WHERE document_id=$1 ON CONFLICT DO NOTHING`, from, to); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO collection_items(collection_id, document_id, added_by, added_at)
		SELECT collection_id, $2, added_by, added_at FROM collection_items WHERE document_id=$1 ON CONFLICT DO NOTHING`, from, to)
	return err
}

// MoveLink moves and/or renames a file. dstFolderID "" keeps the folder,
// newName "" keeps the name.
func (s *Service) MoveLink(ctx context.Context, p *Principal, linkID, dstFolderID, newName string, overwrite bool) (*FileInfo, error) {
	f, err := s.GetFile(ctx, p, linkID)
	if err != nil {
		return nil, err
	}
	if dstFolderID == "" {
		dstFolderID = f.FolderID
	}
	if newName != "" {
		if newName, err = NormalizeName(newName); err != nil {
			return nil, err
		}
	}
	dst, err := s.GetFolder(ctx, p, dstFolderID)
	if err != nil {
		return nil, err
	}
	need := CapWrite
	if dst.SpaceID != f.SpaceID {
		need = CapMove
	}
	if err := s.requireCap(ctx, s.DB, p, f.SpaceID, need); err != nil {
		return nil, err
	}
	if err := s.requirePerm(ctx, s.DB, p, dst.SpaceID, PermWrite); err != nil {
		return nil, err
	}
	var result string
	err = s.tx(ctx, func(tx pgx.Tx) error {
		var err error
		result, err = s.moveLinkTx(ctx, tx, p, linkID, dstFolderID, newName, overwrite)
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.GetFile(ctx, p, result)
}

func (s *Service) copyLinkTx(ctx context.Context, tx pgx.Tx, p *Principal, linkID, dstFolderID, newName string, overwrite bool) (string, error) {
	var docID, srcSpace, name string
	var sha []byte
	err := tx.QueryRow(ctx, `SELECT l.document_id::text, l.space_id::text, l.name, d.sha256 FROM entries l
		JOIN document_heads d ON d.id=l.document_id WHERE l.id=$1 AND l.kind='link' AND l.deleted_at IS NULL`, linkID).Scan(&docID, &srcSpace, &name, &sha)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", errf(ErrNotFound, "file not found")
	}
	if err != nil {
		return "", err
	}
	if newName == "" {
		newName = name
	}
	dstSpace, err := lockFolder(ctx, tx, dstFolderID)
	if err != nil {
		return "", err
	}
	fold, other, err := child(ctx, tx, dstFolderID, newName)
	if err != nil {
		return "", err
	}
	if fold != "" {
		return "", errf(ErrIsFolder, "%q is a folder", newName)
	}
	if other == linkID {
		return linkID, nil
	}
	var h [32]byte
	copy(h[:], sha)
	if other != "" {
		if !overwrite {
			return "", errf(ErrConflict, "%q already exists", newName)
		}
		_, _, err := s.writeVersionTx(ctx, tx, p, other, h, "copy", nil)
		return other, err
	}
	target := docID
	if dstSpace != srcSpace {
		if target, err = findDocBySHA(ctx, tx, dstSpace, h); err != nil {
			return "", err
		}
		if target == "" {
			if target, err = forkDocTx(ctx, tx, p, docID, dstSpace); err != nil {
				return "", err
			}
		}
	}
	var path string
	if err := tx.QueryRow(ctx, `SELECT folder_path($1) || '/' || $2`, dstFolderID, newName).Scan(&path); err != nil {
		return "", err
	}
	return createLinkTx(ctx, tx, p, target, dstFolderID, newName, nil, path)
}

// CopyLink copies a file. Within a space the copy is a new link to the same
// document (no storage, shared history until one side is written:
// copy-on-write). Across spaces the content is deduplicated by hash.
func (s *Service) CopyLink(ctx context.Context, p *Principal, linkID, dstFolderID, newName string, overwrite bool) (*FileInfo, error) {
	src, err := s.GetFile(ctx, p, linkID)
	if err != nil {
		return nil, err
	}
	if err := s.requireDocCap(ctx, s.DB, p, src.DocumentID, src.SpaceID, CapDownload); err != nil { // a copy is a download
		return nil, err
	}
	if newName != "" {
		var err error
		if newName, err = NormalizeName(newName); err != nil {
			return nil, err
		}
	}
	dst, err := s.GetFolder(ctx, p, dstFolderID)
	if err != nil {
		return nil, err
	}
	if err := s.requirePerm(ctx, s.DB, p, dst.SpaceID, PermWrite); err != nil {
		return nil, err
	}
	var result string
	err = s.tx(ctx, func(tx pgx.Tx) error {
		var err error
		result, err = s.copyLinkTx(ctx, tx, p, linkID, dstFolderID, newName, overwrite)
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.GetFile(ctx, p, result)
}

// CopyFolder copies a folder tree. The destination must not exist.
func (s *Service) CopyFolder(ctx context.Context, p *Principal, folderID, dstParentID, newName string) (*FolderInfo, error) {
	src, err := s.GetFolder(ctx, p, folderID)
	if err != nil {
		return nil, err
	}
	if err := s.requireCap(ctx, s.DB, p, src.SpaceID, CapDownload); err != nil { // a copy is a download
		return nil, err
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
	if err := s.requirePerm(ctx, s.DB, p, dst.SpaceID, PermWrite); err != nil {
		return nil, err
	}
	var newID string
	err = s.tx(ctx, func(tx pgx.Tx) error {
		if anc, err := isAncestor(ctx, tx, folderID, dstParentID); err != nil {
			return err
		} else if anc {
			return errf(ErrInvalid, "cannot copy a folder into itself")
		}
		var err error
		newID, err = s.copyTreeTx(ctx, tx, p, folderID, dstParentID, newName)
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.folderInfo(ctx, s.DB, newID)
}

func (s *Service) copyTreeTx(ctx context.Context, tx pgx.Tx, p *Principal, srcID, dstParentID, name string) (string, error) {
	newID, err := s.mkdirTx(ctx, tx, p, dstParentID, name)
	if err != nil {
		return "", err
	}
	type item struct{ id, name string }
	collect := func(sql string) ([]item, error) {
		rows, err := tx.Query(ctx, sql, srcID)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []item
		for rows.Next() {
			var it item
			if err := rows.Scan(&it.id, &it.name); err != nil {
				return nil, err
			}
			out = append(out, it)
		}
		return out, rows.Err()
	}
	links, err := collect(`SELECT id::text, name FROM entries WHERE parent_id=$1 AND kind='link' AND deleted_at IS NULL`)
	if err != nil {
		return "", err
	}
	for _, l := range links {
		if _, err := s.copyLinkTx(ctx, tx, p, l.id, newID, "", false); err != nil {
			return "", err
		}
	}
	subs, err := collect(`SELECT id::text, name FROM entries WHERE parent_id=$1 AND kind='folder'`)
	if err != nil {
		return "", err
	}
	for _, c := range subs {
		if _, err := s.copyTreeTx(ctx, tx, p, c.id, newID, c.name); err != nil {
			return "", err
		}
	}
	return newID, nil
}

// AddLink makes an existing document additionally appear at folder/name.
func (s *Service) AddLink(ctx context.Context, p *Principal, docID, folderID, name string) (*FileInfo, error) {
	name, err := NormalizeName(name)
	if err != nil {
		return nil, err
	}
	d, err := s.GetDocument(ctx, p, docID)
	if err != nil {
		return nil, err
	}
	if d.DeletedAt != nil {
		return nil, errf(ErrInvalid, "document is in trash")
	}
	dst, err := s.GetFolder(ctx, p, folderID)
	if err != nil {
		return nil, err
	}
	if dst.SpaceID != d.SpaceID {
		return nil, errf(ErrInvalid, "links must stay within the document's space (use copy)")
	}
	if err := s.requirePerm(ctx, s.DB, p, d.SpaceID, PermWrite); err != nil {
		return nil, err
	}
	var id string
	err = s.tx(ctx, func(tx pgx.Tx) error {
		if _, err := lockFolder(ctx, tx, folderID); err != nil {
			return err
		}
		f, l, err := child(ctx, tx, folderID, name)
		if err != nil {
			return err
		}
		if f != "" || l != "" {
			return errf(ErrConflict, "%q already exists", name)
		}
		id, err = createLinkTx(ctx, tx, p, docID, folderID, name, nil, JoinPath(dst.Path, name))
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.GetFile(ctx, p, id)
}

// DeleteLink deletes a file (link). The document goes to the trash when it
// has no other live links.
func (s *Service) DeleteLink(ctx context.Context, p *Principal, linkID string) error {
	f, err := s.GetFile(ctx, p, linkID)
	if err != nil {
		return err
	}
	if err := s.requirePerm(ctx, s.DB, p, f.SpaceID, PermDelete); err != nil {
		return err
	}
	return s.tx(ctx, func(tx pgx.Tx) error {
		if _, err := lockFolder(ctx, tx, f.FolderID); err != nil {
			return err
		}
		return softDeleteLinkTx(ctx, tx, linkID)
	})
}

// RestoreOriginal moves links back to the path they were imported at
// (recreating folders; conflicting names get a suffix).
func (s *Service) RestoreOriginal(ctx context.Context, p *Principal, linkIDs []string) ([]FileInfo, error) {
	out := []FileInfo{}
	for _, id := range linkIDs {
		f, err := s.GetFile(ctx, p, id)
		if err != nil {
			return out, err
		}
		if err := s.requirePerm(ctx, s.DB, p, f.SpaceID, PermWrite); err != nil {
			return out, err
		}
		if f.Path == f.OriginalPath {
			out = append(out, *f)
			continue
		}
		segs, err := SplitPath(f.OriginalPath)
		if err != nil || len(segs) == 0 {
			continue
		}
		err = s.tx(ctx, func(tx pgx.Tx) error {
			dir, err := s.mkdirAllTx(ctx, tx, p, f.SpaceID, segs[:len(segs)-1])
			if err != nil {
				return err
			}
			if err := lockFolders(ctx, tx, f.FolderID, dir); err != nil {
				return err
			}
			name, err := uniqueName(ctx, tx, dir, segs[len(segs)-1], "restored")
			if err != nil {
				return err
			}
			_, err = tx.Exec(ctx, `UPDATE entries SET parent_id=$2, name=$3, updated_at=now() WHERE id=$1`, id, dir, name)
			return err
		})
		if err != nil {
			return out, err
		}
		nf, err := s.GetFile(ctx, p, id)
		if err != nil {
			return out, err
		}
		out = append(out, *nf)
	}
	return out, nil
}

// ImportLinks returns the live link ids of an import batch owned by the caller.
func (s *Service) ImportLinks(ctx context.Context, p *Principal, importID string) ([]string, error) {
	rows, err := s.DB.Query(ctx, `SELECT l.id::text FROM entries l JOIN imports i ON i.id=l.import_id
		WHERE i.id=$1 AND i.user_id=$2 AND l.deleted_at IS NULL`, importID, p.UserID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
