package core

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

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
	ID          string
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
		RETURNING id::text, kind, payload, attempts, max_attempts`, workerID).Scan(&j.ID, &j.Kind, &j.Payload, &j.Attempts, &j.MaxAttempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return j, err
}

// CompleteJob marks a job done.
func (s *Service) CompleteJob(ctx context.Context, id string) error {
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

// IsTildocOCR reports whether a blob is one of tildoc's own OCR results
// (a searchable_pdf variant), e.g. when it is uploaded as a new version.
func (s *Service) IsTildocOCR(ctx context.Context, sha blob.SHA256) (bool, error) {
	var ok bool
	err := s.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM blob_derivatives WHERE derived_sha256=$1 AND kind='searchable_pdf')`, sha[:]).Scan(&ok)
	return ok, err
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
	// Positions enable phrase search and better ranking; very large texts
	// exceed PostgreSQL's tsvector size limit (54000) and are stored stripped.
	for _, vec := range []string{`to_tsvector('simple', $3)`, `strip(to_tsvector('simple', $3))`} {
		_, err := s.DB.Exec(ctx, `INSERT INTO blob_texts(sha256, method, content, tsv)
			VALUES($1, $2, $3, `+vec+`)
			ON CONFLICT (sha256) DO UPDATE SET method=EXCLUDED.method, content=EXCLUDED.content, tsv=EXCLUDED.tsv, created_at=now()`,
			sha[:], method, text)
		var pe *pgconn.PgError
		if errors.As(err, &pe) && pe.Code == "54000" {
			continue
		}
		return err
	}
	return errors.New("text could not be indexed")
}

// Derivation describes how a derived variant was produced.
type Derivation struct {
	Kind   string         // searchable_pdf | image_only_pdf
	Input  blob.SHA256    // the blob the tool ran on (source or another variant)
	Engine string         // e.g. "ocrmypdf 16.10 / tesseract 5.5"
	Meta   map[string]any // e.g. {"langs": "deu+eng"}
}

// StoreDerivative stores a derived variant of a source blob.
func (s *Service) StoreDerivative(ctx context.Context, source blob.SHA256, d Derivation, st *blob.Staged) error {
	if d.Meta == nil {
		d.Meta = map[string]any{}
	}
	c := s.claim(st)
	defer c.discardUnused()
	return s.tx(ctx, func(tx pgx.Tx) error {
		if err := c.commit(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO blobs(sha256, md5, size, mime) VALUES($1,$2,$3,$4)
			ON CONFLICT (sha256) DO NOTHING`, st.SHA256[:], st.MD5[:], st.Size, DetectMIME(st.Head)); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO blob_derivatives(source_sha256, kind, derived_sha256, input_sha256, engine, meta)
			VALUES($1,$2,$3,$4,$5,$6)
			ON CONFLICT (source_sha256, kind) DO UPDATE SET derived_sha256=EXCLUDED.derived_sha256, input_sha256=EXCLUDED.input_sha256,
				engine=EXCLUDED.engine, meta=EXCLUDED.meta, created_at=now()`,
			source[:], d.Kind, st.SHA256[:], d.Input[:], d.Engine, d.Meta)
		return err
	})
}
