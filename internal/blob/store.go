// Package blob implements the content-addressed, immutable blob store.
//
// Layout below the root directory:
//
//	blobs/ab/cd/<sha256-hex>   committed, read-only blobs
//	tmp/                       staging area (same filesystem → atomic rename)
package blob

import (
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// HeadSize is the number of leading bytes captured for content sniffing.
const HeadSize = 1024

// ErrTooLarge is returned when a stream exceeds the configured maximum size.
var ErrTooLarge = errors.New("blob: size limit exceeded")

// ErrNotFound is returned for unknown blobs.
var ErrNotFound = errors.New("blob: not found")

// SHA256 is a blob address.
type SHA256 [32]byte

// Hex returns the lowercase hex form.
func (s SHA256) Hex() string { return hex.EncodeToString(s[:]) }

// ParseSHA256 parses a hex encoded hash.
func ParseSHA256(h string) (SHA256, error) {
	var s SHA256
	b, err := hex.DecodeString(h)
	if err != nil || len(b) != 32 {
		return s, fmt.Errorf("invalid sha256 %q", h)
	}
	copy(s[:], b)
	return s, nil
}

// Staged is a fully written but not yet committed blob.
type Staged struct {
	path   string
	SHA256 SHA256
	MD5    [16]byte
	Size   int64
	Head   []byte
}

// File is an open committed blob.
type File interface {
	io.ReadSeekCloser
	io.ReaderAt
	Stat() (fs.FileInfo, error)
}

// Store is the blob storage abstraction. A future S3 backend implements the
// same interface.
type Store interface {
	// Stage streams r into a temporary file while hashing it.
	Stage(r io.Reader, maxSize int64) (*Staged, error)
	// StageFile adopts an existing temp file (created via TempFile) as staged blob.
	StageFile(path string, maxSize int64) (*Staged, error)
	// Commit moves a staged blob into place. If the blob exists already the
	// staged copy is discarded. Returns whether the blob was newly written.
	Commit(st *Staged) (bool, error)
	// Discard removes a staged blob.
	Discard(st *Staged)
	Open(sha SHA256) (File, error)
	Exists(sha SHA256) bool
	Delete(sha SHA256) error
	// LocalPath returns a local filesystem path for processors.
	LocalPath(sha SHA256) (string, error)
	// TempFile creates a file in the staging area.
	TempFile(pattern string) (*os.File, error)
	// TempDir returns the staging directory.
	TempDir() string
	// Walk lists all committed blobs with their modification time.
	Walk(fn func(sha SHA256, mod time.Time) error) error
}

// Local is a filesystem backed Store.
type Local struct {
	root string
}

// NewLocal creates (if needed) and opens a local blob store.
func NewLocal(root string) (*Local, error) {
	for _, d := range []string{"blobs", "tmp"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o750); err != nil {
			return nil, err
		}
	}
	return &Local{root: root}, nil
}

func (l *Local) blobPath(sha SHA256) string {
	h := sha.Hex()
	return filepath.Join(l.root, "blobs", h[0:2], h[2:4], h)
}

// TempDir implements Store.
func (l *Local) TempDir() string { return filepath.Join(l.root, "tmp") }

// TempFile implements Store.
func (l *Local) TempFile(pattern string) (*os.File, error) {
	return os.CreateTemp(l.TempDir(), pattern)
}

type limitedWriter struct {
	w   io.Writer
	n   int64
	max int64
}

func (lw *limitedWriter) Write(p []byte) (int, error) {
	if lw.max > 0 && lw.n+int64(len(p)) > lw.max {
		return 0, ErrTooLarge
	}
	n, err := lw.w.Write(p)
	lw.n += int64(n)
	return n, err
}

type headCapture struct{ buf []byte }

func (h *headCapture) Write(p []byte) (int, error) {
	if rem := HeadSize - len(h.buf); rem > 0 {
		if len(p) < rem {
			rem = len(p)
		}
		h.buf = append(h.buf, p[:rem]...)
	}
	return len(p), nil
}

// Stage implements Store.
func (l *Local) Stage(r io.Reader, maxSize int64) (*Staged, error) {
	f, err := l.TempFile("stage-*")
	if err != nil {
		return nil, err
	}
	sh, mh, head := sha256.New(), md5.New(), &headCapture{}
	lw := &limitedWriter{w: io.MultiWriter(f, sh, mh, head), max: maxSize}
	_, cerr := io.Copy(lw, r)
	serr := f.Sync()
	clerr := f.Close()
	if err := errors.Join(cerr, serr, clerr); err != nil {
		os.Remove(f.Name())
		return nil, err
	}
	return finish(f.Name(), lw.n, sh, mh, head.buf), nil
}

// StageFile implements Store.
func (l *Local) StageFile(path string, maxSize int64) (*Staged, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sh, mh, head := sha256.New(), md5.New(), &headCapture{}
	lw := &limitedWriter{w: io.MultiWriter(sh, mh, head), max: maxSize}
	if _, err := io.Copy(lw, f); err != nil {
		return nil, err
	}
	return finish(path, lw.n, sh, mh, head.buf), nil
}

func finish(path string, n int64, sh, mh hash.Hash, head []byte) *Staged {
	st := &Staged{path: path, Size: n, Head: head}
	copy(st.SHA256[:], sh.Sum(nil))
	copy(st.MD5[:], mh.Sum(nil))
	return st
}

// Commit implements Store.
func (l *Local) Commit(st *Staged) (bool, error) {
	dst := l.blobPath(st.SHA256)
	if _, err := os.Stat(dst); err == nil {
		os.Remove(st.path)
		// refresh mtime so the orphan sweeper does not race with this commit
		now := time.Now()
		os.Chtimes(dst, now, now)
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return false, err
	}
	if err := os.Chmod(st.path, 0o440); err != nil {
		return false, err
	}
	if err := os.Rename(st.path, dst); err != nil {
		return false, err
	}
	return true, nil
}

// Discard implements Store.
func (l *Local) Discard(st *Staged) {
	if st != nil {
		os.Remove(st.path)
	}
}

// Open implements Store.
func (l *Local) Open(sha SHA256) (File, error) {
	f, err := os.Open(l.blobPath(sha))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNotFound
	}
	return f, err
}

// Exists implements Store.
func (l *Local) Exists(sha SHA256) bool {
	_, err := os.Stat(l.blobPath(sha))
	return err == nil
}

// Delete implements Store.
func (l *Local) Delete(sha SHA256) error {
	err := os.Remove(l.blobPath(sha))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// LocalPath implements Store.
func (l *Local) LocalPath(sha SHA256) (string, error) {
	p := l.blobPath(sha)
	if _, err := os.Stat(p); err != nil {
		return "", ErrNotFound
	}
	return p, nil
}

// Walk implements Store.
func (l *Local) Walk(fn func(sha SHA256, mod time.Time) error) error {
	return filepath.WalkDir(filepath.Join(l.root, "blobs"), func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		sha, perr := ParseSHA256(d.Name())
		if perr != nil {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return nil
		}
		return fn(sha, info.ModTime())
	})
}

// MarshalText encodes the hash as hex (JSON friendly).
func (s SHA256) MarshalText() ([]byte, error) { return []byte(s.Hex()), nil }

// UnmarshalText decodes a hex hash.
func (s *SHA256) UnmarshalText(b []byte) error {
	v, err := ParseSHA256(string(b))
	if err != nil {
		return err
	}
	*s = v
	return nil
}
