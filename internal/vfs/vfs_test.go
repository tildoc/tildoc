package vfs_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/tildoc/tildoc/internal/core"
	"github.com/tildoc/tildoc/internal/testutil"
	"github.com/tildoc/tildoc/internal/vfs"
)

func TestVFS(t *testing.T) {
	env := testutil.New(t)
	ctx := context.Background()
	svc := env.Svc
	if _, err := svc.SetupAdmin(ctx, "alice", "alicepass", ""); err != nil {
		t.Fatal(err)
	}
	p, _ := svc.Authenticate(ctx, "alice", "alicepass")
	p.Via = "session"
	fs := vfs.New(svc)
	opt := core.PutOptions{Protocol: "api"}

	root, err := fs.List(ctx, p, "/")
	if err != nil || len(root) != 1 || root[0].Name != "alice" {
		t.Fatalf("root: %+v %v", root, err)
	}
	if err := fs.Mkdir(ctx, p, "/newspace"); !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("mkdir at root: %v", err)
	}
	if err := fs.Mkdir(ctx, p, "/alice/docs"); err != nil {
		t.Fatal(err)
	}
	if err := fs.Mkdir(ctx, p, "/alice/x/y"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("mkdir without parent: %v", err)
	}
	if err := fs.MkdirAll(ctx, p, "/alice/x/y"); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.Put(ctx, p, "/alice/docs/a.txt", strings.NewReader("hello"), opt); err != nil {
		t.Fatal(err)
	}
	n, f, err := fs.Open(ctx, p, "/alice/docs/a.txt")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(f)
	f.Close()
	if string(b) != "hello" || n.Size != 5 || n.Kind != vfs.KindFile {
		t.Fatalf("open: %q %+v", b, n)
	}
	ls, _ := fs.List(ctx, p, "/alice")
	if len(ls) != 2 {
		t.Fatalf("list: %+v", ls)
	}

	// rename file, copy, rename folder
	if err := fs.Rename(ctx, p, "/alice/docs/a.txt", "/alice/docs/b.txt", false); err != nil {
		t.Fatal(err)
	}
	if err := fs.Copy(ctx, p, "/alice/docs/b.txt", "/alice/x/c.txt", false); err != nil {
		t.Fatal(err)
	}
	if err := fs.Rename(ctx, p, "/alice/docs", "/alice/x/y/docs", false); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.Stat(ctx, p, "/alice/x/y/docs/b.txt"); err != nil {
		t.Fatal(err)
	}
	// overwrite a file via rename keeps destination history
	fs.Put(ctx, p, "/alice/x/new.txt", strings.NewReader("new content"), opt)
	if err := fs.Rename(ctx, p, "/alice/x/new.txt", "/alice/x/c.txt", false); !errors.Is(err, core.ErrConflict) {
		t.Fatalf("rename without overwrite: %v", err)
	}
	if err := fs.Rename(ctx, p, "/alice/x/new.txt", "/alice/x/c.txt", true); err != nil {
		t.Fatal(err)
	}
	c, _ := fs.Stat(ctx, p, "/alice/x/c.txt")
	if c.File.Version != 2 || c.Size != 11 {
		t.Fatalf("overwrite: %+v", c.File)
	}

	// recursive key listing (S3)
	space, _ := svc.GetSpace(ctx, p, "alice")
	fs.MkdirAll(ctx, p, "/alice/empty")
	keys, err := svc.ListKeys(ctx, p, space.ID, space.RootFolderID, "", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, k := range keys {
		got = append(got, k.Key)
	}
	want := "empty/,x/c.txt,x/y/docs/b.txt"
	if strings.Join(got, ",") != want {
		t.Fatalf("keys: %v", got)
	}
	keys, _ = svc.ListKeys(ctx, p, space.ID, space.RootFolderID, "x/", "x/c.txt", 100)
	if len(keys) != 1 || keys[0].Key != "x/y/docs/b.txt" {
		t.Fatalf("keys after: %+v", keys)
	}

	// remove
	if err := fs.RemoveEmptyDir(ctx, p, "/alice/x"); !errors.Is(err, core.ErrNotEmpty) {
		t.Fatalf("rmdir non-empty: %v", err)
	}
	if err := fs.Remove(ctx, p, "/alice/x"); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.Stat(ctx, p, "/alice/x/c.txt"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("stat after remove: %v", err)
	}
	if err := fs.Remove(ctx, p, "/alice"); !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("remove space: %v", err)
	}
}
