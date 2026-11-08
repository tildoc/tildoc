package worker

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/tildoc/tildoc/internal/blob"
	"github.com/tildoc/tildoc/internal/core"
)

// minPageChars is the number of non-space characters below which a PDF
// page is considered to have no text layer.
const minPageChars = 10

// Processor extracts text and derives searchable PDFs. New document types
// are added as further cases in Process (dispatch by detected MIME type).
type Processor struct {
	Svc *core.Service
	Log *slog.Logger
}

// NewProcessor creates a processor.
func NewProcessor(svc *core.Service, log *slog.Logger) *Processor {
	return &Processor{Svc: svc, Log: log}
}

// HavePdftotext reports whether poppler's pdftotext is available.
func (p *Processor) HavePdftotext() bool {
	_, err := exec.LookPath(p.Svc.Cfg.PdftotextBin)
	return err == nil
}

// HaveOCR reports whether OCR is enabled and ocrmypdf is available.
func (p *Processor) HaveOCR() bool {
	if !p.Svc.Cfg.OCREnabled {
		return false
	}
	_, err := exec.LookPath(p.Svc.Cfg.OcrmypdfBin)
	return err == nil
}

// Process handles one blob. Content-level failures (corrupt or encrypted
// files, tool errors) are recorded as status "failed" and do not fail the
// job; infrastructure errors (database) are returned for retry.
func (p *Processor) Process(ctx context.Context, sha blob.SHA256, force bool) error {
	state, err := p.Svc.ProcessingState(ctx, sha)
	if err != nil {
		return err
	}
	if !force && (state == core.StatusDone || state == core.StatusUnsupported) {
		return nil
	}
	path, err := p.Svc.Blobs.LocalPath(sha)
	if err != nil {
		return p.Svc.SetProcessing(ctx, sha, core.StatusFailed, "", nil, "blob file missing")
	}
	if err := p.Svc.SetProcessing(ctx, sha, core.StatusProcessing, "", nil, ""); err != nil {
		return err
	}
	head, err := readHead(path, 8192)
	if err != nil {
		return p.Svc.SetProcessing(ctx, sha, core.StatusFailed, "", nil, err.Error())
	}
	switch mt := core.DetectMIME(head); {
	case mt == "application/pdf":
		return p.processPDF(ctx, sha, path)
	case looksLikeText(head):
		return p.processText(ctx, sha, path)
	default:
		return p.Svc.SetProcessing(ctx, sha, core.StatusUnsupported, mt, nil, "")
	}
}

func readHead(path string, n int) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, n)
	m, err := io.ReadFull(f, buf)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return nil, err
	}
	return buf[:m], nil
}

// looksLikeText accepts UTF-8 text without NUL bytes and with few control
// characters (plain text, Markdown, CSV, JSON, source code, ...).
func looksLikeText(b []byte) bool {
	if len(b) == 0 || bytes.IndexByte(b, 0) >= 0 {
		return false
	}
	// tolerate a rune cut at the end of the sample
	for i := 0; i < 4 && !utf8.Valid(b) && len(b) > 0; i++ {
		b = b[:len(b)-1]
	}
	if !utf8.Valid(b) {
		return false
	}
	ctrl := 0
	for _, r := range string(b) {
		if unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' && r != '\f' {
			ctrl++
		}
	}
	return ctrl*100 < len(b)
}

func (p *Processor) processText(ctx context.Context, sha blob.SHA256, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, core.MaxTextBytes))
	if err != nil {
		return err
	}
	if err := p.Svc.StoreText(ctx, sha, "text", string(b)); err != nil {
		return err
	}
	return p.Svc.SetProcessing(ctx, sha, core.StatusDone, "text", nil, "")
}

func (p *Processor) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &limitedBuffer{buf: &stdout, max: 2 * core.MaxTextBytes}
	cmd.Stderr = &limitedBuffer{buf: &stderr, max: 64 << 10}
	cmd.Env = append(os.Environ(), "OMP_THREAD_LIMIT=1")
	err := cmd.Run()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return stdout.Bytes(), fmt.Errorf("%s: %s", filepath.Base(name), msg)
	}
	return stdout.Bytes(), nil
}

type limitedBuffer struct {
	buf *bytes.Buffer
	max int
}

func (l *limitedBuffer) Write(p []byte) (int, error) {
	if rem := l.max - l.buf.Len(); rem > 0 {
		if len(p) > rem {
			l.buf.Write(p[:rem])
		} else {
			l.buf.Write(p)
		}
	}
	return len(p), nil
}

// pageStats splits pdftotext output (pages separated by form feeds) and
// counts pages without a usable text layer.
func pageStats(text string) (pages, empty int) {
	parts := strings.Split(text, "\f")
	if len(parts) > 1 && strings.TrimSpace(parts[len(parts)-1]) == "" {
		parts = parts[:len(parts)-1]
	}
	for _, pg := range parts {
		n := 0
		for _, r := range pg {
			if !unicode.IsSpace(r) {
				n++
			}
		}
		if n < minPageChars {
			empty++
		}
	}
	return len(parts), empty
}

func (p *Processor) processPDF(ctx context.Context, sha blob.SHA256, path string) error {
	if !p.HavePdftotext() {
		return p.Svc.SetProcessing(ctx, sha, core.StatusFailed, "pdf", nil, "pdftotext (poppler) is not installed")
	}
	out, err := p.run(ctx, p.Svc.Cfg.PdftotextBin, "-q", "-enc", "UTF-8", path, "-")
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return p.Svc.SetProcessing(ctx, sha, core.StatusFailed, "pdf", nil, err.Error())
	}
	text := string(out)
	pages, empty := pageStats(text)
	if empty == 0 {
		if err := p.Svc.StoreText(ctx, sha, "pdftotext", text); err != nil {
			return err
		}
		return p.Svc.SetProcessing(ctx, sha, core.StatusDone, "pdf-digital", &pages, "")
	}
	kind := "pdf-scanned"
	if empty < pages {
		kind = "pdf-mixed"
	}
	if !p.HaveOCR() {
		if strings.TrimSpace(text) != "" {
			if err := p.Svc.StoreText(ctx, sha, "pdftotext", text); err != nil {
				return err
			}
		}
		return p.Svc.SetProcessing(ctx, sha, core.StatusOCRPending, kind, &pages, "")
	}
	return p.ocr(ctx, sha, path, kind, pages)
}

// ocr runs ocrmypdf (only pages without text are OCRed), stores the result
// as derived searchable PDF and indexes the text of the result.
func (p *Processor) ocr(ctx context.Context, sha blob.SHA256, path, kind string, pages int) error {
	dir, err := os.MkdirTemp(p.Svc.Blobs.TempDir(), "ocr-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	outPDF := filepath.Join(dir, "out.pdf")
	args := []string{"--skip-text", "--output-type", "pdf", "--jobs", "1", "--quiet",
		"-l", p.Svc.Cfg.OCRLangs, path, outPDF}
	if _, err := p.run(ctx, p.Svc.Cfg.OcrmypdfBin, args...); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return p.Svc.SetProcessing(ctx, sha, core.StatusFailed, kind, &pages, err.Error())
	}
	text, err := p.run(ctx, p.Svc.Cfg.PdftotextBin, "-q", "-enc", "UTF-8", outPDF, "-")
	if err != nil {
		return p.Svc.SetProcessing(ctx, sha, core.StatusFailed, kind, &pages, err.Error())
	}
	st, err := p.Svc.Blobs.StageFile(outPDF, 0)
	if err != nil {
		return err
	}
	// StageFile adopts the file; move it out of the temp dir removed by defer
	if err := p.Svc.StoreDerivative(ctx, sha, "searchable_pdf", st); err != nil {
		return err
	}
	if err := p.Svc.StoreText(ctx, sha, "ocr", string(text)); err != nil {
		return err
	}
	return p.Svc.SetProcessing(ctx, sha, core.StatusDone, kind, &pages, "")
}

