package core

import (
	"context"
	"encoding/json"
	"time"

	"github.com/tildoc/tildoc/internal/db"
)

// Job kinds.
const (
	JobProcessBlob = "process_blob"
	JobPurgeTrash  = "purge_trash"
	JobGCBlobs     = "gc_blobs"
)

// Enqueue inserts a job. With a dedup key, an identical queued/running job
// suppresses the insert.
func Enqueue(ctx context.Context, q db.Querier, kind string, payload any, dedupKey string, runAfter time.Time) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if runAfter.IsZero() {
		runAfter = time.Now()
	}
	var dk *string
	if dedupKey != "" {
		dk = &dedupKey
	}
	_, err = q.Exec(ctx, `INSERT INTO jobs(kind, payload, dedup_key, run_after) VALUES($1,$2,$3,$4)
		ON CONFLICT (dedup_key) WHERE status IN ('queued','running') DO NOTHING`, kind, b, dk, runAfter)
	return err
}

// ProcessPayload is the payload of JobProcessBlob.
type ProcessPayload struct {
	SHA256 string `json:"sha256"`
	Force  bool   `json:"force,omitempty"`
}

func enqueueProcess(ctx context.Context, q db.Querier, shaHex string, force bool) error {
	return Enqueue(ctx, q, JobProcessBlob, ProcessPayload{SHA256: shaHex, Force: force}, JobProcessBlob+":"+shaHex, time.Time{})
}
