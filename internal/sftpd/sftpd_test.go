package sftpd_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"github.com/tildoc/tildoc/internal/sftpd"
	"github.com/tildoc/tildoc/internal/testutil"
	"github.com/tildoc/tildoc/internal/vfs"
)

func TestSFTP(t *testing.T) {
	env := testutil.New(t)
	ctx := context.Background()
	svc := env.Svc
	svc.SetupAdmin(ctx, "alice", "alicepass", "")
	p, _ := svc.Authenticate(ctx, "alice", "alicepass")
	p.Via = "session"
	_, secret, _ := svc.CreateAccessKey(ctx, p, "sftp", "", false)
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	spub, _ := ssh.NewPublicKey(pub)
	if _, err := svc.AddSSHKey(ctx, p, string(ssh.MarshalAuthorizedKey(spub)), "test"); err != nil {
		t.Fatal(err)
	}

	hostKey, err := sftpd.LoadOrCreateHostKey(filepath.Join(env.DataDir, "ssh_host_ed25519_key"))
	if err != nil {
		t.Fatal(err)
	}
	again, _ := sftpd.LoadOrCreateHostKey(filepath.Join(env.DataDir, "ssh_host_ed25519_key"))
	if string(again.PublicKey().Marshal()) != string(hostKey.PublicKey().Marshal()) {
		t.Fatal("host key not persisted")
	}
	srv := sftpd.New(vfs.New(svc), slog.New(slog.NewTextHandler(io.Discard, nil)), hostKey)
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	go srv.Serve(l)
	defer srv.Close()

	dial := func(user string, am ssh.AuthMethod) (*sftp.Client, error) {
		conn, err := ssh.Dial("tcp", l.Addr().String(), &ssh.ClientConfig{User: user, Auth: []ssh.AuthMethod{am},
			HostKeyCallback: ssh.FixedHostKey(hostKey.PublicKey())})
		if err != nil {
			return nil, err
		}
		return sftp.NewClient(conn)
	}
	if _, err := dial("alice", ssh.Password("alicepass")); err == nil {
		t.Fatal("account password must not work for sftp")
	}
	signer, _ := ssh.NewSignerFromKey(priv)
	c, err := dial("alice", ssh.PublicKeys(signer))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	cpw, err := dial("alice", ssh.Password(secret))
	if err != nil {
		t.Fatal(err)
	}
	cpw.Close()

	ls, err := c.ReadDir("/")
	if err != nil || len(ls) != 1 || ls[0].Name() != "alice" || !ls[0].IsDir() {
		t.Fatalf("root: %v %v", ls, err)
	}
	if err := c.Mkdir("/alice/Docs"); err != nil {
		t.Fatal(err)
	}
	if err := c.MkdirAll("/alice/a/b/c"); err != nil {
		t.Fatal(err)
	}
	// write with out-of-order chunks (pkg/sftp client uses concurrent writes)
	data := strings.Repeat("sftp-data-", 100000)
	f, err := c.Create("/alice/Docs/big.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.ReadFrom(strings.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	rf, err := c.Open("/alice/Docs/big.txt")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(rf)
	rf.Close()
	if string(got) != data {
		t.Fatalf("content mismatch: %d vs %d", len(got), len(data))
	}
	st, err := c.Stat("/alice/Docs/big.txt")
	if err != nil || st.Size() != int64(len(data)) {
		t.Fatalf("stat: %v %v", st, err)
	}

	// KIO style: write .part, posix-rename over existing -> new version
	w, _ := c.Create("/alice/Docs/big.txt.part")
	w.Write([]byte("replaced"))
	w.Close()
	if err := c.PosixRename("/alice/Docs/big.txt.part", "/alice/Docs/big.txt"); err != nil {
		t.Fatal(err)
	}
	sp, _ := svc.GetSpace(ctx, p, "alice")
	fi, _ := svc.FileByPath(ctx, p, sp.ID, []string{"Docs", "big.txt"})
	if fi.Version != 2 {
		t.Fatalf("version after posix-rename: %d", fi.Version)
	}
	// plain rename onto existing fails (SFTPv3 semantics)
	w, _ = c.Create("/alice/Docs/other.txt")
	w.Write([]byte("o"))
	w.Close()
	if err := c.Rename("/alice/Docs/other.txt", "/alice/Docs/big.txt"); err == nil {
		t.Fatal("rename over existing must fail")
	}
	if err := c.Rename("/alice/Docs/other.txt", "/alice/a/moved.txt"); err != nil {
		t.Fatal(err)
	}
	// append-style open keeps existing content
	af, err := c.OpenFile("/alice/a/moved.txt", 0x2|0x8) // O_WRONLY | O_APPEND (sftp flags)
	if err == nil {
		af.Seek(1, io.SeekStart)
		af.Write([]byte("k"))
		af.Close()
		rd, _ := c.Open("/alice/a/moved.txt")
		b, _ := io.ReadAll(rd)
		rd.Close()
		if string(b) != "ok" {
			t.Fatalf("in-place write: %q", b)
		}
	}

	names := func(dir string) string {
		ls, _ := c.ReadDir(dir)
		var n []string
		for _, e := range ls {
			n = append(n, e.Name())
		}
		sort.Strings(n)
		return strings.Join(n, ",")
	}
	if got := names("/alice/a"); got != "b,moved.txt" {
		t.Fatalf("list: %s", got)
	}
	if err := c.RemoveDirectory("/alice/a"); err == nil {
		t.Fatal("rmdir non-empty must fail")
	}
	if err := c.Remove("/alice/a/moved.txt"); err != nil {
		t.Fatal(err)
	}
	if err := c.RemoveDirectory("/alice/a/b/c"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Stat("/alice/a/b/c"); err == nil {
		t.Fatal("dir still exists")
	}
	if _, err := c.Stat("/alice/nope"); err == nil {
		t.Fatal("expected not exist")
	}
	if err := c.Mkdir("/newspace"); err == nil {
		t.Fatal("mkdir at root must fail")
	}
}
