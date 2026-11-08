// Package dav implements the WebDAV gateway (golang.org/x/net/webdav) on
// the tildoc VFS. PUT, MOVE and COPY are handled directly so that uploads
// are atomic (aborted uploads never create versions) and rename-over keeps
// the destination's version history.
package dav

import (
	"context"
	"encoding/xml"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/webdav"

	"github.com/tildoc/tildoc/internal/auth"
	"github.com/tildoc/tildoc/internal/blob"
	"github.com/tildoc/tildoc/internal/core"
	"github.com/tildoc/tildoc/internal/vfs"
)

// Prefix is the URL prefix the handler is mounted at.
const Prefix = "/dav"

// Handler is the WebDAV gateway.
type Handler struct {
	FS             *vfs.FS
	Log            *slog.Logger
	Limiter        *auth.Limiter
	TrustedProxies []netip.Prefix
	dav            *webdav.Handler
}

// New creates the WebDAV handler.
func New(fsys *vfs.FS, log *slog.Logger, trusted []netip.Prefix) *Handler {
	h := &Handler{FS: fsys, Log: log, Limiter: auth.NewLimiter(10, 15*time.Minute), TrustedProxies: trusted}
	h.dav = &webdav.Handler{
		Prefix:     Prefix,
		FileSystem: &davFS{fs: fsys},
		LockSystem: webdav.NewMemLS(),
		Logger: func(r *http.Request, err error) {
			if err != nil && !os.IsNotExist(err) {
				log.Debug("webdav", "method", r.Method, "path", r.URL.Path, "err", err)
			}
		},
	}
	return h
}

type ctxKey int

const (
	principalKey ctxKey = iota
	cacheKey
)

func principal(ctx context.Context) *core.Principal {
	p, _ := ctx.Value(principalKey).(*core.Principal)
	return p
}

// statCache holds nodes resolved during one request (PROPFIND resolves
// every child several times).
type statCache struct {
	mu    sync.Mutex
	nodes map[string]*vfs.Node
}

func cacheGet(ctx context.Context, name string) *vfs.Node {
	c, _ := ctx.Value(cacheKey).(*statCache)
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.nodes[name]
}

func cachePut(ctx context.Context, name string, n *vfs.Node) {
	c, _ := ctx.Value(cacheKey).(*statCache)
	if c == nil {
		return
	}
	c.mu.Lock()
	c.nodes[name] = n
	c.mu.Unlock()
}

func clientIP(r *http.Request, trusted []netip.Prefix) string {
	host := r.RemoteAddr
	if i := strings.LastIndex(host, ":"); i >= 0 {
		host = strings.Trim(host[:i], "[]")
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

// ServeHTTP authenticates (Basic, app password = access key secret) and
// dispatches.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	user, pass, ok := r.BasicAuth()
	ip := clientIP(r, h.TrustedProxies)
	keys := []string{"ip:" + ip, "login:" + strings.ToLower(user)}
	if !ok {
		w.Header().Set("WWW-Authenticate", `Basic realm="tildoc", charset="UTF-8"`)
		http.Error(w, "authentication required (username + app password)", http.StatusUnauthorized)
		return
	}
	if !h.Limiter.Allowed(keys...) {
		http.Error(w, "too many failed attempts", http.StatusTooManyRequests)
		return
	}
	p, err := h.FS.Svc.AuthenticateAppPassword(r.Context(), user, pass)
	if err != nil {
		h.Limiter.Fail(keys...)
		w.Header().Set("WWW-Authenticate", `Basic realm="tildoc", charset="UTF-8"`)
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}
	h.Limiter.Success(keys[1])
	ctx := context.WithValue(r.Context(), principalKey, p)
	ctx = context.WithValue(ctx, cacheKey, &statCache{nodes: map[string]*vfs.Node{}})
	r = r.WithContext(ctx)

	hdr := w.Header()
	hdr.Set("X-Content-Type-Options", "nosniff")
	hdr.Set("Content-Security-Policy", "sandbox; default-src 'none'")

	switch r.Method {
	case http.MethodPut:
		h.put(w, r)
	case "MOVE", "COPY":
		h.moveCopy(w, r)
	case http.MethodDelete:
		h.delete(w, r)
	default:
		h.dav.ServeHTTP(w, r)
	}
}

func (h *Handler) reqPath(r *http.Request) (string, bool) {
	if !strings.HasPrefix(r.URL.Path, Prefix) {
		return "", false
	}
	p := strings.TrimPrefix(r.URL.Path, Prefix)
	if p == "" {
		p = "/"
	}
	return p, true
}

func statusFor(err error) int {
	switch core.KindOf(err) {
	case core.ErrNotFound:
		return http.StatusConflict // missing parent
	case core.ErrForbidden, core.ErrUnauthorized:
		return http.StatusForbidden
	case core.ErrConflict, core.ErrIsFolder, core.ErrNotFolder:
		return http.StatusMethodNotAllowed
	case core.ErrInvalid:
		return http.StatusBadRequest
	case core.ErrTooLarge:
		return http.StatusRequestEntityTooLarge
	case core.ErrNotEmpty:
		return http.StatusConflict
	}
	return http.StatusInternalServerError
}

func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error, status int) {
	if status == http.StatusInternalServerError {
		h.Log.Error("webdav request failed", "method", r.Method, "path", r.URL.Path, "err", err)
		http.Error(w, "internal error", status)
		return
	}
	msg := err.Error()
	var ce *core.Error
	if errors.As(err, &ce) {
		msg = ce.Msg
	}
	http.Error(w, msg, status)
}

func (h *Handler) put(w http.ResponseWriter, r *http.Request) {
	name, ok := h.reqPath(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	p := principal(r.Context())
	_, statErr := h.FS.Stat(r.Context(), p, name)
	existed := statErr == nil
	n, err := h.FS.Put(r.Context(), p, name, r.Body, core.PutOptions{Protocol: "webdav"})
	if err != nil {
		h.fail(w, r, err, statusFor(err))
		return
	}
	w.Header().Set("ETag", n.File.ETag())
	if existed {
		w.WriteHeader(http.StatusNoContent)
	} else {
		w.WriteHeader(http.StatusCreated)
	}
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	name, ok := h.reqPath(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	err := h.FS.Remove(r.Context(), principal(r.Context()), name)
	switch {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case core.KindOf(err) == core.ErrNotFound:
		http.NotFound(w, r)
	default:
		h.fail(w, r, err, statusFor(err))
	}
}

func (h *Handler) moveCopy(w http.ResponseWriter, r *http.Request) {
	src, ok := h.reqPath(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	dst := r.Header.Get("Destination")
	u, err := url.Parse(dst)
	if err != nil || dst == "" {
		http.Error(w, "invalid Destination header", http.StatusBadRequest)
		return
	}
	if u.Host != "" && u.Host != r.Host {
		http.Error(w, "destination on another server", http.StatusBadGateway)
		return
	}
	if !strings.HasPrefix(u.Path, Prefix+"/") {
		http.Error(w, "destination outside of WebDAV root", http.StatusBadGateway)
		return
	}
	dstPath := strings.TrimPrefix(u.Path, Prefix)
	if path.Clean(dstPath) == path.Clean(src) {
		http.Error(w, "source and destination are the same", http.StatusForbidden)
		return
	}
	overwrite := strings.ToUpper(r.Header.Get("Overwrite")) != "F"
	p := principal(r.Context())
	if _, err := h.FS.Stat(r.Context(), p, src); err != nil {
		if core.KindOf(err) == core.ErrNotFound {
			http.NotFound(w, r)
			return
		}
		h.fail(w, r, err, statusFor(err))
		return
	}
	_, derr := h.FS.Stat(r.Context(), p, dstPath)
	existed := derr == nil
	if existed && !overwrite {
		http.Error(w, "destination exists", http.StatusPreconditionFailed)
		return
	}
	if r.Method == "MOVE" {
		err = h.FS.Rename(r.Context(), p, src, dstPath, overwrite)
	} else {
		err = h.FS.Copy(r.Context(), p, src, dstPath, overwrite)
	}
	if err != nil {
		h.fail(w, r, err, statusFor(err))
		return
	}
	if existed {
		w.WriteHeader(http.StatusNoContent)
	} else {
		w.WriteHeader(http.StatusCreated)
	}
}

// ---------------------------------------------------------------- webdav.FileSystem

type davFS struct {
	fs *vfs.FS
}

func toOS(op, name string, err error) error {
	if err == nil {
		return nil
	}
	var target error
	switch core.KindOf(err) {
	case core.ErrNotFound:
		target = os.ErrNotExist
	case core.ErrConflict:
		target = os.ErrExist
	case core.ErrForbidden, core.ErrUnauthorized:
		target = os.ErrPermission
	default:
		return err
	}
	return &os.PathError{Op: op, Path: name, Err: target}
}

func (d *davFS) stat(ctx context.Context, name string) (*vfs.Node, error) {
	name = "/" + strings.Trim(name, "/")
	if n := cacheGet(ctx, name); n != nil {
		return n, nil
	}
	n, err := d.fs.Stat(ctx, principal(ctx), name)
	if err != nil {
		return nil, err
	}
	cachePut(ctx, name, n)
	return n, nil
}

func (d *davFS) Mkdir(ctx context.Context, name string, _ os.FileMode) error {
	return toOS("mkdir", name, d.fs.Mkdir(ctx, principal(ctx), name))
}

func (d *davFS) RemoveAll(ctx context.Context, name string) error {
	return toOS("remove", name, d.fs.Remove(ctx, principal(ctx), name))
}

func (d *davFS) Rename(ctx context.Context, oldName, newName string) error {
	return toOS("rename", oldName, d.fs.Rename(ctx, principal(ctx), oldName, newName, false))
}

func (d *davFS) Stat(ctx context.Context, name string) (os.FileInfo, error) {
	n, err := d.stat(ctx, name)
	if err != nil {
		return nil, toOS("stat", name, err)
	}
	return info{n}, nil
}

func (d *davFS) OpenFile(ctx context.Context, name string, flag int, _ os.FileMode) (webdav.File, error) {
	n, err := d.stat(ctx, name)
	if err == nil && flag&os.O_TRUNC == 0 {
		return &file{fs: d, ctx: ctx, node: n}, nil
	}
	if flag&(os.O_CREATE|os.O_TRUNC) == 0 {
		return nil, toOS("open", name, err)
	}
	if err != nil && core.KindOf(err) != core.ErrNotFound {
		return nil, toOS("open", name, err)
	}
	if n != nil && n.IsDir() {
		return nil, &os.PathError{Op: "open", Path: name, Err: os.ErrExist}
	}
	tmp, terr := d.fs.Svc.Blobs.TempFile("dav-*")
	if terr != nil {
		return nil, terr
	}
	return &writeFile{fs: d, ctx: ctx, name: name, tmp: tmp}, nil
}

// info adapts a vfs.Node to os.FileInfo (+ webdav ETager/ContentTyper).
type info struct{ n *vfs.Node }

func (i info) Name() string {
	if i.n.Kind == vfs.KindRoot {
		return "/"
	}
	return i.n.Name
}
func (i info) Size() int64 { return i.n.Size }
func (i info) Mode() fs.FileMode {
	if i.n.IsDir() {
		return fs.ModeDir | 0o755
	}
	return 0o644
}
func (i info) ModTime() time.Time { return i.n.ModTime }
func (i info) IsDir() bool        { return i.n.IsDir() }
func (i info) Sys() any           { return nil }

// ETag implements webdav.ETager.
func (i info) ETag(context.Context) (string, error) {
	if i.n.File == nil {
		return "", webdav.ErrNotImplemented
	}
	return i.n.File.ETag(), nil
}

// ContentType implements webdav.ContentTyper.
func (i info) ContentType(context.Context) (string, error) {
	if i.n.File == nil {
		return "httpd/unix-directory", nil
	}
	return i.n.File.MIME, nil
}

// file is a read-only handle; content is opened lazily (PROPFIND opens
// every resource without reading it).
type file struct {
	fs   *davFS
	ctx  context.Context
	node *vfs.Node
	rd   blob.File
	list []fs.FileInfo
	pos  int
}

func (f *file) open() error {
	if f.rd != nil {
		return nil
	}
	if f.node.File == nil {
		return &os.PathError{Op: "read", Path: f.node.Path, Err: errors.New("is a directory")}
	}
	rd, err := f.fs.fs.Svc.OpenFile(f.ctx, principal(f.ctx), f.node.File)
	if err != nil {
		return toOS("open", f.node.Path, err)
	}
	f.rd = rd
	return nil
}

func (f *file) Read(p []byte) (int, error) {
	if err := f.open(); err != nil {
		return 0, err
	}
	return f.rd.Read(p)
}

func (f *file) Seek(off int64, whence int) (int64, error) {
	if f.node.File != nil && f.rd == nil && off == 0 && whence == io.SeekEnd {
		return f.node.Size, nil
	}
	if err := f.open(); err != nil {
		return 0, err
	}
	return f.rd.Seek(off, whence)
}

func (f *file) Write([]byte) (int, error) {
	return 0, &os.PathError{Op: "write", Path: f.node.Path, Err: os.ErrPermission}
}

func (f *file) Close() error {
	if f.rd != nil {
		return f.rd.Close()
	}
	return nil
}

func (f *file) Stat() (fs.FileInfo, error) { return info{f.node}, nil }

func (f *file) Readdir(count int) ([]fs.FileInfo, error) {
	if !f.node.IsDir() {
		return nil, &os.PathError{Op: "readdir", Path: f.node.Path, Err: errors.New("not a directory")}
	}
	if f.list == nil {
		nodes, err := f.fs.fs.List(f.ctx, principal(f.ctx), f.node.Path)
		if err != nil {
			return nil, toOS("readdir", f.node.Path, err)
		}
		f.list = make([]fs.FileInfo, 0, len(nodes))
		for i := range nodes {
			n := &nodes[i]
			cachePut(f.ctx, n.Path, n)
			f.list = append(f.list, info{n})
		}
	}
	rest := f.list[f.pos:]
	if count <= 0 {
		f.pos = len(f.list)
		return rest, nil
	}
	if len(rest) == 0 {
		return nil, io.EOF
	}
	if count > len(rest) {
		count = len(rest)
	}
	f.pos += count
	return rest[:count], nil
}

var linksProp = xml.Name{Space: "urn:tildoc", Local: "links"}
var docProp = xml.Name{Space: "urn:tildoc", Local: "document-id"}
var tagsProp = xml.Name{Space: "urn:tildoc", Local: "tags"}

// DeadProps implements webdav.DeadPropsHolder: exposes tildoc:links (all
// paths the document appears at), tildoc:document-id and tildoc:tags.
func (f *file) DeadProps() (map[xml.Name]webdav.Property, error) {
	if f.node.File == nil {
		return nil, nil
	}
	var tags strings.Builder
	for _, t := range f.node.File.Tags {
		tags.WriteString("<tag>" + xmlEscape(t.Name) + "</tag>")
	}
	props := map[xml.Name]webdav.Property{
		docProp:  {XMLName: docProp, InnerXML: []byte(xmlEscape(f.node.File.DocumentID))},
		tagsProp: {XMLName: tagsProp, InnerXML: []byte(tags.String())},
	}
	if f.node.File.LinkCount > 1 {
		d, err := f.fs.fs.Svc.GetDocument(f.ctx, principal(f.ctx), f.node.File.DocumentID)
		if err == nil {
			var b strings.Builder
			for _, l := range d.Links {
				b.WriteString("<path>" + xmlEscape("/"+f.node.Space.Slug+l.Path) + "</path>")
			}
			props[linksProp] = webdav.Property{XMLName: linksProp, InnerXML: []byte(b.String())}
		}
	} else {
		props[linksProp] = webdav.Property{XMLName: linksProp, InnerXML: []byte("<path>" + xmlEscape(f.node.Path) + "</path>")}
	}
	return props, nil
}

// Patch accepts (and ignores) client properties such as Windows timestamps;
// tildoc properties are read-only.
func (f *file) Patch(patches []webdav.Proppatch) ([]webdav.Propstat, error) {
	ok := webdav.Propstat{Status: http.StatusOK}
	denied := webdav.Propstat{Status: http.StatusForbidden}
	for _, pp := range patches {
		for _, prop := range pp.Props {
			if prop.XMLName.Space == "urn:tildoc" {
				denied.Props = append(denied.Props, webdav.Property{XMLName: prop.XMLName})
			} else {
				ok.Props = append(ok.Props, webdav.Property{XMLName: prop.XMLName})
			}
		}
	}
	var out []webdav.Propstat
	if len(ok.Props) > 0 {
		out = append(out, ok)
	}
	if len(denied.Props) > 0 {
		out = append(out, denied)
	}
	return out, nil
}

func xmlEscape(s string) string {
	var b strings.Builder
	xml.EscapeText(&b, []byte(s))
	return b.String()
}

// writeFile spools writes (LOCK on unmapped URLs creates empty files) into
// a temp file that is committed on Close.
type writeFile struct {
	fs   *davFS
	ctx  context.Context
	name string
	tmp  *os.File
	size int64
}

func (w *writeFile) Write(p []byte) (int, error) {
	n, err := w.tmp.Write(p)
	w.size += int64(n)
	return n, err
}
func (w *writeFile) Read([]byte) (int, error)           { return 0, io.EOF }
func (w *writeFile) Seek(int64, int) (int64, error)     { return 0, nil }
func (w *writeFile) Readdir(int) ([]fs.FileInfo, error) { return nil, os.ErrInvalid }
func (w *writeFile) Stat() (fs.FileInfo, error) {
	return info{&vfs.Node{Kind: vfs.KindFile, Name: path.Base(w.name), Path: w.name, Size: w.size, ModTime: time.Now()}}, nil
}

func (w *writeFile) Close() error {
	if err := w.tmp.Close(); err != nil {
		os.Remove(w.tmp.Name())
		return err
	}
	st, err := w.fs.fs.Svc.Blobs.StageFile(w.tmp.Name(), w.fs.fs.Svc.Cfg.MaxUploadBytes)
	if err != nil {
		os.Remove(w.tmp.Name())
		return err
	}
	_, err = w.fs.fs.PutStaged(w.ctx, principal(w.ctx), w.name, st, core.PutOptions{Protocol: "webdav"})
	return toOS("close", w.name, err)
}
