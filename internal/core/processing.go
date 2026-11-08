package core

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/tildoc/tildoc/internal/blob"
)

// Processing statuses.
const (
	StatusPending     = "pending"
	StatusProcessing  = "processing"
	StatusDone        = "done"
	StatusOCRPending  = "ocr_pending"
	StatusUnsupported = "unsupported"
	StatusFailed      = "failed"
)

// MaxTextBytes caps stored extracted text per blob.
const MaxTextBytes = 20 << 20

// Job is a claimed queue entry.
type Job struct {
	ID          int64
	Kind        string
	Payload     json.RawMessage
	Attempts    int
	MaxAttempts int
}

// ClaimJob locks the next due job for worker id (nil if none).
func (s *Service) ClaimJob(ctx context.Context, workerID string) (*Job, error) {
	j := &Job{}
	err := s.DB.QueryRow(ctx, `
		UPDATE jobs SET status='running', locked_at=now(), locked_by=$1, attempts=attempts+1, updated_at=now()
		WHERE id = (SELECT id FROM jobs WHERE status='queued' AND run_after <= now()
		            ORDER BY run_after, id FOR UPDATE SKIP LOCKED LIMIT 1)
		RETURNING id, kind, payload, attempts, max_attempts`, workerID).Scan(&j.ID, &j.Kind, &j.Payload, &j.Attempts, &j.MaxAttempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return j, err
}

// CompleteJob marks a job done.
func (s *Service) CompleteJob(ctx context.Context, id int64) error {
	_, err := s.DB.Exec(ctx, `UPDATE jobs SET status='done', locked_at=NULL, updated_at=now(), last_error='' WHERE id=$1`, id)
	return err
}

// FailJob re-queues a job with exponential backoff or marks it failed.
func (s *Service) FailJob(ctx context.Context, j *Job, jobErr error) error {
	msg := jobErr.Error()
	if len(msg) > 2000 {
		msg = msg[:2000]
	}
	if j.Attempts >= j.MaxAttempts {
		_, err := s.DB.Exec(ctx, `UPDATE jobs SET status='failed', locked_at=NULL, last_error=$2, updated_at=now() WHERE id=$1`, j.ID, msg)
		return err
	}
	backoff := time.Duration(1<<uint(j.Attempts)) * 30 * time.Second
	_, err := s.DB.Exec(ctx, `UPDATE jobs SET status='queued', locked_at=NULL, last_error=$2, run_after=now() + make_interval(secs => $3), updated_at=now()
		WHERE id=$1`, j.ID, msg, backoff.Seconds())
	return err
}

// ReclaimStaleJobs re-queues jobs whose worker died.
func (s *Service) ReclaimStaleJobs(ctx context.Context, olderThan time.Duration) (int, error) {
	tag, err := s.DB.Exec(ctx, `UPDATE jobs SET status='queued', locked_at=NULL, updated_at=now()
		WHERE status='running' AND locked_at < now() - make_interval(secs => $1)`, olderThan.Seconds())
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// PruneJobs deletes finished jobs older than age.
func (s *Service) PruneJobs(ctx context.Context, age time.Duration) error {
	_, err := s.DB.Exec(ctx, `DELETE FROM jobs WHERE status='done' AND updated_at < now() - make_interval(secs => $1)`, age.Seconds())
	return err
}

// ProcessingState returns the processing status of a blob.
func (s *Service) ProcessingState(ctx context.Context, sha blob.SHA256) (string, error) {
	var st string
	err := s.DB.QueryRow(ctx, `SELECT status FROM blob_processing WHERE sha256=$1`, sha[:]).Scan(&st)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return st, err
}

// SetProcessing records the processing result of a blob.
func (s *Service) SetProcessing(ctx context.Context, sha blob.SHA256, status, kind string, pages *int, errMsg string) error {
	if len(errMsg) > 2000 {
		errMsg = errMsg[:2000]
	}
	_, err := s.DB.Exec(ctx, `INSERT INTO blob_processing(sha256, status, kind, pages, error) VALUES($1,$2,$3,$4,$5)
		ON CONFLICT (sha256) DO UPDATE SET status=EXCLUDED.status, kind=EXCLUDED.kind, pages=EXCLUDED.pages,
		error=EXCLUDED.error, updated_at=now()`, sha[:], status, kind, pages, errMsg)
	return err
}

// sanitizeText makes text storable in PostgreSQL (valid UTF-8, no NUL).
func sanitizeText(t string) string {
	if len(t) > MaxTextBytes {
		t = t[:MaxTextBytes]
	}
	if !utf8.ValidString(t) {
		t = strings.ToValidUTF8(t, "�")
	}
	return strings.ReplaceAll(t, "\x00", "")
}

// StoreText saves extracted text for a blob and indexes it.
func (s *Service) StoreText(ctx context.Context, sha blob.SHA256, method, text string) error {
	text = sanitizeText(text)
	_, err := s.DB.Exec(ctx, `INSERT INTO blob_texts(sha256, method, content, tsv)
		VALUES($1, $2, $3, strip(to_tsvector('simple', $3)))
		ON CONFLICT (sha256) DO UPDATE SET method=EXCLUDED.method, content=EXCLUDED.content, tsv=EXCLUDED.tsv, created_at=now()`,
		sha[:], method, text)
	return err
}

// StoreDerivative stores a derived file (e.g. searchable PDF) for a source blob.
func (s *Service) StoreDerivative(ctx context.Context, source blob.SHA256, kind string, st *blob.Staged) error {
	if _, err := s.Blobs.Commit(st); err != nil {
		s.Blobs.Discard(st)
		return err
	}
	return s.tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO blobs(sha256, md5, size, mime) VALUES($1,$2,$3,$4)
			ON CONFLICT (sha256) DO UPDATE SET last_used_at=now()`, st.SHA256[:], st.MD5[:], st.Size, DetectMIME(st.Head)); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO blob_derivatives(source_sha256, kind, derived_sha256) VALUES($1,$2,$3)
			ON CONFLICT (source_sha256, kind) DO UPDATE SET derived_sha256=EXCLUDED.derived_sha256, created_at=now()`,
			source[:], kind, st.SHA256[:])
		return err
	})
}
