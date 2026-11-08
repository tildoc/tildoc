package blob

import (
	"bytes"
	"crypto/md5"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

func TestStageCommitOpen(t *testing.T) {
	s, err := NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	data := []byte(strings.Repeat("hello tildoc ", 1000))
	st, err := s.Stage(bytes.NewReader(data), 0)
	if err != nil {
		t.Fatal(err)
	}
	if st.SHA256 != sha256.Sum256(data) || st.MD5 != md5.Sum(data) || st.Size != int64(len(data)) {
		t.Fatal("hash/size mismatch")
	}
	if len(st.Head) != HeadSize || !bytes.Equal(st.Head, data[:HeadSize]) {
		t.Fatal("head mismatch")
	}
	created, err := s.Commit(st)
	if err != nil || !created {
		t.Fatalf("commit: %v %v", created, err)
	}
	// second commit of the same content is deduplicated
	st2, _ := s.Stage(bytes.NewReader(data), 0)
	created, err = s.Commit(st2)
	if err != nil || created {
		t.Fatalf("second commit: %v %v", created, err)
	}
	if _, err := os.Stat(st2.path); !os.IsNotExist(err) {
		t.Fatal("staged duplicate not removed")
	}
	f, err := s.Open(st.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	got, _ := io.ReadAll(f)
	if !bytes.Equal(got, data) {
		t.Fatal("content mismatch")
	}
	buf := make([]byte, 5)
	if _, err := f.ReadAt(buf, 6); err != nil || string(buf) != "docto" {
		t.Fatalf("ReadAt: %q %v", buf, err)
	}
	n := 0
	s.Walk(func(sha SHA256, _ time.Time) error { n++; return nil })
	if n != 1 {
		t.Fatalf("walk: %d", n)
	}
	if err := s.Delete(st.SHA256); err != nil || s.Exists(st.SHA256) {
		t.Fatal("delete failed")
	}
	if _, err := s.Open(st.SHA256); !errors.Is(err, ErrNotFound) {
		t.Fatal("expected not found")
	}
}

func TestStageLimit(t *testing.T) {
	s, _ := NewLocal(t.TempDir())
	_, err := s.Stage(bytes.NewReader(make([]byte, 100)), 99)
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("expected ErrTooLarge, got %v", err)
	}
	entries, _ := os.ReadDir(s.TempDir())
	if len(entries) != 0 {
		t.Fatal("temp file left behind")
	}
}

func TestStageFile(t *testing.T) {
	s, _ := NewLocal(t.TempDir())
	f, _ := s.TempFile("x-*")
	f.WriteString("abc")
	f.Close()
	st, err := s.StageFile(f.Name(), 0)
	if err != nil || st.SHA256 != sha256.Sum256([]byte("abc")) {
		t.Fatal(err)
	}
	if _, err := s.Commit(st); err != nil || !s.Exists(st.SHA256) {
		t.Fatal(err)
	}
}
