package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/tildoc/tildoc/internal/db"
)

// Space is a top-level container.
type Space struct {
	ID           string    `json:"id"`
	Slug         string    `json:"slug"`
	Name         string    `json:"name"`
	OwnerUserID  *string   `json:"owner_user_id"`
	OwnerTeamID  *string   `json:"owner_team_id"`
	TeamName     string    `json:"team_name,omitempty"`
	RootFolderID string    `json:"root_folder_id"`
	Perm         Perm      `json:"perm"`
	Role         string    `json:"role"`
	CreatedAt    time.Time `json:"created_at"`
}

func permRole(p Perm) string {
	switch p {
	case PermManage:
		return "owner"
	case PermDelete:
		return "admin"
	case PermWrite:
		return "member"
	case PermRead:
		return "viewer"
	}
	return ""
}

const spaceSelect = `
	SELECT s.id::text, s.slug, s.name, s.owner_user_id::text, s.owner_team_id::text, coalesce(t.name,''),
	       f.id::text, s.created_at,
	       CASE WHEN s.owner_user_id = $1 THEN 'owner' ELSE coalesce(tm.role,'') END
	FROM spaces s
	JOIN folders f ON f.space_id = s.id AND f.parent_id IS NULL
	LEFT JOIN teams t ON t.id = s.owner_team_id
	LEFT JOIN team_members tm ON tm.team_id = s.owner_team_id AND tm.user_id = $1`

func scanSpace(row pgx.Row, p *Principal) (*Space, error) {
	sp := &Space{}
	var role string
	err := row.Scan(&sp.ID, &sp.Slug, &sp.Name, &sp.OwnerUserID, &sp.OwnerTeamID, &sp.TeamName, &sp.RootFolderID, &sp.CreatedAt, &role)
	if err != nil {
		return nil, err
	}
	sp.Perm = roleToPerm(role)
	if p.Via == "system" {
		sp.Perm = PermManage
	}
	if p.ScopeSpaceID != "" && p.ScopeSpaceID != sp.ID {
		sp.Perm = PermNone
	}
	if p.ReadOnly && sp.Perm > PermRead {
		sp.Perm = PermRead
	}
	sp.Role = permRole(sp.Perm)
	return sp, nil
}

// ListSpaces lists spaces the caller can read.
func (s *Service) ListSpaces(ctx context.Context, p *Principal) ([]Space, error) {
	rows, err := s.DB.Query(ctx, spaceSelect+` WHERE s.owner_user_id = $1 OR tm.user_id IS NOT NULL ORDER BY s.slug`, p.UserID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Space{}
	for rows.Next() {
		sp, err := scanSpace(rows, p)
		if err != nil {
			return nil, err
		}
		if sp.Perm >= PermRead {
			out = append(out, *sp)
		}
	}
	return out, rows.Err()
}

// GetSpace returns a space by id or slug.
func (s *Service) GetSpace(ctx context.Context, p *Principal, idOrSlug string) (*Space, error) {
	where := ` WHERE s.slug = $2`
	if looksLikeUUID(idOrSlug) {
		where = ` WHERE s.id = $2::uuid`
	}
	sp, err := scanSpace(s.DB.QueryRow(ctx, spaceSelect+where, p.UserID, idOrSlug), p)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && sp.Perm == PermNone) {
		return nil, errf(ErrNotFound, "space not found")
	}
	return sp, err
}

func looksLikeUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
				return false
			}
		}
	}
	return true
}

// CreateSpace creates a personal space (teamID empty) or a team space.
func (s *Service) CreateSpace(ctx context.Context, p *Principal, slug, name, teamID string) (*Space, error) {
	if err := requireSession(p); err != nil {
		return nil, err
	}
	slug = strings.ToLower(strings.TrimSpace(slug))
	name = strings.TrimSpace(name)
	if slug == "" {
		slug = slugify(name)
	}
	if err := ValidateSlug(slug); err != nil {
		return nil, err
	}
	if name == "" {
		name = slug
	}
	var id string
	err := s.tx(ctx, func(tx pgx.Tx) error {
		owner := p.UserID
		if teamID != "" {
			role, err := s.teamRole(ctx, tx, p, teamID)
			if err != nil {
				return err
			}
			if role != "owner" && role != "admin" {
				return errf(ErrForbidden, "team admin required")
			}
			owner = ""
		}
		var err error
		id, err = s.createSpaceTx(ctx, tx, slug, name, owner, teamID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.GetSpace(ctx, p, id)
}

func (s *Service) createSpaceTx(ctx context.Context, tx pgx.Tx, slug, name, ownerUser, ownerTeam string) (string, error) {
	var id string
	err := tx.QueryRow(ctx, `INSERT INTO spaces(slug, name, owner_user_id, owner_team_id)
		VALUES($1,$2,nullif($3,'')::uuid,nullif($4,'')::uuid) RETURNING id::text`, slug, name, ownerUser, ownerTeam).Scan(&id)
	if db.IsUniqueViolation(err) {
		return "", errf(ErrConflict, "slug %q already taken", slug)
	}
	if err != nil {
		return "", err
	}
	_, err = tx.Exec(ctx, `INSERT INTO folders(space_id, parent_id, name) VALUES($1, NULL, '')`, id)
	return id, err
}

func (s *Service) uniqueSlug(ctx context.Context, tx pgx.Tx, base string) string {
	base = slugify(base)
	if reservedSlugs[base] {
		base += "-space"
	}
	cand := base
	for i := 2; ; i++ {
		var exists bool
		tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM spaces WHERE slug=$1)`, cand).Scan(&exists)
		if !exists && ValidateSlug(cand) == nil {
			return cand
		}
		cand = fmt.Sprintf("%s-%d", base, i)
	}
}

// UpdateSpace renames a space and/or changes its slug (manage permission;
// team spaces: team admin).
func (s *Service) UpdateSpace(ctx context.Context, p *Principal, id string, name, slug *string) (*Space, error) {
	if err := requireSession(p); err != nil {
		return nil, err
	}
	sp, err := s.GetSpace(ctx, p, id)
	if err != nil {
		return nil, err
	}
	if sp.Perm < PermDelete {
		return nil, errf(ErrForbidden, "insufficient permission")
	}
	if name != nil {
		n := strings.TrimSpace(*name)
		if n == "" {
			return nil, errf(ErrInvalid, "name required")
		}
		if _, err := s.DB.Exec(ctx, `UPDATE spaces SET name=$2 WHERE id=$1`, sp.ID, n); err != nil {
			return nil, err
		}
	}
	if slug != nil {
		sl := strings.ToLower(strings.TrimSpace(*slug))
		if err := ValidateSlug(sl); err != nil {
			return nil, err
		}
		_, err := s.DB.Exec(ctx, `UPDATE spaces SET slug=$2 WHERE id=$1`, sp.ID, sl)
		if db.IsUniqueViolation(err) {
			return nil, errf(ErrConflict, "slug %q already taken", sl)
		}
		if err != nil {
			return nil, err
		}
	}
	return s.GetSpace(ctx, p, sp.ID)
}

// DeleteSpace deletes an empty space (no live documents).
func (s *Service) DeleteSpace(ctx context.Context, p *Principal, id string) error {
	if err := requireSession(p); err != nil {
		return err
	}
	sp, err := s.GetSpace(ctx, p, id)
	if err != nil {
		return err
	}
	if sp.Perm < PermManage && !(sp.OwnerTeamID != nil && sp.Perm >= PermDelete) {
		return errf(ErrForbidden, "insufficient permission")
	}
	return s.tx(ctx, func(tx pgx.Tx) error {
		var live bool
		tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM documents WHERE space_id=$1 AND deleted_at IS NULL)`, sp.ID).Scan(&live)
		if live {
			return errf(ErrNotEmpty, "space still contains documents")
		}
		var n int
		tx.QueryRow(ctx, `SELECT count(*) FROM spaces WHERE owner_user_id=$1`, p.UserID).Scan(&n)
		if sp.OwnerUserID != nil && n <= 1 {
			return errf(ErrInvalid, "cannot delete your last personal space")
		}
		_, err := tx.Exec(ctx, `DELETE FROM spaces WHERE id=$1`, sp.ID)
		return err
	})
}
