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
	Perm         Perm      `json:"perm"` // highest level, for simple UI checks
	Role         string    `json:"role"`
	Caps         []string  `json:"caps"`
	Shared       bool      `json:"shared"` // access comes from a grant, not ownership/team
	CreatedAt    time.Time `json:"created_at"`
}

const spaceSelect = `
	SELECT s.id::text, s.slug, s.name, s.owner_user_id::text, s.owner_team_id::text, coalesce(t.name,''),
	       f.id::text, s.created_at, space_permissions(s.id, $1::uuid),
	       NOT (s.owner_user_id IS NOT DISTINCT FROM $1::uuid
	            OR EXISTS (SELECT 1 FROM team_members m WHERE m.team_id = s.owner_team_id AND m.user_id = $1::uuid))
	FROM spaces s
	JOIN entries f ON f.space_id = s.id AND f.parent_id IS NULL AND f.kind = 'folder'
	LEFT JOIN teams t ON t.id = s.owner_team_id`

func scanSpace(row pgx.Row, p *Principal) (*Space, error) {
	sp := &Space{}
	var names []string
	err := row.Scan(&sp.ID, &sp.Slug, &sp.Name, &sp.OwnerUserID, &sp.OwnerTeamID, &sp.TeamName, &sp.RootFolderID, &sp.CreatedAt, &names, &sp.Shared)
	if err != nil {
		return nil, err
	}
	caps := effectiveCaps(p, sp.ID, capsFromNames(names))
	sp.Perm, sp.Caps = capsPerm(caps), caps.Names()
	sp.Role = roleLabel(caps)
	return sp, nil
}

// ListSpaces lists spaces the caller can read.
func (s *Service) ListSpaces(ctx context.Context, p *Principal) ([]Space, error) {
	rows, err := s.DB.Query(ctx, spaceSelect+` WHERE cardinality(space_permissions(s.id, $1::uuid)) > 0 ORDER BY s.slug`, p.UserID)
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
	if teamID == "" && !p.Can(InstSpacesCreate) {
		return nil, errf(ErrForbidden, "not allowed to create spaces")
	}
	var id string
	err := s.tx(ctx, func(tx pgx.Tx) error {
		owner := p.UserID
		if teamID != "" {
			role, err := s.teamRole(ctx, tx, p, teamID)
			if err != nil {
				return err
			}
			if !canManageTeam(p, role) {
				return errf(ErrForbidden, "team manager required")
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
	if _, err = tx.Exec(ctx, `INSERT INTO entries(space_id, kind, parent_id, name) VALUES($1, 'folder', NULL, '')`, id); err != nil {
		return "", err
	}
	if ownerTeam != "" {
		err = addTeamDefaultGrantTx(ctx, tx, id, ownerTeam)
	}
	return id, err
}

// addTeamDefaultGrantTx: managers of the owning team are owners; everyone
// else in it starts as Editor (changeable in the space's matrix).
func addTeamDefaultGrantTx(ctx context.Context, tx pgx.Tx, spaceID, teamID string) error {
	_, err := tx.Exec(ctx, `INSERT INTO grants(space_id, team_id, role_id) SELECT $1, $2, id FROM roles WHERE slug='editor'
		ON CONFLICT (space_id, team_id) WHERE space_id IS NOT NULL AND team_id IS NOT NULL DO NOTHING`, spaceID, teamID)
	return err
}

// TransferSpace hands a space to another user or team at once. confirm must
// be the space's slug. keepRole: a role for the previous owner ("" = none).
func (s *Service) TransferSpace(ctx context.Context, p *Principal, spaceID, toUser, toTeam, keepRole, confirm string) error {
	if err := requireSession(p); err != nil {
		return err
	}
	if !looksLikeUUID(spaceID) {
		return errf(ErrNotFound, "space not found")
	}
	if (toUser == "") == (toTeam == "") {
		return errf(ErrInvalid, "transfer to either a user or a team")
	}
	if err := s.requireCap(ctx, s.DB, p, spaceID, CapTransfer); err != nil && !p.Can(InstSpacesRecover) {
		return err
	}
	return s.tx(ctx, func(tx pgx.Tx) error {
		var slug string
		var fromUser, fromTeam *string
		err := tx.QueryRow(ctx, `SELECT slug, owner_user_id::text, owner_team_id::text FROM spaces WHERE id=$1 FOR UPDATE`, spaceID).
			Scan(&slug, &fromUser, &fromTeam)
		if errors.Is(err, pgx.ErrNoRows) {
			return errf(ErrNotFound, "space not found")
		}
		if err != nil {
			return err
		}
		if confirm != slug {
			return errf(ErrInvalid, "confirm the transfer with the slug of the space")
		}
		var toU, toT *string
		if toUser != "" {
			q := `SELECT id::text FROM users WHERE username = lower($1) AND NOT disabled`
			if looksLikeUUID(toUser) {
				q = `SELECT id::text FROM users WHERE id = $1::uuid AND NOT disabled`
			}
			var id string
			if err := tx.QueryRow(ctx, q, toUser).Scan(&id); err != nil {
				return errf(ErrNotFound, "user not found")
			}
			toU = &id
		} else {
			var id string
			if !looksLikeUUID(toTeam) || tx.QueryRow(ctx, `SELECT id::text FROM teams WHERE id=$1`, toTeam).Scan(&id) != nil {
				return errf(ErrNotFound, "team not found")
			}
			toT = &id
		}
		if (toU != nil && fromUser != nil && *toU == *fromUser) || (toT != nil && fromTeam != nil && *toT == *fromTeam) {
			return errf(ErrInvalid, "this is already the owner")
		}
		if _, err := tx.Exec(ctx, `DELETE FROM grants WHERE space_id=$1 AND (user_id=$2::uuid OR team_id=$3::uuid)`, spaceID, toU, toT); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE spaces SET owner_user_id=$2::uuid, owner_team_id=$3::uuid WHERE id=$1`, spaceID, toU, toT); err != nil {
			return err
		}
		if toT != nil {
			if err := addTeamDefaultGrantTx(ctx, tx, spaceID, *toT); err != nil {
				return err
			}
		}
		if keepRole != "" {
			role, err := roleByRefTx(ctx, tx, keepRole, "space")
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO grants(space_id, user_id, team_id, role_id, created_by) VALUES($1,$2::uuid,$3::uuid,$4,$5::uuid)
				ON CONFLICT DO NOTHING`, spaceID, fromUser, fromTeam, role.ID, p.userRef()); err != nil {
				return err
			}
		}
		if fromUser == nil {
			return nil
		}
		var left bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM spaces WHERE owner_user_id=$1)`, *fromUser).Scan(&left); err != nil || left {
			return err
		}
		var username, display string
		if err := tx.QueryRow(ctx, `SELECT username, display_name FROM users WHERE id=$1`, *fromUser).Scan(&username, &display); err != nil {
			return err
		}
		_, err = s.createSpaceTx(ctx, tx, s.uniqueSlug(ctx, tx, username), display, *fromUser, "")
		return err
	})
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
