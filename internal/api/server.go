// Package api implements the REST/JSON API and serves the web UI.
package api

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tildoc/tildoc/internal/auth"
	"github.com/tildoc/tildoc/internal/blob"
	"github.com/tildoc/tildoc/internal/config"
	"github.com/tildoc/tildoc/internal/core"
	"github.com/tildoc/tildoc/internal/oidc"
	"github.com/tildoc/tildoc/internal/wopi"
)

// SessionCookie is the name of the session cookie.
const SessionCookie = "tildoc_session"

// CSRFHeader must be present on unsafe requests authenticated by cookie.
const CSRFHeader = "X-Tildoc-CSRF"

// Server is the REST API.
type Server struct {
	Svc     *core.Service
	Cfg     *config.Config
	Log     *slog.Logger
	Limiter *auth.Limiter
	// Extra handlers mounted on the same listener (e.g. WebDAV at /dav/).
	Mounts map[string]http.Handler
	// Events feeds GET /api/v1/events; run it with Events.Run.
	Events *core.EventHub
	// Office is the Collabora discovery client (nil: feature off).
	Office         *wopi.Client
	googleMu       sync.Mutex
	googleProvider *oidc.Provider // discovered on first use

	stop     chan struct{}
	stopOnce sync.Once
}

// New creates the API server.
func New(svc *core.Service, cfg *config.Config, log *slog.Logger) *Server {
	return &Server{Svc: svc, Cfg: cfg, Log: log, Limiter: auth.NewLimiter(10, 15*time.Minute), Mounts: map[string]http.Handler{},
		Events: svc.NewEventHub(), Office: newOfficeClient(cfg.OfficeInternalURL), stop: make(chan struct{})}
}

type ctxKey int

const principalKey ctxKey = 1

func principal(r *http.Request) *core.Principal {
	p, _ := r.Context().Value(principalKey).(*core.Principal)
	return p
}

// ClientIP returns the client address, honoring X-Forwarded-For only from
// trusted proxies.
func ClientIP(r *http.Request, trusted []netip.Prefix) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return host
	}
	for _, pfx := range trusted {
		if pfx.Contains(addr.Unmap()) {
			if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
				parts := strings.Split(xff, ",")
				return strings.TrimSpace(parts[len(parts)-1])
			}
		}
	}
	return addr.String()
}

// Handler returns the HTTP handler with all routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	s.routes(mux)
	for prefix, h := range s.Mounts {
		mux.Handle(prefix, h)
	}
	if s.Cfg.UIDir != "" {
		mux.Handle("/", s.uiHandler())
	}
	return s.recoverer(securityHeaders(mux))
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		if !strings.HasPrefix(r.URL.Path, "/dav") {
			h.Set("X-Frame-Options", "SAMEORIGIN")
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler {
					panic(v)
				}
				s.Log.Error("panic", "path", r.URL.Path, "panic", v)
				http.Error(w, "internal error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// authn resolves the caller from session cookie or bearer token.
func (s *Server) authn(r *http.Request) (*core.Principal, bool, error) {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		token := strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
		if strings.HasPrefix(token, core.APIKeyPrefix) {
			p, err := s.Svc.APIKeyPrincipal(r.Context(), token)
			return p, false, err
		}
		p, err := s.Svc.SessionPrincipal(r.Context(), token)
		return p, false, err
	}
	c, err := r.Cookie(SessionCookie)
	if err != nil {
		return nil, false, core.ErrUnauthorized
	}
	p, err := s.Svc.SessionPrincipal(r.Context(), c.Value)
	return p, true, err
}

type handlerFunc func(w http.ResponseWriter, r *http.Request) error

// public wraps a handler that needs no authentication.
func (s *Server) public(h handlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w = conditional(w, r)
		if err := h(w, r); err != nil {
			s.writeError(w, r, err)
		}
	})
}

// authed wraps a handler that requires a valid session.
func (s *Server) authed(h handlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, viaCookie, err := s.authn(r)
		if err != nil {
			s.writeError(w, r, core.ErrUnauthorized)
			return
		}
		if viaCookie && !safeMethod(r.Method) && r.Header.Get(CSRFHeader) == "" {
			s.writeError(w, r, &core.Error{Kind: core.ErrForbidden, Msg: "missing " + CSRFHeader + " header"})
			return
		}
		ctx := context.WithValue(r.Context(), principalKey, p)
		w = conditional(w, r)
		if err := h(w, r.WithContext(ctx)); err != nil {
			s.writeError(w, r, err)
		}
	})
}

func safeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

type apiError struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

// StatusFor maps core errors to HTTP status codes.
func StatusFor(err error) (int, string) {
	switch core.KindOf(err) {
	case core.ErrNotFound:
		return http.StatusNotFound, "not_found"
	case core.ErrForbidden:
		return http.StatusForbidden, "forbidden"
	case core.ErrUnauthorized:
		return http.StatusUnauthorized, "unauthorized"
	case core.ErrConflict, core.ErrNotEmpty, core.ErrIsFolder, core.ErrNotFolder:
		return http.StatusConflict, "conflict"
	case core.ErrInvalid:
		return http.StatusBadRequest, "invalid"
	case core.ErrTooLarge:
		return http.StatusRequestEntityTooLarge, "too_large"
	case core.ErrPrecondition:
		return http.StatusPreconditionFailed, "precondition_failed"
	case core.ErrChecksum:
		return http.StatusUnprocessableEntity, "checksum_mismatch"
	case core.ErrTooMany:
		return http.StatusTooManyRequests, "too_many"
	}
	return http.StatusInternalServerError, "internal"
}

func (s *Server) writeError(w http.ResponseWriter, r *http.Request, err error) {
	status, code := StatusFor(err)
	msg := err.Error()
	var ce *core.Error
	if errors.As(err, &ce) {
		msg = ce.Msg
	} else if k := core.KindOf(err); k != nil {
		msg = k.Error()
	}
	if status == http.StatusInternalServerError {
		s.Log.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
		msg = "internal error"
	}
	writeJSON(w, status, apiError{Error: msg, Code: code}) //nolint:errcheck // the client is gone
}

// writeJSON writes v. Successful GET responses get an ETag (304 when the
// client sends it back in If-None-Match) and gzip when accepted. They stay
// no-store: authenticated data never lands in the browser's disk cache; the
// UI keeps its own in-memory cache and sends If-None-Match itself.
func writeJSON(w http.ResponseWriter, status int, v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return err
	}
	body = append(body, '\n')
	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	if cw, ok := w.(*condWriter); ok && status == http.StatusOK {
		sum := sha256.Sum256(body)
		etag := `W/"` + base64.RawURLEncoding.EncodeToString(sum[:18]) + `"`
		h.Set("ETag", etag)
		h.Set("Vary", "Accept-Encoding")
		if etagMatch(cw.r.Header.Get("If-None-Match"), etag) {
			h.Del("Content-Type")
			w.WriteHeader(http.StatusNotModified)
			return nil
		}
		if len(body) > 1024 && acceptsGzip(cw.r) {
			h.Set("Content-Encoding", "gzip")
			w.WriteHeader(status)
			gz := gzipPool.Get().(*gzip.Writer)
			defer gzipPool.Put(gz)
			gz.Reset(w)
			if _, err := gz.Write(body); err != nil {
				return nil // client gone; headers are out
			}
			gz.Close()
			return nil
		}
	}
	w.WriteHeader(status)
	w.Write(body)
	return nil
}

// condWriter marks GET/HEAD responses that may use ETag and compression.
type condWriter struct {
	http.ResponseWriter
	r *http.Request
}

func (c *condWriter) Unwrap() http.ResponseWriter { return c.ResponseWriter }

func conditional(w http.ResponseWriter, r *http.Request) http.ResponseWriter {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return &condWriter{w, r}
	}
	return w
}

var gzipPool = sync.Pool{New: func() any {
	gz, _ := gzip.NewWriterLevel(io.Discard, gzip.DefaultCompression)
	return gz
}}

func acceptsGzip(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		name, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		if strings.EqualFold(strings.TrimSpace(name), "gzip") {
			return !strings.Contains(strings.ReplaceAll(params, " ", ""), "q=0") || strings.Contains(params, "q=0.")
		}
	}
	return false
}

func etagMatch(header, etag string) bool {
	for _, t := range strings.Split(header, ",") {
		t = strings.TrimSpace(t)
		if t == "*" || strings.TrimPrefix(t, "W/") == strings.TrimPrefix(etag, "W/") {
			return true
		}
	}
	return false
}

func readJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return &core.Error{Kind: core.ErrInvalid, Msg: "invalid JSON body: " + err.Error()}
	}
	return nil
}

func queryInt(r *http.Request, key string, def int) int {
	if v := r.URL.Query().Get(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

// ---------------------------------------------------------------- content

// inlineSafe says how a type may be rendered inline by the browser.
func inlineSafe(mt string) (string, bool) {
	base, _, _ := mime.ParseMediaType(mt)
	switch {
	case !core.InlineSafe(mt):
		return "", false
	case strings.HasPrefix(base, "text/"):
		return "text/plain; charset=utf-8", true
	}
	return base, true
}

func contentDisposition(kind, name string) string {
	ascii := strings.Map(func(r rune) rune {
		if r < 0x20 || r > 0x7e || r == '"' || r == '\\' {
			return '_'
		}
		return r
	}, name)
	return kind + `; filename="` + ascii + `"; filename*=UTF-8''` + url.PathEscape(name)
}

// Disposition selects how ServeContent delivers a blob.
type Disposition int

const (
	Attachment   Disposition = iota // download
	Inline                          // inline, sandboxed
	InlineNative                    // inline PDF for the browser's own viewer, unsandboxed
)

// disposition reads ?inline=1 and ?viewer=native. The unsandboxed native
// PDF mode is only granted to users who chose the browser's viewer in their
// preferences, so a crafted link cannot opt other users out of the sandbox.
func (s *Server) disposition(r *http.Request) Disposition {
	q := r.URL.Query()
	if q.Get("inline") != "1" {
		return Attachment
	}
	if q.Get("viewer") == "native" {
		if p := principal(r); p != nil {
			if prefs, err := s.Svc.GetPrefs(r.Context(), p.UserID); err == nil && prefs.PDFViewer == core.PDFViewerNative {
				return InlineNative
			}
		}
	}
	return Inline
}

// ServeContent writes a blob with safe headers. Content is never rendered
// as active content on the UI origin: downloads are attachments, inline
// rendering is limited to PDF, plain text and raster images, and every
// response carries a sandbox CSP. The single exception is InlineNative for
// PDFs (browsers refuse to run their PDF viewer in a sandboxed document).
func ServeContent(w http.ResponseWriter, r *http.Request, f blob.File, name, mt, md5 string, mod time.Time, disp Disposition) {
	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "private, no-cache")
	if md5 != "" {
		h.Set("ETag", `"`+md5+`"`)
	}
	ctype, ok := inlineSafe(mt)
	if disp != Attachment && ok {
		h.Set("Content-Type", ctype)
		h.Set("Content-Disposition", contentDisposition("inline", name))
		if !(disp == InlineNative && ctype == "application/pdf") {
			h.Set("Content-Security-Policy", "sandbox; default-src 'none'; img-src 'self' data:; style-src 'unsafe-inline'")
		}
	} else {
		if mt == "" {
			mt = "application/octet-stream"
		}
		h.Set("Content-Type", mt)
		h.Set("Content-Disposition", contentDisposition("attachment", name))
		h.Set("Content-Security-Policy", "sandbox; default-src 'none'")
	}
	http.ServeContent(w, r, "", mod, f)
}

// ---------------------------------------------------------------- UI

// UIFramePolicy narrows the page's own CSP (a meta tag in the build, which
// cannot know the deployment): frames and form posts only to tildoc and its
// Collabora. deploy/nginx/tildoc.conf.template sets the same.
func UIFramePolicy(officeURL string) string {
	return "frame-src 'self' " + officeURL + "; form-action 'self' " + officeURL
}

func (s *Server) uiHandler() http.Handler {
	root := s.Cfg.UIDir
	fs := http.FileServer(http.Dir(root))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		p := path.Clean("/" + r.URL.Path)
		if st, err := os.Stat(filepath.Join(root, filepath.FromSlash(p))); err != nil || st.IsDir() {
			// SPA fallback
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Content-Security-Policy", UIFramePolicy(s.Cfg.OfficeURL))
			http.ServeFile(w, r, filepath.Join(root, "index.html"))
			return
		}
		if strings.HasPrefix(p, "/_app/immutable/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		if servePrecompressed(w, r, root, p) {
			return
		}
		fs.ServeHTTP(w, r)
	})
}

// servePrecompressed serves the .br or .gz file the build produced next to
// a static file (adapter-static precompress), if the client accepts it.
func servePrecompressed(w http.ResponseWriter, r *http.Request, root, p string) bool {
	ae := r.Header.Get("Accept-Encoding")
	for _, enc := range []struct{ name, ext string }{{"br", ".br"}, {"gzip", ".gz"}} {
		if !strings.Contains(ae, enc.name) {
			continue
		}
		f, err := os.Open(filepath.Join(root, filepath.FromSlash(p)+enc.ext))
		if err != nil {
			continue
		}
		defer f.Close()
		st, err := f.Stat()
		if err != nil || st.IsDir() {
			return false
		}
		ctype := mime.TypeByExtension(path.Ext(p))
		if ctype == "" {
			ctype = "application/octet-stream"
		}
		h := w.Header()
		h.Set("Content-Type", ctype)
		h.Set("Content-Encoding", enc.name)
		h.Add("Vary", "Accept-Encoding")
		http.ServeContent(w, r, "", st.ModTime(), f)
		return true
	}
	h := w.Header()
	h.Add("Vary", "Accept-Encoding")
	return false
}
