package core

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

type linkRow struct {
	ID, DocID, FolderID, Name, SpaceID string
	CreatedAt                          time.Time
	CreatedBy                          *string
}

func loadLinkTx(ctx context.Context, q pgx.Tx, linkID string) (*linkRow, error) {
	l := &linkRow{ID: linkID}
	err := q.QueryRow(ctx, `SELECT l.document_id::text, l.folder_id::text, l.name, d.space_id::text, l.created_at, l.created_by::text
		FROM links l JOIN documents d ON d.id = l.document_id WHERE l.id=$1 AND l.deleted_at IS NULL FOR UPDATE OF l`, linkID).
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
	err := tx.QueryRow(ctx, `UPDATE links SET deleted_at=now(), deleted_path=folder_path(folder_id) || '/' || name
		WHERE id=$1 AND deleted_at IS NULL RETURNING document_id::text`, linkID).Scan(&docID)
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
	err := tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM links WHERE document_id=d.id AND deleted_at IS NULL AND id<>$2),
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
	if err := tx.QueryRow(ctx, `SELECT space_id::text FROM folders WHERE id=$1`, dstFolderID).Scan(&dstSpace); err != nil {
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
	if other != "" {
		if !overwrite {
			return "", errf(ErrConflict, "%q already exists", newName)
		}
		var otherDoc string
		if err := tx.QueryRow(ctx, `SELECT document_id::text FROM links WHERE id=$1`, other).Scan(&otherDoc); err != nil {
			return "", err
		}
		if otherDoc != l.DocID {
			var sha []byte
			var meta map[string]string
			if err := tx.QueryRow(ctx, `SELECT d.current_sha256, v.meta FROM documents d JOIN document_versions v
				ON v.document_id=d.id AND v.version=d.current_version WHERE d.id=$1`, l.DocID).Scan(&sha, &meta); err != nil {
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
		_, err = tx.Exec(ctx, `UPDATE links SET folder_id=$2, name=$3, updated_at=now(),
			original_path = CASE WHEN created_at > now() - make_interval(secs => $4) AND created_by IS NOT DISTINCT FROM $5::uuid
				THEN folder_path($2) || '/' || $3 ELSE original_path END
			WHERE id=$1`, linkID, dstFolderID, newName, s.Cfg.ProvenanceWindow.Seconds(), p.userRef())
		return linkID, err
	}
	// cross-space move
	var live int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM links WHERE document_id=$1 AND deleted_at IS NULL`, l.DocID).Scan(&live); err != nil {
		return "", err
	}
	docID := l.DocID
	if live == 1 {
		if _, err := tx.Exec(ctx, `UPDATE documents SET space_id=$2 WHERE id=$1`, docID, dstSpace); err != nil {
			return "", err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM links WHERE document_id=$1 AND deleted_at IS NOT NULL`, docID); err != nil {
			return "", err
		}
	} else {
		if docID, err = forkDocTx(ctx, tx, p, l.DocID, dstSpace); err != nil {
			return "", err
		}
	}
	_, err = tx.Exec(ctx, `UPDATE links SET document_id=$2, folder_id=$3, name=$4, updated_at=now() WHERE id=$1`,
		linkID, docID, dstFolderID, newName)
	return linkID, err
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
	need := PermWrite
	if dst.SpaceID != f.SpaceID {
		need = PermDelete
	}
	if err := s.requirePerm(ctx, s.DB, p, f.SpaceID, need); err != nil {
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
	err := tx.QueryRow(ctx, `SELECT l.document_id::text, d.space_id::text, l.name, d.current_sha256 FROM links l
		JOIN documents d ON d.id=l.document_id WHERE l.id=$1 AND l.deleted_at IS NULL`, linkID).Scan(&docID, &srcSpace, &name, &sha)
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
	if _, err := s.GetFile(ctx, p, linkID); err != nil {
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
			rows.Scan(&it.id, &it.name)
			out = append(out, it)
		}
		return out, rows.Err()
	}
	links, err := collect(`SELECT id::text, name FROM links WHERE folder_id=$1 AND deleted_at IS NULL`)
	if err != nil {
		return "", err
	}
	for _, l := range links {
		if _, err := s.copyLinkTx(ctx, tx, p, l.id, newID, "", false); err != nil {
			return "", err
		}
	}
	subs, err := collect(`SELECT id::text, name FROM folders WHERE parent_id=$1`)
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
			_, err = tx.Exec(ctx, `UPDATE links SET folder_id=$2, name=$3, updated_at=now() WHERE id=$1`, id, dir, name)
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
	rows, err := s.DB.Query(ctx, `SELECT l.id::text FROM links l JOIN imports i ON i.id=l.import_id
		WHERE i.id=$1 AND i.user_id=$2 AND l.deleted_at IS NULL`, importID, p.UserID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		rows.Scan(&id)
		out = append(out, id)
	}
	return out, rows.Err()
}
