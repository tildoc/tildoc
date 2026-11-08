package core

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5"
)

// Upload is a pending S3 multipart upload.
type Upload struct {
	ID        string            `json:"id"`
	UserID    string            `json:"user_id"`
	SpaceID   string            `json:"space_id"`
	Key       string            `json:"key"`
	Meta      map[string]string `json:"meta"`
	CreatedAt time.Time         `json:"created_at"`
}

// Part is an uploaded part.
type Part struct {
	Number    int
	Size      int64
	MD5       string
	Path      string
	CreatedAt time.Time
}

// CreateUpload starts a multipart upload.
func (s *Service) CreateUpload(ctx context.Context, p *Principal, spaceID, key string, meta map[string]string) (*Upload, error) {
	if err := s.requirePerm(ctx, s.DB, p, spaceID, PermWrite); err != nil {
		return nil, err
	}
	b := make([]byte, 16)
	rand.Read(b)
	u := &Upload{ID: hex.EncodeToString(b), UserID: p.UserID, SpaceID: spaceID, Key: key, Meta: meta}
	if u.Meta == nil {
		u.Meta = map[string]string{}
	}
	err := s.DB.QueryRow(ctx, `INSERT INTO multipart_uploads(id, user_id, space_id, key, meta) VALUES($1,$2,$3,$4,$5) RETURNING created_at`,
		u.ID, p.UserID, spaceID, key, u.Meta).Scan(&u.CreatedAt)
	return u, err
}

// GetUpload returns an upload owned by the caller.
func (s *Service) GetUpload(ctx context.Context, p *Principal, id string) (*Upload, error) {
	u := &Upload{ID: id}
	err := s.DB.QueryRow(ctx, `SELECT user_id::text, space_id::text, key, meta, created_at FROM multipart_uploads WHERE id=$1 AND user_id=$2`,
		id, p.UserID).Scan(&u.UserID, &u.SpaceID, &u.Key, &u.Meta, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errf(ErrNotFound, "upload not found")
	}
	if err != nil {
		return nil, err
	}
	if err := s.requirePerm(ctx, s.DB, p, u.SpaceID, PermWrite); err != nil {
		return nil, err
	}
	return u, nil
}

// ListUploads lists the caller's uploads in a space.
func (s *Service) ListUploads(ctx context.Context, p *Principal, spaceID string) ([]Upload, error) {
	rows, err := s.DB.Query(ctx, `SELECT id, user_id::text, space_id::text, key, meta, created_at FROM multipart_uploads
		WHERE space_id=$1 AND user_id=$2 ORDER BY key, created_at`, spaceID, p.UserID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Upload
	for rows.Next() {
		var u Upload
		if err := rows.Scan(&u.ID, &u.UserID, &u.SpaceID, &u.Key, &u.Meta, &u.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// PartPath returns the staging file path for a part.
func (s *Service) PartPath(uploadID string, n int) string {
	return filepath.Join(s.Blobs.TempDir(), "mpu-"+uploadID+"-"+itoa(n))
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// RecordPart stores part metadata (the file is written by the caller).
func (s *Service) RecordPart(ctx context.Context, uploadID string, n int, size int64, md5 []byte, path string) error {
	_, err := s.DB.Exec(ctx, `INSERT INTO multipart_parts(upload_id, part_number, size, md5, path) VALUES($1,$2,$3,$4,$5)
		ON CONFLICT (upload_id, part_number) DO UPDATE SET size=EXCLUDED.size, md5=EXCLUDED.md5, path=EXCLUDED.path, created_at=now()`,
		uploadID, n, size, md5, path)
	return err
}

// ListParts returns all parts of an upload ordered by number.
func (s *Service) ListParts(ctx context.Context, uploadID string) ([]Part, error) {
	rows, err := s.DB.Query(ctx, `SELECT part_number, size, md5, path, created_at FROM multipart_parts WHERE upload_id=$1 ORDER BY part_number`, uploadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Part
	for rows.Next() {
		var pt Part
		var md5 []byte
		if err := rows.Scan(&pt.Number, &pt.Size, &md5, &pt.Path, &pt.CreatedAt); err != nil {
			return nil, err
		}
		pt.MD5 = hex.EncodeToString(md5)
		out = append(out, pt)
	}
	return out, rows.Err()
}

// DeleteUpload removes an upload and its part files.
func (s *Service) DeleteUpload(ctx context.Context, uploadID string) error {
	parts, _ := s.ListParts(ctx, uploadID)
	for _, pt := range parts {
		os.Remove(pt.Path)
	}
	_, err := s.DB.Exec(ctx, `DELETE FROM multipart_uploads WHERE id=$1`, uploadID)
	return err
}

// CleanupUploads aborts uploads older than maxAge and removes stale temp files.
func (s *Service) CleanupUploads(ctx context.Context, maxAge time.Duration) (int, error) {
	rows, err := s.DB.Query(ctx, `SELECT id FROM multipart_uploads WHERE created_at < now() - make_interval(secs => $1)`, maxAge.Seconds())
	if err != nil {
		return 0, err
	}
	var ids []string
	for rows.Next() {
		var id string
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		s.DeleteUpload(ctx, id)
	}
	// stale staging files (crashed uploads)
	entries, _ := os.ReadDir(s.Blobs.TempDir())
	cutoff := s.now().Add(-maxAge)
	for _, e := range entries {
		if info, err := e.Info(); err == nil && info.ModTime().Before(cutoff) {
			os.Remove(filepath.Join(s.Blobs.TempDir(), e.Name()))
		}
	}
	return len(ids), nil
}

// SetFileMeta replaces the user metadata of a file's current version
// (S3 CopyObject onto itself with REPLACE directive, e.g. mtime updates).
func (s *Service) SetFileMeta(ctx context.Context, p *Principal, linkID string, meta map[string]string) error {
	f, err := s.GetFile(ctx, p, linkID)
	if err != nil {
		return err
	}
	if err := s.requirePerm(ctx, s.DB, p, f.SpaceID, PermWrite); err != nil {
		return err
	}
	if meta == nil {
		meta = map[string]string{}
	}
	_, err = s.DB.Exec(ctx, `UPDATE document_versions SET meta=$3 WHERE document_id=$1 AND version=$2`, f.DocumentID, f.Version, meta)
	return err
}
