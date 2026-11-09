// Package testutil provides the integration test harness: a PostgreSQL
// container (Docker) shared by all test binaries and an isolated database per
// test, cloned from a migrated template.
//
// Set TILDOC_TEST_DATABASE_URL to use an existing server instead (must be a
// superuser connection to a maintenance database such as "postgres").
package testutil

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tildoc/tildoc/internal/blob"
	"github.com/tildoc/tildoc/internal/config"
	"github.com/tildoc/tildoc/internal/core"
	"github.com/tildoc/tildoc/internal/db"
)

const (
	containerName = "tildoc-test-pg"
	containerPort = "55432"
	pgImage       = "postgres:18-alpine"
)

var (
	setupOnce sync.Once
	setupErr  error
	adminURL  string
	tplName   string
)

// Env is a fully wired core service on a fresh database.
type Env struct {
	Pool    *pgxpool.Pool
	Blobs   *blob.Local
	Cfg     *config.Config
	Svc     *core.Service
	DataDir string
	DBURL   string
}

// New creates an isolated environment for one test. Skips the test if no
// database can be provided.
func New(t testing.TB) *Env {
	t.Helper()
	setupOnce.Do(func() { setupErr = setup() })
	if setupErr != nil {
		if os.Getenv("TILDOC_TEST_REQUIRE_DB") != "" {
			t.Fatalf("test database unavailable: %v", setupErr)
		}
		t.Skipf("test database unavailable: %v", setupErr)
	}
	ctx := context.Background()
	name := "t_" + randHex(8)
	admin, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		t.Fatal(err)
	}
	var cerr error
	for i := 0; i < 20; i++ {
		_, cerr = admin.Exec(ctx, fmt.Sprintf(`CREATE DATABASE %s TEMPLATE %s`, name, tplName))
		if cerr == nil || !strings.Contains(cerr.Error(), "being accessed") {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	admin.Close(ctx)
	if cerr != nil {
		t.Fatal(cerr)
	}
	dbURL := withDB(adminURL, name)
	pool, err := db.Open(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		if os.Getenv("TILDOC_TEST_KEEP_DB") != "" {
			return
		}
		a, err := pgx.Connect(context.Background(), adminURL)
		if err == nil {
			a.Exec(context.Background(), fmt.Sprintf(`DROP DATABASE IF EXISTS %s WITH (FORCE)`, name))
			a.Close(context.Background())
		}
	})
	dataDir := t.TempDir()
	blobs, err := blob.NewLocal(filepath.Join(dataDir, "store"))
	if err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	rand.Read(key)
	os.Setenv("TILDOC_DATABASE_URL", dbURL)
	os.Setenv("TILDOC_MASTER_KEY", base64.StdEncoding.EncodeToString(key))
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.DataDir = dataDir
	cfg.BackupDir = filepath.Join(dataDir, "backups") // never next to the sources
	cfg.CookieSecure = false
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if os.Getenv("TILDOC_TEST_LOG") != "" {
		log = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}
	return &Env{
		Pool: pool, Blobs: blobs, Cfg: cfg, DataDir: dataDir, DBURL: dbURL,
		Svc: core.New(pool, blobs, cfg, log),
	}
}

func setup() error {
	ctx := context.Background()
	adminURL = os.Getenv("TILDOC_TEST_DATABASE_URL")
	unlock, err := lock()
	if err != nil {
		return err
	}
	defer unlock()
	if adminURL == "" {
		if err := ensureContainer(); err != nil {
			return err
		}
		adminURL = "postgres://postgres:test@127.0.0.1:" + containerPort + "/postgres?sslmode=disable"
	}
	var conn *pgx.Conn
	deadline := time.Now().Add(60 * time.Second)
	for {
		conn, err = pgx.Connect(ctx, adminURL)
		if err == nil {
			if err = conn.Ping(ctx); err == nil {
				break
			}
			conn.Close(ctx)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("postgres not reachable: %w", err)
		}
		time.Sleep(300 * time.Millisecond)
	}
	defer conn.Close(ctx)

	tplName = "tildoc_tpl_" + db.MigrationsHash()
	var exists bool
	if err := conn.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname=$1)`, tplName).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return nil
	}
	tmp := tplName + "_build"
	conn.Exec(ctx, fmt.Sprintf(`DROP DATABASE IF EXISTS %s WITH (FORCE)`, tmp))
	if _, err := conn.Exec(ctx, fmt.Sprintf(`CREATE DATABASE %s`, tmp)); err != nil {
		return err
	}
	pool, err := db.Open(ctx, withDB(adminURL, tmp))
	if err != nil {
		return err
	}
	err = db.Migrate(ctx, pool)
	pool.Close()
	if err != nil {
		return err
	}
	_, err = conn.Exec(ctx, fmt.Sprintf(`ALTER DATABASE %s RENAME TO %s`, tmp, tplName))
	return err
}

func ensureContainer() error {
	if _, err := exec.LookPath("docker"); err != nil {
		return fmt.Errorf("docker not found: %w", err)
	}
	out, err := exec.Command("docker", "inspect", "-f", "{{.State.Running}}", containerName).Output()
	if err == nil && strings.TrimSpace(string(out)) == "true" {
		return nil
	}
	exec.Command("docker", "rm", "-f", containerName).Run()
	cmd := exec.Command("docker", "run", "-d", "--rm", "--name", containerName,
		"-p", "127.0.0.1:"+containerPort+":5432",
		"-e", "POSTGRES_PASSWORD=test",
		"--tmpfs", "/var/lib/postgresql:rw",
		pgImage, "-c", "fsync=off", "-c", "synchronous_commit=off", "-c", "full_page_writes=off", "-c", "max_connections=300")
	if b, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("docker run: %v: %s", err, b)
	}
	return nil
}

// lock serializes harness setup across concurrently running test binaries.
func lock() (func(), error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = os.TempDir()
	}
	dir = filepath.Join(dir, "tildoc")
	os.MkdirAll(dir, 0o700)
	f, err := os.OpenFile(filepath.Join(dir, "test-setup.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() { syscall.Flock(int(f.Fd()), syscall.LOCK_UN); f.Close() }, nil
}

func withDB(raw, name string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	u.Path = "/" + name
	return u.String()
}

func randHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}
