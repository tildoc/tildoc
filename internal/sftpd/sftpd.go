// Package sftpd implements the SFTP gateway (golang.org/x/crypto/ssh +
// github.com/pkg/sftp request server) on the tildoc VFS.
//
// Authentication: SSH public keys registered in the UI, or username +
// app password (access key secret).
package sftpd

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"github.com/tildoc/tildoc/internal/auth"
	"github.com/tildoc/tildoc/internal/blob"
	"github.com/tildoc/tildoc/internal/core"
	"github.com/tildoc/tildoc/internal/vfs"
)

// Server is the SFTP gateway.
type Server struct {
	FS      *vfs.FS
	Log     *slog.Logger
	Limiter *auth.Limiter
	config  *ssh.ServerConfig

	mu       sync.Mutex
	listener net.Listener
	conns    map[net.Conn]struct{}
	closed   bool
}

// LoadOrCreateHostKey loads the ed25519 host key or generates it (0600).
func LoadOrCreateHostKey(path string) (ssh.Signer, error) {
	if b, err := os.ReadFile(path); err == nil {
		return ssh.ParsePrivateKey(b)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	blk, err := ssh.MarshalPrivateKey(priv, "tildoc host key")
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(blk), 0o600); err != nil {
		return nil, err
	}
	return ssh.NewSignerFromKey(priv)
}

// New creates the SFTP server with the given host key.
func New(fsys *vfs.FS, log *slog.Logger, hostKey ssh.Signer) *Server {
	s := &Server{FS: fsys, Log: log, Limiter: auth.NewLimiter(10, 15*time.Minute), conns: map[net.Conn]struct{}{}}
	cfg := &ssh.ServerConfig{
		ServerVersion: "SSH-2.0-tildoc",
		MaxAuthTries:  6,
		PublicKeyCallback: func(meta ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if !s.allowed(meta) {
				return nil, errors.New("rate limited")
			}
			p, err := fsys.Svc.SSHKeyPrincipal(context.Background(), meta.User(), key)
			if err != nil {
				// a client offering several keys is normal; only count the IP lightly
				return nil, err
			}
			return perms(p), nil
		},
		PasswordCallback: func(meta ssh.ConnMetadata, pw []byte) (*ssh.Permissions, error) {
			if !s.allowed(meta) {
				return nil, errors.New("rate limited")
			}
			p, err := fsys.Svc.AuthenticateAppPassword(context.Background(), meta.User(), string(pw))
			if err != nil {
				s.Limiter.Fail(limitKeys(meta)...)
				return nil, err
			}
			return perms(p), nil
		},
	}
	cfg.AddHostKey(hostKey)
	s.config = cfg
	return s
}

func limitKeys(meta ssh.ConnMetadata) []string {
	host, _, _ := net.SplitHostPort(meta.RemoteAddr().String())
	return []string{"ip:" + host, "login:" + strings.ToLower(meta.User())}
}

func (s *Server) allowed(meta ssh.ConnMetadata) bool { return s.Limiter.Allowed(limitKeys(meta)...) }

func perms(p *core.Principal) *ssh.Permissions {
	return &ssh.Permissions{Extensions: map[string]string{
		"user_id": p.UserID, "username": p.Username, "via": p.Via, "key_id": p.KeyID,
		"scope": p.ScopeSpaceID, "read_only": strconv.FormatBool(p.ReadOnly),
	}}
}

func principalFrom(pm *ssh.Permissions) *core.Principal {
	e := pm.Extensions
	ro, _ := strconv.ParseBool(e["read_only"])
	return &core.Principal{UserID: e["user_id"], Username: e["username"], Via: e["via"], KeyID: e["key_id"],
		ScopeSpaceID: e["scope"], ReadOnly: ro}
}

// Serve accepts connections until the listener is closed.
func (s *Server) Serve(l net.Listener) error {
	s.mu.Lock()
	s.listener = l
	s.mu.Unlock()
	for {
		c, err := l.Accept()
		if err != nil {
			s.mu.Lock()
			closed := s.closed
			s.mu.Unlock()
			if closed {
				return nil
			}
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				time.Sleep(50 * time.Millisecond)
				continue
			}
			return err
		}
		go s.handle(c)
	}
}

// Close stops the server and closes active connections.
func (s *Server) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	for c := range s.conns {
		c.Close()
	}
	if s.listener != nil {
		return s.listener.Close()
	}
	return nil
}

func (s *Server) handle(c net.Conn) {
	s.mu.Lock()
	s.conns[c] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.conns, c)
		s.mu.Unlock()
		c.Close()
	}()
	c.SetDeadline(time.Now().Add(30 * time.Second))
	sconn, chans, reqs, err := ssh.NewServerConn(c, s.config)
	if err != nil {
		s.Log.Debug("ssh handshake failed", "remote", c.RemoteAddr(), "err", err)
		return
	}
	c.SetDeadline(time.Time{})
	defer sconn.Close()
	p := principalFrom(sconn.Permissions)
	s.Limiter.Success("login:" + strings.ToLower(p.Username))
	s.Log.Info("sftp login", "user", p.Username, "via", p.Via, "remote", c.RemoteAddr())
	go ssh.DiscardRequests(reqs)
	for nc := range chans {
		if nc.ChannelType() != "session" {
			nc.Reject(ssh.UnknownChannelType, "only session channels are supported")
			continue
		}
		ch, creqs, err := nc.Accept()
		if err != nil {
			continue
		}
		go s.session(ch, creqs, p)
	}
}

func (s *Server) session(ch ssh.Channel, reqs <-chan *ssh.Request, p *core.Principal) {
	defer ch.Close()
	for req := range reqs {
		if req.Type == "subsystem" && len(req.Payload) >= 4 && string(req.Payload[4:]) == "sftp" {
			req.Reply(true, nil)
			ctx, cancel := context.WithCancel(context.Background())
			h := &handler{fs: s.FS, p: p, ctx: ctx, log: s.Log}
			srv := sftp.NewRequestServer(ch, sftp.Handlers{FileGet: h, FilePut: h, FileCmd: h, FileList: h})
			if err := srv.Serve(); err != nil && err != io.EOF {
				s.Log.Debug("sftp session ended", "err", err)
			}
			srv.Close()
			cancel()
			return
		}
		if req.WantReply {
			req.Reply(false, nil)
		}
	}
}

// ---------------------------------------------------------------- handlers

type handler struct {
	fs  *vfs.FS
	p   *core.Principal
	ctx context.Context
	log *slog.Logger
}

func toOS(err error) error {
	if err == nil {
		return nil
	}
	switch core.KindOf(err) {
	case core.ErrNotFound:
		return os.ErrNotExist
	case core.ErrForbidden, core.ErrUnauthorized:
		return os.ErrPermission
	case core.ErrConflict:
		return os.ErrExist
	case core.ErrNotEmpty:
		return errors.New("directory not empty")
	}
	var ce *core.Error
	if errors.As(err, &ce) {
		return errors.New(ce.Msg)
	}
	return err
}

// Fileread opens a file for reading.
func (h *handler) Fileread(r *sftp.Request) (io.ReaderAt, error) {
	_, f, err := h.fs.Open(h.ctx, h.p, r.Filepath)
	if err != nil {
		return nil, toOS(err)
	}
	return f, nil
}

// Filewrite opens a file for writing. Writes go to a temp file (SFTP
// clients write out of order); the content is committed on close.
func (h *handler) Filewrite(r *sftp.Request) (io.WriterAt, error) {
	flags := r.Pflags()
	n, err := h.fs.Stat(h.ctx, h.p, r.Filepath)
	exists := err == nil
	if err != nil && core.KindOf(err) != core.ErrNotFound {
		return nil, toOS(err)
	}
	if exists && n.IsDir() {
		return nil, errors.New("is a directory")
	}
	if exists && flags.Excl {
		return nil, os.ErrExist
	}
	if !exists && !flags.Creat {
		return nil, os.ErrNotExist
	}
	tmp, err := h.fs.Svc.Blobs.TempFile("sftp-*")
	if err != nil {
		return nil, err
	}
	w := &writer{h: h, path: r.Filepath, f: tmp}
	if exists && !flags.Trunc {
		// in-place modification: start from the current content
		_, rd, err := h.fs.Open(h.ctx, h.p, r.Filepath)
		if err != nil {
			w.abort()
			return nil, toOS(err)
		}
		_, cerr := io.Copy(tmp, rd)
		rd.Close()
		if cerr != nil {
			w.abort()
			return nil, cerr
		}
	}
	return w, nil
}

type writer struct {
	h       *handler
	path    string
	f       *os.File
	mu      sync.Mutex
	done    bool
	aborted bool
}

// TransferError implements sftp.TransferError: an interrupted transfer must
// never be committed.
func (w *writer) TransferError(err error) {
	w.mu.Lock()
	w.aborted = true
	w.mu.Unlock()
}

func (w *writer) WriteAt(p []byte, off int64) (int, error) {
	return w.f.WriteAt(p, off)
}

func (w *writer) abort() {
	w.f.Close()
	os.Remove(w.f.Name())
}

func (w *writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.done {
		return nil
	}
	w.done = true
	if w.aborted {
		w.abort()
		return nil
	}
	if err := w.f.Close(); err != nil {
		os.Remove(w.f.Name())
		return err
	}
	st, err := w.h.fs.Svc.Blobs.StageFile(w.f.Name(), w.h.fs.Svc.Cfg.MaxUploadBytes)
	if err != nil {
		os.Remove(w.f.Name())
		if errors.Is(err, blob.ErrTooLarge) {
			return errors.New("file exceeds the upload limit")
		}
		return err
	}
	if _, err := w.h.fs.PutStaged(w.h.ctx, w.h.p, w.path, st, core.PutOptions{Protocol: "sftp"}); err != nil {
		w.h.log.Debug("sftp commit failed", "path", w.path, "err", err)
		return toOS(err)
	}
	return nil
}

// Filecmd handles Setstat, Rename, Rmdir, Mkdir, Remove, (Sym)link.
func (h *handler) Filecmd(r *sftp.Request) error {
	switch r.Method {
	case "Setstat":
		return nil // timestamps/permissions are not stored
	case "Rename":
		return toOS(h.fs.Rename(h.ctx, h.p, r.Filepath, r.Target, false))
	case "Rmdir":
		return toOS(h.fs.RemoveEmptyDir(h.ctx, h.p, r.Filepath))
	case "Mkdir":
		return toOS(h.fs.Mkdir(h.ctx, h.p, r.Filepath))
	case "Remove":
		n, err := h.fs.Stat(h.ctx, h.p, r.Filepath)
		if err != nil {
			return toOS(err)
		}
		if n.IsDir() {
			return errors.New("is a directory")
		}
		return toOS(h.fs.Remove(h.ctx, h.p, r.Filepath))
	case "Link", "Symlink":
		return sftp.ErrSSHFxOpUnsupported
	}
	return sftp.ErrSSHFxOpUnsupported
}

// PosixRename implements posix-rename@openssh.com (overwriting rename).
func (h *handler) PosixRename(r *sftp.Request) error {
	return toOS(h.fs.Rename(h.ctx, h.p, r.Filepath, r.Target, true))
}

// StatVFS reports a large free space (quotas are not implemented yet).
func (h *handler) StatVFS(r *sftp.Request) (*sftp.StatVFS, error) {
	return &sftp.StatVFS{Bsize: 4096, Frsize: 4096, Blocks: 1 << 40, Bfree: 1 << 39, Bavail: 1 << 39,
		Files: 1 << 30, Ffree: 1 << 29, Favail: 1 << 29, Namemax: 255}, nil
}

// Filelist handles List, Stat and Readlink.
func (h *handler) Filelist(r *sftp.Request) (sftp.ListerAt, error) {
	switch r.Method {
	case "List":
		nodes, err := h.fs.List(h.ctx, h.p, r.Filepath)
		if err != nil {
			return nil, toOS(err)
		}
		out := make(lister, len(nodes))
		for i := range nodes {
			out[i] = info{&nodes[i]}
		}
		return out, nil
	case "Stat":
		n, err := h.fs.Stat(h.ctx, h.p, r.Filepath)
		if err != nil {
			return nil, toOS(err)
		}
		return lister{info{n}}, nil
	}
	return nil, sftp.ErrSSHFxOpUnsupported
}

type lister []os.FileInfo

func (l lister) ListAt(dst []os.FileInfo, off int64) (int, error) {
	if off >= int64(len(l)) {
		return 0, io.EOF
	}
	n := copy(dst, l[off:])
	if n+int(off) >= len(l) {
		return n, io.EOF
	}
	return n, nil
}

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

// String helps debugging.
func (i info) String() string { return fmt.Sprintf("%s (%d)", i.n.Path, i.n.Size) }
