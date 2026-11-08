package worker_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/tildoc/tildoc/internal/core"
	"github.com/tildoc/tildoc/internal/testutil"
	"github.com/tildoc/tildoc/internal/worker"
)

type fx struct {
	env *testutil.Env
	ctx context.Context
	p   *core.Principal
	sp  core.Space
	w   *worker.Worker
}

func newFx(t *testing.T) *fx {
	env := testutil.New(t)
	ctx := context.Background()
	env.Svc.SetupAdmin(ctx, "alice", "alicepass", "")
	p, _ := env.Svc.Authenticate(ctx, "alice", "alicepass")
	p.Via = "session"
	p.IsAdmin = true
	sps, _ := env.Svc.ListSpaces(ctx, p)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if os.Getenv("TILDOC_TEST_LOG") != "" {
		log = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}
	return &fx{env: env, ctx: ctx, p: p, sp: sps[0], w: worker.New(env.Svc, log)}
}

func (f *fx) put(t *testing.T, path string, data []byte) *core.FileInfo {
	t.Helper()
	segs, _ := core.SplitPath(path)
	res, err := f.env.Svc.PutFile(f.ctx, f.p, f.sp.ID, segs, bytes.NewReader(data), core.PutOptions{CreateParents: true})
	if err != nil {
		t.Fatal(err)
	}
	return res.File
}

// drain runs jobs until the queue is empty.
func (f *fx) drain(t *testing.T) {
	t.Helper()
	for i := 0; i < 100 && f.w.RunOne(f.ctx); i++ {
	}
}

func (f *fx) doc(t *testing.T, id string) *core.Document {
	t.Helper()
	d, err := f.env.Svc.GetDocument(f.ctx, f.p, id)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func (f *fx) search(t *testing.T, q string) int {
	t.Helper()
	r, err := f.env.Svc.Search(f.ctx, f.p, core.SearchQuery{Q: q})
	if err != nil {
		t.Fatal(err)
	}
	return r.Total
}

func TestTextAndUnsupported(t *testing.T) {
	f := newFx(t)
	txt := f.put(t, "/notes/meeting.md", []byte("# Protokoll\nBesprechung über Quartalszahlen und Zuständigkeiten\n"))
	bin := f.put(t, "/photo.raw", []byte{0x00, 0x01, 0x02, 0xff, 0xfe, 0x00, 0x10})
	f.drain(t)
	d := f.doc(t, txt.DocumentID)
	if d.Status != core.StatusDone || d.Kind != "text" || !d.HasText {
		t.Fatalf("text doc: %+v", d)
	}
	if n := f.search(t, "quartalszahlen"); n != 1 {
		t.Fatalf("search: %d", n)
	}
	if n := f.search(t, `"Besprechung über"`); n != 1 {
		t.Fatalf("phrase search: %d", n)
	}
	if d := f.doc(t, bin.DocumentID); d.Status != core.StatusUnsupported {
		t.Fatalf("binary: %s", d.Status)
	}
	// dedup: identical content elsewhere is searchable without new job
	dup := f.put(t, "/copy-of-meeting.md", []byte("# Protokoll\nBesprechung über Quartalszahlen und Zuständigkeiten\n"))
	if f.w.RunOne(f.ctx) {
		t.Fatal("deduplicated content must not be reprocessed")
	}
	if dup.Status != core.StatusDone {
		t.Fatalf("dedup status: %s", dup.Status)
	}
	// forced reprocess
	if err := f.env.Svc.Reprocess(f.ctx, f.p, txt.DocumentID); err != nil {
		t.Fatal(err)
	}
	if !f.w.RunOne(f.ctx) {
		t.Fatal("reprocess job missing")
	}
	text, method, err := f.env.Svc.DocumentText(f.ctx, f.p, txt.DocumentID)
	if err != nil || method != "text" || !strings.Contains(text, "Zuständigkeiten") {
		t.Fatalf("text: %q %q %v", text, method, err)
	}
}

func requireTool(t *testing.T, name string) {
	if _, err := exec.LookPath(name); err != nil {
		if os.Getenv("TILDOC_TEST_REQUIRE_TOOLS") != "" {
			t.Fatalf("%s required but not installed", name)
		}
		t.Skipf("%s not installed", name)
	}
}

func TestDigitalPDF(t *testing.T) {
	requireTool(t, "pdftotext")
	f := newFx(t)
	pdf := testutil.MakePDF(
		testutil.PDFPage{Text: "Rechnung Nr. 4711\nKunde: Hamburger Hafenbetriebe"},
		testutil.PDFPage{Text: "Seite zwei mit Zahlungsziel"},
	)
	fi := f.put(t, "/Rechnungen/r4711.pdf", pdf)
	f.drain(t)
	d := f.doc(t, fi.DocumentID)
	if d.Status != core.StatusDone || d.Kind != "pdf-digital" || d.Pages == nil || *d.Pages != 2 || d.HasSearchable {
		t.Fatalf("digital pdf: %+v", d)
	}
	if f.search(t, "Hafenbetriebe") != 1 || f.search(t, "zahlungsziel") != 1 {
		t.Fatal("pdf text not searchable")
	}
	r, err := f.env.Svc.Search(f.ctx, f.p, core.SearchQuery{Q: "Hafenbetriebe"})
	if err != nil || !strings.Contains(r.Hits[0].Snippet, "<<Hafenbetriebe>>") {
		t.Fatalf("snippet: %+v %v", r, err)
	}
	// corrupt PDF is recorded as failed, the job itself succeeds (no retry storm)
	bad := f.put(t, "/broken.pdf", []byte("%PDF-1.4\nthis is not a pdf at all"))
	f.drain(t)
	if d := f.doc(t, bad.DocumentID); d.Status != core.StatusFailed && d.Status != core.StatusOCRPending {
		t.Fatalf("broken pdf: %+v", d)
	}
}

func TestScannedPDF(t *testing.T) {
	requireTool(t, "pdftotext")
	f := newFx(t)
	pdf := testutil.MakePDF(
		testutil.PDFPage{Text: "Deckblatt digital"},
		testutil.PDFPage{Image: "TILDOC SCANNED INVOICE\nTOTAL AMOUNT 1234 EUR"},
	)
	fi := f.put(t, "/scan.pdf", pdf)
	f.drain(t)
	d := f.doc(t, fi.DocumentID)
	if d.Kind != "pdf-mixed" {
		t.Fatalf("kind: %+v", d)
	}
	if _, err := exec.LookPath("ocrmypdf"); err != nil || !f.env.Cfg.OCREnabled {
		if d.Status != core.StatusOCRPending {
			t.Fatalf("without OCR expected ocr_pending: %s", d.Status)
		}
		if f.search(t, "deckblatt") != 1 {
			t.Fatal("digital page text should be indexed while OCR is pending")
		}
		if os.Getenv("TILDOC_TEST_REQUIRE_TOOLS") != "" {
			t.Fatal("ocrmypdf required")
		}
		t.Skip("ocrmypdf not installed: OCR part skipped")
	}
	if d.Status != core.StatusDone || !d.HasSearchable || d.TextMethod != "ocr" {
		t.Fatalf("ocr result: %+v", d)
	}
	text, _, _ := f.env.Svc.DocumentText(f.ctx, f.p, fi.DocumentID)
	up := strings.ToUpper(text)
	if !strings.Contains(up, "INVOICE") && !strings.Contains(up, "SCANNED") && !strings.Contains(up, "AMOUNT") {
		t.Fatalf("OCR text does not contain expected words: %q", text)
	}
	if !strings.Contains(text, "Deckblatt") {
		t.Fatalf("digital text lost after OCR: %q", text)
	}
	// original untouched, searchable variant downloadable
	rd, ci, err := f.env.Svc.OpenDocument(f.ctx, f.p, fi.DocumentID, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	orig, _ := io.ReadAll(rd)
	rd.Close()
	if !bytes.Equal(orig, pdf) || ci.SHA256 != fi.SHA256 {
		t.Fatal("original modified")
	}
	rs, cs, err := f.env.Svc.OpenDocument(f.ctx, f.p, fi.DocumentID, 0, "searchable")
	if err != nil {
		t.Fatal(err)
	}
	rs.Close()
	if cs.SHA256 == fi.SHA256 || cs.MIME != "application/pdf" {
		t.Fatalf("searchable: %+v", cs)
	}
	// derived blob survives GC while the source is referenced, and is collected afterwards
	if n, _ := f.env.Svc.GCBlobs(f.ctx, 0); n != 0 {
		t.Fatalf("gc removed referenced blobs: %d", n)
	}
	f.env.Svc.DeleteLink(f.ctx, f.p, fi.LinkID)
	f.env.Svc.PurgeDocument(f.ctx, f.p, fi.DocumentID)
	if n, _ := f.env.Svc.GCBlobs(f.ctx, 0); n != 2 {
		t.Fatalf("gc after purge: %d", n)
	}
}

func TestJobRetryAndMaintenance(t *testing.T) {
	f := newFx(t)
	if err := core.Enqueue(f.ctx, f.env.Pool, "bogus", map[string]any{}, "", time.Time{}); err != nil {
		t.Fatal(err)
	}
	f.env.Pool.Exec(f.ctx, `UPDATE jobs SET max_attempts=2`)
	f.w.RunOne(f.ctx)
	var status string
	var attempts int
	f.env.Pool.QueryRow(f.ctx, `SELECT status, attempts FROM jobs WHERE kind='bogus'`).Scan(&status, &attempts)
	if status != "queued" || attempts != 1 {
		t.Fatalf("after first failure: %s %d", status, attempts)
	}
	f.env.Pool.Exec(f.ctx, `UPDATE jobs SET run_after=now() WHERE kind='bogus'`)
	f.w.RunOne(f.ctx)
	f.env.Pool.QueryRow(f.ctx, `SELECT status, attempts FROM jobs WHERE kind='bogus'`).Scan(&status, &attempts)
	if status != "failed" || attempts != 2 {
		t.Fatalf("after max attempts: %s %d", status, attempts)
	}
	st, err := f.env.Svc.AdminStats(f.ctx, f.p)
	if err != nil || len(st.FailedJobs) != 1 {
		t.Fatalf("stats: %+v %v", st, err)
	}
	// purge job enqueues gc
	fi := f.put(t, "/x.txt", []byte("x"))
	f.env.Svc.DeleteLink(f.ctx, f.p, fi.LinkID)
	f.env.Pool.Exec(f.ctx, `UPDATE documents SET deleted_at = now() - interval '400 days'`)
	core.Enqueue(f.ctx, f.env.Pool, core.JobPurgeTrash, map[string]any{}, core.JobPurgeTrash, time.Time{})
	f.drain(t)
	var docs int
	f.env.Pool.QueryRow(f.ctx, `SELECT count(*) FROM documents`).Scan(&docs)
	if docs != 0 {
		t.Fatalf("documents after purge: %d", docs)
	}
	// stale running job reclaimed
	core.Enqueue(f.ctx, f.env.Pool, "bogus2", map[string]any{}, "", time.Time{})
	f.env.Pool.Exec(f.ctx, `UPDATE jobs SET status='running', locked_at=now() - interval '2 hours' WHERE kind='bogus2'`)
	if n, _ := f.env.Svc.ReclaimStaleJobs(f.ctx, time.Hour); n != 1 {
		t.Fatalf("reclaim: %d", n)
	}
	_ = json.Valid
}

func TestWorkerRunLoop(t *testing.T) {
	f := newFx(t)
	f.w.PollInterval = 100 * time.Millisecond
	ctx, cancel := context.WithCancel(f.ctx)
	done := make(chan struct{})
	go func() { f.w.Run(ctx); close(done) }()
	fi := f.put(t, "/live.txt", []byte("notify driven processing works"))
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if d := f.doc(t, fi.DocumentID); d.Status == core.StatusDone {
			cancel()
			<-done
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	t.Fatal("worker loop did not process the job")
}
