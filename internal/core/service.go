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
	IsAdmin  bool
	// Restrictions coming from an access key.
	ScopeSpaceID string
	ReadOnly     bool
	Via          string // session | accesskey | ssh | system
	KeyID        string
}

// SystemPrincipal is used by background jobs.
var SystemPrincipal = &Principal{UserID: "", Username: "system", Via: "system"}

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

func roleToPerm(role string) Perm {
	switch role {
	case "owner":
		return PermManage
	case "admin":
		return PermDelete
	case "member":
		return PermWrite
	case "viewer":
		return PermRead
	}
	return PermNone
}

// spacePerm returns the caller's permission on a space.
func (s *Service) spacePerm(ctx context.Context, q db.Querier, p *Principal, spaceID string) (Perm, error) {
	if p == nil {
		return PermNone, ErrUnauthorized
	}
	if p.Via == "system" {
		return PermManage, nil
	}
	var ownerUser, role *string
	err := q.QueryRow(ctx, `
		SELECT s.owner_user_id::text, tm.role
		FROM spaces s
		LEFT JOIN team_members tm ON tm.team_id = s.owner_team_id AND tm.user_id = $2
		WHERE s.id = $1`, spaceID, p.UserID).Scan(&ownerUser, &role)
	if err == pgx.ErrNoRows {
		return PermNone, errf(ErrNotFound, "space not found")
	}
	if err != nil {
		return PermNone, err
	}
	perm := PermNone
	if ownerUser != nil && *ownerUser == p.UserID {
		perm = PermManage
	} else if role != nil {
		perm = roleToPerm(*role)
	}
	if p.ScopeSpaceID != "" && p.ScopeSpaceID != spaceID {
		perm = PermNone
	}
	if p.ReadOnly && perm > PermRead {
		perm = PermRead
	}
	return perm, nil
}

// requirePerm checks that the caller has at least want on the space. Spaces
// without read access are reported as not found (no existence leak).
func (s *Service) requirePerm(ctx context.Context, q db.Querier, p *Principal, spaceID string, want Perm) error {
	got, err := s.spacePerm(ctx, q, p, spaceID)
	if err != nil {
		return err
	}
	if got == PermNone {
		return errf(ErrNotFound, "space not found")
	}
	if got < want {
		return errf(ErrForbidden, "insufficient permission on space")
	}
	return nil
}
