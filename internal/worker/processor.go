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
	"regexp"
	"strconv"
	"strings"
	"sync"
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
		return p.Svc.SetProcessing(ctx, sha, core.StatusUnsupported, "", nil, "")
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
		if !p.foreignOCR(ctx, path, pages) {
			return p.Svc.SetProcessing(ctx, sha, core.StatusDone, "pdf-digital", &pages, "")
		}
		// scanned pages with someone else's OCR layer: keep that text and
		// add tildoc's own OCR of an image-only variant
		if !p.HaveOCR() || !p.Svc.Cfg.OCRRedoForeign || !p.haveTool(p.Svc.Cfg.GhostscriptBin) {
			return p.Svc.SetProcessing(ctx, sha, core.StatusDone, "pdf-ocred", &pages, "")
		}
		return p.reOCR(ctx, sha, path, pages)
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
	if err := p.Svc.StoreDerivative(ctx, sha, core.Derivation{Kind: "searchable_pdf", Input: sha, Engine: p.ocrEngine(ctx),
		Meta: map[string]any{"langs": p.Svc.Cfg.OCRLangs, "mode": "skip-text"}}, st); err != nil {
		return err
	}
	if err := p.Svc.StoreText(ctx, sha, "ocr", string(text)); err != nil {
		return err
	}
	return p.Svc.SetProcessing(ctx, sha, core.StatusDone, kind, &pages, "")
}

func (p *Processor) haveTool(bin string) bool {
	_, err := exec.LookPath(bin)
	return err == nil
}

// popplerTool returns another poppler binary next to pdftotext.
func (p *Processor) popplerTool(name string) string {
	if dir := filepath.Dir(p.Svc.Cfg.PdftotextBin); dir != "." {
		return filepath.Join(dir, name)
	}
	return name
}

var (
	pdfinfoPage = regexp.MustCompile(`(?m)^Page\s+(\d+)\s+size:\s+([\d.]+)\s+x\s+([\d.]+)\s+pts`)
	engineOnce  sync.Once
	engineName  string
)

// foreignOCR reports whether every page is a full-page image that also has
// a text layer: a scan that was OCRed elsewhere.
func (p *Processor) foreignOCR(ctx context.Context, path string, pages int) bool {
	if pages == 0 || pages > 5000 {
		return false
	}
	info, err := p.run(ctx, p.popplerTool("pdfinfo"), "-f", "1", "-l", strconv.Itoa(pages), path)
	if err != nil {
		return false
	}
	area := map[int]float64{}
	for _, m := range pdfinfoPage.FindAllStringSubmatch(string(info), -1) {
		n, _ := strconv.Atoi(m[1])
		w, _ := strconv.ParseFloat(m[2], 64)
		h, _ := strconv.ParseFloat(m[3], 64)
		area[n] = w * h
	}
	list, err := p.run(ctx, p.popplerTool("pdfimages"), "-list", path)
	if err != nil {
		return false
	}
	covered := map[int]bool{}
	for _, line := range strings.Split(string(list), "\n") {
		f := strings.Fields(line)
		// page num type width height color comp bpc enc interp object ID x-ppi y-ppi size ratio
		if len(f) < 14 {
			continue
		}
		page, err1 := strconv.Atoi(f[0])
		w, err2 := strconv.ParseFloat(f[3], 64)
		h, err3 := strconv.ParseFloat(f[4], 64)
		xppi, err4 := strconv.ParseFloat(f[12], 64)
		yppi, err5 := strconv.ParseFloat(f[13], 64)
		if err1 != nil || err2 != nil || err3 != nil || err4 != nil || err5 != nil || xppi <= 0 || yppi <= 0 {
			continue
		}
		img := (w / xppi * 72) * (h / yppi * 72)
		if a := area[page]; a > 0 && img >= 0.8*a {
			covered[page] = true
		}
	}
	return len(area) == pages && len(covered) == pages
}

// ocrEngine names the OCR tool chain (cached).
func (p *Processor) ocrEngine(ctx context.Context) string {
	engineOnce.Do(func() {
		engineName = "ocrmypdf"
		if out, err := p.run(ctx, p.Svc.Cfg.OcrmypdfBin, "--version"); err == nil {
			engineName += " " + strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
		}
		if out, err := p.run(ctx, "tesseract", "--version"); err == nil {
			first := strings.Fields(strings.SplitN(string(out), "\n", 2)[0])
			if len(first) >= 2 {
				engineName += " / tesseract " + strings.TrimPrefix(first[1], "v")
			}
		}
	})
	return engineName
}

// reOCR builds the variant tree for a PDF with a foreign OCR layer:
// original -> image-only PDF (all text removed) -> tildoc's own OCR.
func (p *Processor) reOCR(ctx context.Context, sha blob.SHA256, path string, pages int) error {
	const kind = "pdf-ocred"
	dir, err := os.MkdirTemp(p.Svc.Blobs.TempDir(), "reocr-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	imgPDF := filepath.Join(dir, "image-only.pdf")
	if _, err := p.run(ctx, p.Svc.Cfg.GhostscriptBin, "-q", "-dSAFER", "-dBATCH", "-dNOPAUSE", "-sDEVICE=pdfwrite",
		"-dFILTERTEXT", "-dPassThroughJPEGImages=true", "-dPassThroughJPXImages=true",
		"-dDownsampleColorImages=false", "-dDownsampleGrayImages=false", "-dDownsampleMonoImages=false",
		"-sOutputFile="+imgPDF, path); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return p.Svc.SetProcessing(ctx, sha, core.StatusFailed, kind, &pages, "image-only variant: "+err.Error())
	}
	gsVersion := "ghostscript"
	if out, err := p.run(ctx, p.Svc.Cfg.GhostscriptBin, "--version"); err == nil {
		gsVersion += " " + strings.TrimSpace(string(out))
	}
	imgSt, err := p.Svc.Blobs.StageFile(imgPDF, 0)
	if err != nil {
		return err
	}
	imgSHA := imgSt.SHA256
	if err := p.Svc.StoreDerivative(ctx, sha, core.Derivation{Kind: "image_only_pdf", Input: sha, Engine: gsVersion,
		Meta: map[string]any{"filter": "text"}}, imgSt); err != nil {
		return err
	}
	src, err := p.Svc.Blobs.Open(imgSHA)
	if err != nil {
		return err
	}
	srcPath := filepath.Join(dir, "in.pdf")
	tmp, err := os.Create(srcPath)
	if err == nil {
		_, err = io.Copy(tmp, src)
		tmp.Close()
	}
	src.Close()
	if err != nil {
		return err
	}
	outPDF := filepath.Join(dir, "out.pdf")
	if _, err := p.run(ctx, p.Svc.Cfg.OcrmypdfBin, "--output-type", "pdf", "--jobs", "1", "--quiet",
		"-l", p.Svc.Cfg.OCRLangs, srcPath, outPDF); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return p.Svc.SetProcessing(ctx, sha, core.StatusFailed, kind, &pages, "OCR: "+err.Error())
	}
	text, err := p.run(ctx, p.Svc.Cfg.PdftotextBin, "-q", "-enc", "UTF-8", outPDF, "-")
	if err != nil {
		return p.Svc.SetProcessing(ctx, sha, core.StatusFailed, kind, &pages, err.Error())
	}
	st, err := p.Svc.Blobs.StageFile(outPDF, 0)
	if err != nil {
		return err
	}
	ocrSHA := st.SHA256
	if err := p.Svc.StoreDerivative(ctx, sha, core.Derivation{Kind: "searchable_pdf", Input: imgSHA, Engine: p.ocrEngine(ctx),
		Meta: map[string]any{"langs": p.Svc.Cfg.OCRLangs, "mode": "full"}}, st); err != nil {
		return err
	}
	if err := p.Svc.StoreText(ctx, ocrSHA, "ocr", string(text)); err != nil {
		return err
	}
	return p.Svc.SetProcessing(ctx, sha, core.StatusDone, kind, &pages, "")
}
