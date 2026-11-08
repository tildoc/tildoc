package db_test

import (
	"context"
	"testing"

	"github.com/tildoc/tildoc/internal/db"
	"github.com/tildoc/tildoc/internal/testutil"
)

func TestMigrateIdempotent(t *testing.T) {
	env := testutil.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, env.Pool); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := env.Pool.QueryRow(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&n); err != nil || n < 1 {
		t.Fatalf("migrations: %d %v", n, err)
	}
	for _, tbl := range []string{"users", "spaces", "entries", "documents", "document_versions", "blobs", "tags", "collections", "jobs"} {
		if _, err := env.Pool.Exec(ctx, "SELECT 1 FROM "+tbl+" LIMIT 1"); err != nil {
			t.Fatalf("table %s: %v", tbl, err)
		}
	}
}
