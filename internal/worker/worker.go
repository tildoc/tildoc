// Package worker runs background jobs: content processing (text
// extraction, OCR) and maintenance (trash purge, blob GC, upload cleanup).
package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/tildoc/tildoc/internal/blob"
	"github.com/tildoc/tildoc/internal/core"
)

// Worker processes the job queue.
type Worker struct {
	Svc  *core.Service
	Log  *slog.Logger
	ID   string
	Proc *Processor
	// PollInterval is the fallback polling interval (NOTIFY wakes earlier).
	PollInterval time.Duration
	// MaintenanceInterval controls periodic maintenance jobs.
	MaintenanceInterval time.Duration
}

// New creates a worker.
func New(svc *core.Service, log *slog.Logger) *Worker {
	host, _ := os.Hostname()
	return &Worker{
		Svc: svc, Log: log, ID: host + "-" + strconv.Itoa(os.Getpid()),
		Proc:                NewProcessor(svc, log),
		PollInterval:        5 * time.Second,
		MaintenanceInterval: 15 * time.Minute,
	}
}

// Run processes jobs until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) error {
	w.Log.Info("worker started", "id", w.ID, "concurrency", w.Svc.Cfg.WorkerConcurrency,
		"pdftotext", w.Proc.HavePdftotext(), "ocr", w.Proc.HaveOCR())
	wake := make(chan struct{}, 1)
	signal := func() {
		select {
		case wake <- struct{}{}:
		default:
		}
	}
	go w.listen(ctx, signal)
	go w.maintenance(ctx)

	var wg sync.WaitGroup
	for i := 0; i < w.Svc.Cfg.WorkerConcurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			t := time.NewTicker(w.PollInterval)
			defer t.Stop()
			for {
				for ctx.Err() == nil && w.RunOne(ctx) {
				}
				select {
				case <-ctx.Done():
					return
				case <-wake:
					signal() // let other loops check too
				case <-t.C:
				}
			}
		}()
	}
	wg.Wait()
	return nil
}

// listen wakes workers on new jobs via LISTEN/NOTIFY.
func (w *Worker) listen(ctx context.Context, signal func()) {
	for ctx.Err() == nil {
		conn, err := w.Svc.DB.Acquire(ctx)
		if err != nil {
			time.Sleep(time.Second)
			continue
		}
		if _, err := conn.Exec(ctx, "LISTEN tildoc_jobs"); err != nil {
			conn.Release()
			time.Sleep(time.Second)
			continue
		}
		for {
			if _, err := conn.Conn().WaitForNotification(ctx); err != nil {
				break
			}
			signal()
		}
		conn.Conn().Close(context.Background())
		conn.Release()
	}
}

func (w *Worker) maintenance(ctx context.Context) {
	tick := func() {
		core.Enqueue(ctx, w.Svc.DB, core.JobPurgeTrash, map[string]any{}, core.JobPurgeTrash, time.Time{})
		if n, err := w.Svc.ReclaimStaleJobs(ctx, w.Svc.Cfg.ProcessTimeout+5*time.Minute); err == nil && n > 0 {
			w.Log.Warn("reclaimed stale jobs", "count", n)
		}
		w.Svc.PruneJobs(ctx, 7*24*time.Hour)
		if n, err := w.Svc.CleanupUploads(ctx, w.Svc.Cfg.MultipartMaxAge); err == nil && n > 0 {
			w.Log.Info("aborted stale multipart uploads", "count", n)
		}
	}
	tick()
	t := time.NewTicker(w.MaintenanceInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			tick()
		}
	}
}

// RunOne claims and executes one job. Returns false if no job was due.
func (w *Worker) RunOne(ctx context.Context) bool {
	j, err := w.Svc.ClaimJob(ctx, w.ID)
	if err != nil {
		if ctx.Err() == nil {
			w.Log.Error("claim job", "err", err)
		}
		return false
	}
	if j == nil {
		return false
	}
	start := time.Now()
	jctx, cancel := context.WithTimeout(ctx, w.Svc.Cfg.ProcessTimeout)
	err = w.execute(jctx, j)
	cancel()
	if err != nil {
		w.Log.Warn("job failed", "id", j.ID, "kind", j.Kind, "attempt", j.Attempts, "err", err)
		if ferr := w.Svc.FailJob(context.Background(), j, err); ferr != nil {
			w.Log.Error("fail job", "err", ferr)
		}
		return true
	}
	w.Log.Debug("job done", "id", j.ID, "kind", j.Kind, "took", time.Since(start))
	if err := w.Svc.CompleteJob(context.Background(), j.ID); err != nil {
		w.Log.Error("complete job", "err", err)
	}
	return true
}

func (w *Worker) execute(ctx context.Context, j *core.Job) error {
	switch j.Kind {
	case core.JobProcessBlob:
		var p core.ProcessPayload
		if err := json.Unmarshal(j.Payload, &p); err != nil {
			return err
		}
		sha, err := blob.ParseSHA256(p.SHA256)
		if err != nil {
			return err
		}
		return w.Proc.Process(ctx, sha, p.Force)
	case core.JobPurgeTrash:
		n, err := w.Svc.PurgeExpired(ctx)
		if err != nil {
			return err
		}
		if n > 0 {
			w.Log.Info("purged expired trash", "documents", n)
		}
		return core.Enqueue(ctx, w.Svc.DB, core.JobGCBlobs, map[string]any{}, core.JobGCBlobs, time.Time{})
	case core.JobGCBlobs:
		n, ok, err := w.Svc.GCBlobs(ctx, nil)
		if err != nil {
			return err
		}
		if !ok { // an export or import holds the blobs: try again later
			return core.Enqueue(ctx, w.Svc.DB, core.JobGCBlobs, map[string]any{}, core.JobGCBlobs, time.Now().Add(5*time.Minute))
		}
		o, err := w.Svc.SweepOrphanFiles(ctx, 24*time.Hour)
		if n+o > 0 {
			w.Log.Info("blob gc", "deleted", n, "orphans", o)
		}
		return err
	case core.JobTransfer, core.JobBackup:
		var p struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(j.Payload, &p); err != nil {
			return err
		}
		if j.Kind == core.JobTransfer {
			return w.Svc.RunTransfer(ctx, p.ID)
		}
		return w.Svc.RunBackup(ctx, p.ID)
	}
	return fmt.Errorf("unknown job kind %q", j.Kind)
}
