package core

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/tildoc/tildoc/internal/blob"
)

// TrashItem is a deleted document.
type TrashItem struct {
	DocumentID string    `json:"document_id"`
	SpaceID    string    `json:"space_id"`
	Name       string    `json:"name"`
	Path       string    `json:"path"`
	Size       int64     `json:"size"`
	MIME       string    `json:"mime"`
	DeletedAt  time.Time `json:"deleted_at"`
	PurgeAt    time.Time `json:"purge_at"`
}

// ListTrash lists trashed documents in spaces the caller can read
// (optionally restricted to one space).
func (s *Service) ListTrash(ctx context.Context, p *Principal, spaceID string) ([]TrashItem, error) {
	spaces, err := s.ListSpaces(ctx, p)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, sp := range spaces {
		if spaceID == "" || sp.ID == spaceID {
			ids = append(ids, sp.ID)
		}
	}
	rows, err := s.DB.Query(ctx, `
		SELECT d.id::text, d.space_id::text, coalesce(l.name,''), coalesce(l.deleted_path,''), b.size, b.mime, d.deleted_at
		FROM document_heads d JOIN blobs b ON b.sha256=d.sha256
		LEFT JOIN LATERAL (SELECT name, deleted_path FROM entries WHERE document_id=d.id ORDER BY deleted_at DESC NULLS LAST LIMIT 1) l ON true
		WHERE d.deleted_at IS NOT NULL AND d.space_id = ANY($1::uuid[])
		ORDER BY d.deleted_at DESC LIMIT 5000`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TrashItem{}
	for rows.Next() {
		var t TrashItem
		if err := rows.Scan(&t.DocumentID, &t.SpaceID, &t.Name, &t.Path, &t.Size, &t.MIME, &t.DeletedAt); err != nil {
			return nil, err
		}
		t.MIME = displayMIME(t.Name, t.MIME)
		t.PurgeAt = t.DeletedAt.Add(s.Cfg.TrashRetention)
		out = append(out, t)
	}
	return out, rows.Err()
}

// RestoreDocument restores a trashed document to the location(s) it was
// deleted from. Missing folders are recreated, name clashes get a suffix.
func (s *Service) RestoreDocument(ctx context.Context, p *Principal, docID string) (*Document, error) {
	d, err := s.GetDocument(ctx, p, docID)
	if err != nil {
		return nil, err
	}
	if d.DeletedAt == nil {
		return nil, errf(ErrInvalid, "document is not in trash")
	}
	if err := s.requirePerm(ctx, s.DB, p, d.SpaceID, PermWrite); err != nil {
		return nil, err
	}
	err = s.tx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id::text, coalesce(deleted_path,''), name FROM entries
			WHERE document_id=$1 AND deleted_at = (SELECT max(deleted_at) FROM entries WHERE document_id=$1)`, docID)
		if err != nil {
			return err
		}
		type rl struct{ id, path, name string }
		var links []rl
		for rows.Next() {
			var l rl
			if err := rows.Scan(&l.id, &l.path, &l.name); err != nil {
				rows.Close()
				return err
			}
			links = append(links, l)
		}
		rows.Close()
		if err := rows.Err(); err != nil { // restoring only some places must not go unnoticed
			return err
		}
		if len(links) == 0 {
			return errf(ErrInvalid, "no restorable location")
		}
		for _, l := range links {
			segs, err := SplitPath(l.path)
			if err != nil || len(segs) == 0 {
				segs = []string{l.name}
			}
			dir, err := s.mkdirAllTx(ctx, tx, p, d.SpaceID, segs[:len(segs)-1])
			if err != nil {
				return err
			}
			if _, err := lockFolder(ctx, tx, dir); err != nil {
				return err
			}
			name, err := uniqueName(ctx, tx, dir, segs[len(segs)-1], "restored")
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE entries SET parent_id=$2, name=$3, deleted_at=NULL, deleted_path=NULL, updated_at=now()
				WHERE id=$1`, l.id, dir, name); err != nil {
				return err
			}
		}
		_, err = tx.Exec(ctx, `UPDATE documents SET deleted_at=NULL WHERE id=$1`, docID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.GetDocument(ctx, p, docID)
}

// PurgeDocument permanently deletes a trashed document.
func (s *Service) PurgeDocument(ctx context.Context, p *Principal, docID string) error {
	d, err := s.GetDocument(ctx, p, docID)
	if err != nil {
		return err
	}
	if d.DeletedAt == nil {
		return errf(ErrInvalid, "document is not in trash")
	}
	if err := s.requireCap(ctx, s.DB, p, d.SpaceID, CapPurge); err != nil {
		return err
	}
	_, err = s.purgeDocuments(ctx, `id=$1`, docID)
	return err
}

// purgeDocuments deletes trashed documents matching cond and then their
// content (blob, text, processing state, OCR variants), unless the content
// is still used by another document.
func (s *Service) purgeDocuments(ctx context.Context, cond string, args ...any) (int, error) {
	var ids []string
	var shas [][]byte
	err := s.tx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id::text FROM documents WHERE deleted_at IS NOT NULL AND `+cond+` FOR UPDATE`, args...)
		if err != nil {
			return err
		}
		if ids, err = pgx.CollectRows(rows, pgx.RowTo[string]); err != nil || len(ids) == 0 {
			return err
		}
		rows, err = tx.Query(ctx, `SELECT sha256 FROM document_versions WHERE document_id = ANY($1::uuid[])
			UNION SELECT working_sha256 FROM office_sessions WHERE document_id = ANY($1::uuid[]) AND working_sha256 IS NOT NULL`, ids)
		if err != nil {
			return err
		}
		if shas, err = pgx.CollectRows(rows, pgx.RowTo[[]byte]); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM documents WHERE id = ANY($1::uuid[])`, ids); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO purge_log(document_id) SELECT unnest($1::uuid[])`, ids)
		return err
	})
	if err != nil {
		return 0, err
	}
	entries := make([]MPurge, 0, len(ids))
	for _, id := range ids {
		entries = append(entries, MPurge{DocumentID: id, PurgedAt: s.now()})
	}
	s.appendPurgeFile(entries)
	s.purgeContent(ctx, shas)
	return len(ids), nil
}

// PurgeVersion deletes one version for good, e.g. one holding a leaked
// secret. The current version can't be purged: restore another one first, or
// delete the document.
func (s *Service) PurgeVersion(ctx context.Context, p *Principal, docID string, version int) error {
	d, err := s.GetDocument(ctx, p, docID)
	if err != nil {
		return err
	}
	if err := s.requireCap(ctx, s.DB, p, d.SpaceID, CapPurge); err != nil {
		return err
	}
	var sha []byte
	err = s.tx(ctx, func(tx pgx.Tx) error {
		var cur int
		if err := tx.QueryRow(ctx, `SELECT current_version FROM documents WHERE id=$1 FOR UPDATE`, docID).Scan(&cur); err != nil {
			return err
		}
		if version == cur {
			return errf(ErrInvalid, "the current version can't be deleted: restore another version first, or delete the document")
		}
		err := tx.QueryRow(ctx, `DELETE FROM document_versions WHERE document_id=$1 AND version=$2 RETURNING sha256`, docID, version).Scan(&sha)
		if errors.Is(err, pgx.ErrNoRows) {
			return errf(ErrNotFound, "version not found")
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO purge_log(document_id, version) VALUES($1,$2)`, docID, version); err != nil {
			return err
		}
		return s.audit(ctx, tx, p, "version.purge", "document", docID, d.SpaceID, map[string]any{"version": version})
	})
	if err != nil {
		return err
	}
	s.appendPurgeFile([]MPurge{{DocumentID: docID, Version: &version, PurgedAt: s.now()}})
	s.purgeContent(ctx, [][]byte{sha})
	return nil
}

// purgeLogFile sits next to the backups. It outlives the database, so that a
// restore of an older backup still knows what was deleted afterwards.
const purgeLogFile = "purge.log"

func (s *Service) appendPurgeFile(entries []MPurge) {
	if len(entries) == 0 || s.Cfg.BackupDir == "" {
		return
	}
	if err := os.MkdirAll(s.Cfg.BackupDir, 0o700); err != nil {
		s.Log.Error("purge log", "err", err)
		return
	}
	f, err := os.OpenFile(filepath.Join(s.Cfg.BackupDir, purgeLogFile), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		s.Log.Error("purge log", "err", err)
		return
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, e := range entries {
		if err := enc.Encode(e); err != nil {
			s.Log.Error("purge log", "err", err)
			return
		}
	}
}

func (s *Service) readPurgeFile(dir string) []MPurge {
	f, err := os.Open(filepath.Join(dir, purgeLogFile))
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []MPurge
	dec := json.NewDecoder(f)
	for {
		var e MPurge
		if err := dec.Decode(&e); err != nil {
			if !errors.Is(err, io.EOF) {
				s.Log.Error("purge log unreadable, stopped reading it", "err", err)
			}
			return out
		}
		out = append(out, e)
	}
}

// applyPurgesTx removes from a restored instance what was deleted for good.
func applyPurgesTx(ctx context.Context, tx pgx.Tx, entries []MPurge) error {
	for _, e := range entries {
		if e.Version == nil {
			if _, err := tx.Exec(ctx, `DELETE FROM documents WHERE id=$1`, e.DocumentID); err != nil {
				return err
			}
		} else {
			var cur int
			err := tx.QueryRow(ctx, `SELECT current_version FROM documents WHERE id=$1`, e.DocumentID).Scan(&cur)
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			if err != nil {
				return err
			}
			if cur == *e.Version { // an older backup, where it still was the current one
				var newest *int
				if err := tx.QueryRow(ctx, `SELECT max(version) FROM document_versions WHERE document_id=$1 AND version<>$2`, e.DocumentID, cur).Scan(&newest); err != nil {
					return err
				}
				if newest == nil {
					if _, err := tx.Exec(ctx, `DELETE FROM documents WHERE id=$1`, e.DocumentID); err != nil {
						return err
					}
					continue
				}
				if _, err := tx.Exec(ctx, `UPDATE documents SET current_version=$2 WHERE id=$1`, e.DocumentID, *newest); err != nil {
					return err
				}
			}
			if _, err := tx.Exec(ctx, `DELETE FROM document_versions WHERE document_id=$1 AND version=$2`, e.DocumentID, *e.Version); err != nil {
				return err
			}
		}
	}
	return nil
}

// EmptyTrash purges all trashed documents of a space.
func (s *Service) EmptyTrash(ctx context.Context, p *Principal, spaceID string) (int, error) {
	if err := s.requireCap(ctx, s.DB, p, spaceID, CapPurge); err != nil {
		return 0, err
	}
	return s.purgeDocuments(ctx, `space_id=$1`, spaceID)
}

// PurgeExpired deletes documents whose trash retention has passed.
func (s *Service) PurgeExpired(ctx context.Context) (int, error) {
	return s.purgeDocuments(ctx, `deleted_at < now() - make_interval(secs => $1)`, s.Cfg.TrashRetention.Seconds())
}

// GCBlobs deletes blobs no longer referenced by any version (and not part
// of the variant tree of a referenced blob), with their processing state,
// text and variants. only limits it to these blobs and the variants derived
// from them (nil: all). Writers record blobs under the shared blob lock and
// GC takes it exclusively, so there is no grace period: unreferenced content
// is gone at once. ok=false: an export or import holds the lock, try later.
func (s *Service) GCBlobs(ctx context.Context, only [][]byte) (n int, ok bool, err error) {
	conn, ok, err := s.tryBlobGC(ctx)
	if err != nil || !ok {
		return 0, false, err
	}
	defer func() {
		conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, int64(blobLockKey)) //nolint:errcheck // the lock ends with the connection anyway
		conn.Release()
	}()
	all := only == nil
	for round := 0; round < 5 && (all || len(only) > 0); round++ {
		var next [][]byte
		if !all {
			// variants of the candidates become candidates once their source is gone
			rows, err := s.DB.Query(ctx, `SELECT derived_sha256 FROM blob_derivatives WHERE source_sha256 = ANY($1)
				UNION SELECT input_sha256 FROM blob_derivatives WHERE source_sha256 = ANY($1)`, only)
			if err != nil {
				return n, true, err
			}
			for rows.Next() {
				var b []byte
				if err := rows.Scan(&b); err != nil {
					rows.Close()
					return n, true, err
				}
				next = append(next, b)
			}
			rows.Close()
		}
		rows, err := s.DB.Query(ctx, `
			DELETE FROM blobs b
			WHERE ($1::bytea[] IS NULL OR b.sha256 = ANY($1))
			  AND NOT EXISTS (SELECT 1 FROM document_versions v WHERE v.sha256 = b.sha256)
			  AND NOT EXISTS (SELECT 1 FROM office_sessions o WHERE o.working_sha256 = b.sha256)
			  AND NOT EXISTS (SELECT 1 FROM blob_derivatives x WHERE x.source_sha256 <> b.sha256
			                  AND (x.derived_sha256 = b.sha256 OR x.input_sha256 = b.sha256))
			RETURNING b.sha256`, only)
		if err != nil {
			return n, true, err
		}
		var shas []blob.SHA256
		for rows.Next() {
			var raw []byte
			if err := rows.Scan(&raw); err != nil {
				rows.Close()
				return n, true, err
			}
			var h blob.SHA256
			copy(h[:], raw)
			shas = append(shas, h)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return n, true, err
		}
		for _, h := range shas {
			if err := s.Blobs.Delete(h); err != nil {
				s.Log.Warn("gc: delete blob file", "sha256", h.Hex(), "err", err)
			}
		}
		n += len(shas)
		if len(shas) == 0 && all {
			break
		}
		if !all {
			only = next
		}
	}
	return n, true, nil
}

// purgeContent deletes the content of purged documents right away (unless
// still used elsewhere); if an export holds the blobs, a GC job does it later.
func (s *Service) purgeContent(ctx context.Context, shas [][]byte) {
	if len(shas) == 0 {
		return
	}
	if _, ok, err := s.GCBlobs(ctx, shas); err != nil || !ok {
		if err != nil {
			s.Log.Warn("purge content", "err", err)
		}
		if err := Enqueue(ctx, s.DB, JobGCBlobs, map[string]any{}, JobGCBlobs, time.Now().Add(time.Minute)); err != nil {
			s.Log.Error("purge content: could not queue the cleanup", "err", err) // content stays until the next full GC
		}
	}
}

// SweepOrphanFiles removes blob files that have no database row and were not
// touched within grace (left over from crashed ingests).
func (s *Service) SweepOrphanFiles(ctx context.Context, grace time.Duration) (int, error) {
	n := 0
	cutoff := s.now().Add(-grace)
	err := s.Blobs.Walk(func(sha blob.SHA256, mod time.Time) error {
		if mod.After(cutoff) {
			return nil
		}
		var exists bool
		if err := s.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM blobs WHERE sha256=$1)`, sha[:]).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			if err := s.Blobs.Delete(sha); err == nil {
				n++
			}
		}
		return nil
	})
	return n, err
}
