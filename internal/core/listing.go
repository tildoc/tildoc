package core

import (
	"context"
	"encoding/hex"
	"time"
)

// KeyEntry is one entry of a recursive key listing (S3 semantics).
type KeyEntry struct {
	Key     string // relative to the start folder; folders end with "/"
	IsDir   bool   // empty folder marker
	Size    int64
	MD5     string
	ModTime time.Time
	LinkID  string
}

// ListKeys lists all files below startFolder recursively as slash separated
// keys in byte order (S3 ListObjects order). Empty folders are reported as
// "dir/" markers so clients can see them. Only keys > after that start with
// prefix are returned.
func (s *Service) ListKeys(ctx context.Context, p *Principal, spaceID, startFolder, prefix, after string, limit int) ([]KeyEntry, error) {
	if err := s.requirePerm(ctx, s.DB, p, spaceID, PermRead); err != nil {
		return nil, err
	}
	rows, err := s.DB.Query(ctx, `
		WITH RECURSIVE tree(id, path) AS (
			SELECT id, ''::text FROM entries WHERE id = $1 AND space_id = $5 AND kind = 'folder'
			UNION ALL
			SELECT f.id, t.path || f.name || '/' FROM entries f JOIN tree t ON f.parent_id = t.id AND f.kind = 'folder'
		), keys AS (
			SELECT t.path || l.name AS key, false AS is_dir, b.size, b.md5, d.updated_at AS mod, l.id::text AS link_id
			FROM tree t
			JOIN entries l ON l.parent_id = t.id AND l.kind = 'link' AND l.deleted_at IS NULL
			JOIN document_heads d ON d.id = l.document_id
			JOIN blobs b ON b.sha256 = d.sha256
			UNION ALL
			SELECT t.path, true, 0, NULL, f.created_at, ''
			FROM tree t JOIN entries f ON f.id = t.id
			WHERE t.path <> ''
			  AND NOT EXISTS (SELECT 1 FROM entries c WHERE c.parent_id = t.id AND c.deleted_at IS NULL)
		)
		SELECT key, is_dir, size, md5, mod, link_id FROM keys
		WHERE key COLLATE "C" > $2 COLLATE "C" AND starts_with(key, $3)
		ORDER BY key COLLATE "C" LIMIT $4`, startFolder, after, prefix, limit, spaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []KeyEntry
	for rows.Next() {
		var e KeyEntry
		var md5 []byte
		if err := rows.Scan(&e.Key, &e.IsDir, &e.Size, &md5, &e.ModTime, &e.LinkID); err != nil {
			return nil, err
		}
		e.MD5 = hex.EncodeToString(md5)
		out = append(out, e)
	}
	return out, rows.Err()
}
