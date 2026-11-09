// Package core contains tildoc's domain logic. All protocol adapters (REST,
// S3, WebDAV, SFTP) use this package; none of them access the database or
// the blob store directly.
package core

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tildoc/tildoc/internal/blob"
	"github.com/tildoc/tildoc/internal/config"
	"github.com/tildoc/tildoc/internal/db"
)

// Service is the core domain service.
type Service struct {
	DB    *pgxpool.Pool
	Blobs blob.Store
	Cfg   *config.Config
	Log   *slog.Logger
	now   func() time.Time
}

// New creates a Service.
func New(pool *pgxpool.Pool, blobs blob.Store, cfg *config.Config, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{DB: pool, Blobs: blobs, Cfg: cfg, Log: log, now: time.Now}
}

func (s *Service) tx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	return db.InTx(ctx, s.DB, fn)
}

// Principal is an authenticated caller.
type Principal struct {
	UserID   string
	Username string
	Inst     InstCap // instance permissions
	// Restrictions coming from an access key.
	ScopeSpaceID string
	ReadOnly     bool
	Via          string // session | accesskey | ssh | system
	KeyID        string
	KeyCaps      Cap      // Via apikey: at most these
	KeySpaces    []string // Via apikey: only these spaces, unless KeyAllSpaces
	KeyAllSpaces bool
	KeyFolder    *string // Via apikey: Drop-In target
	SessionHash  []byte  // Via session: the login session
	IdentityID   string  // how the user logged in
}

// SystemPrincipal is used by background jobs.
var SystemPrincipal = &Principal{UserID: "00000000-0000-0000-0000-000000000000", Username: "system", Via: "system"}

// Permission levels on a space.
type Perm int

// Permission levels.
const (
	PermNone   Perm = 0
	PermRead   Perm = 1
	PermWrite  Perm = 2
	PermDelete Perm = 3
	PermManage Perm = 4
)

// requirePerm checks that the caller has at least want on the space. Spaces
// without read access are reported as not found (no existence leak).
func (s *Service) requirePerm(ctx context.Context, q db.Querier, p *Principal, spaceID string, want Perm) error {
	return s.requireCap(ctx, q, p, spaceID, permCap(want))
}
