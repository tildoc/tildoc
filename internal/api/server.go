// Package api implements the REST/JSON API and serves the web UI.
package api

import (
	"context"
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
	"time"

	"github.com/tildoc/tildoc/internal/auth"
	"github.com/tildoc/tildoc/internal/blob"
	"github.com/tildoc/tildoc/internal/config"
	"github.com/tildoc/tildoc/internal/core"
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
}

// New creates the API server.
func New(svc *core.Service, cfg *config.Config, log *slog.Logger) *Server {
	return &Server{Svc: svc, Cfg: cfg, Log: log, Limiter: auth.NewLimiter(10, 15*time.Minute), Mounts: map[string]http.Handler{}}
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
		p, err := s.Svc.SessionPrincipal(r.Context(), strings.TrimSpace(strings.TrimPrefix(h, "Bearer ")))
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
	writeJSON(w, status, apiError{Error: msg, Code: code})
}

func writeJSON(w http.ResponseWriter, status int, v any) error {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	return json.NewEncoder(w).Encode(v)
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

// inlineSafe lists types that may be rendered inline by the browser.
func inlineSafe(mt string) (string, bool) {
	base, _, _ := mime.ParseMediaType(mt)
	switch {
	case base == "application/pdf":
		return "application/pdf", true
	case strings.HasPrefix(base, "text/"):
		return "text/plain; charset=utf-8", true
	case base == "image/png", base == "image/jpeg", base == "image/gif", base == "image/webp":
		return base, true
	}
	return "", false
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

// ServeContent writes a blob with safe headers. Content is never rendered
// as active content on the UI origin: downloads are attachments, inline
// rendering is limited to PDF, plain text and raster images, and a sandbox
// CSP isolates everything but PDFs.
func ServeContent(w http.ResponseWriter, r *http.Request, f blob.File, name, mt, md5 string, mod time.Time, inline bool) {
	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "private, no-cache")
	if md5 != "" {
		h.Set("ETag", `"`+md5+`"`)
	}
	ctype, ok := inlineSafe(mt)
	if inline && ok {
		h.Set("Content-Type", ctype)
		h.Set("Content-Disposition", contentDisposition("inline", name))
		if ctype != "application/pdf" {
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
			http.ServeFile(w, r, filepath.Join(root, "index.html"))
			return
		}
		if strings.HasPrefix(p, "/_app/immutable/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		fs.ServeHTTP(w, r)
	})
}
