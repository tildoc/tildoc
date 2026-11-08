package core

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/tildoc/tildoc/internal/db"
)

// Team groups users.
type Team struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Role      string    `json:"role"` // caller's role ("" for system admins without membership)
	CreatedAt time.Time `json:"created_at"`
}

// Member is a team membership.
type Member struct {
	UserID      string `json:"user_id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
}

var validRoles = map[string]bool{"owner": true, "admin": true, "member": true, "viewer": true}

func (s *Service) teamRole(ctx context.Context, q db.Querier, p *Principal, teamID string) (string, error) {
	var role string
	err := q.QueryRow(ctx, `SELECT role FROM team_members WHERE team_id=$1 AND user_id=$2`, teamID, p.UserID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		var exists bool
		q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM teams WHERE id=$1)`, teamID).Scan(&exists)
		if !exists || !p.IsAdmin {
			return "", errf(ErrNotFound, "team not found")
		}
		return "", nil
	}
	return role, err
}

func requireSession(p *Principal) error {
	if p == nil {
		return ErrUnauthorized
	}
	if p.Via == "accesskey" || p.Via == "ssh" {
		return errf(ErrForbidden, "not allowed with access keys")
	}
	return nil
}

// CreateTeam creates a team; the creator becomes owner.
func (s *Service) CreateTeam(ctx context.Context, p *Principal, name string) (*Team, error) {
	if err := requireSession(p); err != nil {
		return nil, err
	}
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 100 {
		return nil, errf(ErrInvalid, "team name must be 1-100 characters")
	}
	t := &Team{Name: name, Role: "owner"}
	err := s.tx(ctx, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `INSERT INTO teams(name) VALUES($1) RETURNING id::text, created_at`, name).Scan(&t.ID, &t.CreatedAt)
		if db.IsUniqueViolation(err) {
			return errf(ErrConflict, "team %q exists", name)
		}
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO team_members(team_id, user_id, role) VALUES($1,$2,'owner')`, t.ID, p.UserID)
		return err
	})
	return t, err
}

// ListTeams lists the caller's teams (all teams for system admins).
func (s *Service) ListTeams(ctx context.Context, p *Principal) ([]Team, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT t.id::text, t.name, coalesce(tm.role,''), t.created_at
		FROM teams t LEFT JOIN team_members tm ON tm.team_id=t.id AND tm.user_id=$1
		WHERE tm.user_id IS NOT NULL OR $2
		ORDER BY t.name`, p.UserID, p.IsAdmin)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Team{}
	for rows.Next() {
		var t Team
		if err := rows.Scan(&t.ID, &t.Name, &t.Role, &t.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// GetTeam returns a team the caller belongs to.
func (s *Service) GetTeam(ctx context.Context, p *Principal, id string) (*Team, error) {
	role, err := s.teamRole(ctx, s.DB, p, id)
	if err != nil {
		return nil, err
	}
	t := &Team{ID: id, Role: role}
	err = s.DB.QueryRow(ctx, `SELECT name, created_at FROM teams WHERE id=$1`, id).Scan(&t.Name, &t.CreatedAt)
	return t, err
}

// RenameTeam renames a team (owner/admin).
func (s *Service) RenameTeam(ctx context.Context, p *Principal, id, name string) error {
	if err := requireSession(p); err != nil {
		return err
	}
	role, err := s.teamRole(ctx, s.DB, p, id)
	if err != nil {
		return err
	}
	if role != "owner" && role != "admin" {
		return errf(ErrForbidden, "team admin required")
	}
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 100 {
		return errf(ErrInvalid, "team name must be 1-100 characters")
	}
	_, err = s.DB.Exec(ctx, `UPDATE teams SET name=$2 WHERE id=$1`, id, name)
	if db.IsUniqueViolation(err) {
		return errf(ErrConflict, "team %q exists", name)
	}
	return err
}

// DeleteTeam deletes a team. Only allowed when its spaces are empty.
func (s *Service) DeleteTeam(ctx context.Context, p *Principal, id string) error {
	if err := requireSession(p); err != nil {
		return err
	}
	return s.tx(ctx, func(tx pgx.Tx) error {
		role, err := s.teamRole(ctx, tx, p, id)
		if err != nil {
			return err
		}
		if role != "owner" && !p.IsAdmin {
			return errf(ErrForbidden, "team owner required")
		}
		var live bool
		tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM documents d JOIN spaces s ON s.id=d.space_id
			WHERE s.owner_team_id=$1 AND d.deleted_at IS NULL)`, id).Scan(&live)
		if live {
			return errf(ErrNotEmpty, "team spaces still contain documents")
		}
		_, err = tx.Exec(ctx, `DELETE FROM teams WHERE id=$1`, id)
		return err
	})
}

// ListMembers lists members of a team.
func (s *Service) ListMembers(ctx context.Context, p *Principal, teamID string) ([]Member, error) {
	if _, err := s.teamRole(ctx, s.DB, p, teamID); err != nil {
		return nil, err
	}
	rows, err := s.DB.Query(ctx, `SELECT u.id::text, u.username, u.display_name, tm.role
		FROM team_members tm JOIN users u ON u.id=tm.user_id WHERE tm.team_id=$1 ORDER BY u.username`, teamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Member{}
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.UserID, &m.Username, &m.DisplayName, &m.Role); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// SetMember adds a member or changes its role. Admins manage members/viewers/
// admins; only owners grant or revoke the owner role.
func (s *Service) SetMember(ctx context.Context, p *Principal, teamID, userID, role string) error {
	if err := requireSession(p); err != nil {
		return err
	}
	if !validRoles[role] {
		return errf(ErrInvalid, "invalid role %q", role)
	}
	return s.tx(ctx, func(tx pgx.Tx) error {
		myRole, err := s.teamRole(ctx, tx, p, teamID)
		if err != nil {
			return err
		}
		if myRole != "owner" && myRole != "admin" && !p.IsAdmin {
			return errf(ErrForbidden, "team admin required")
		}
		var current string
		err = tx.QueryRow(ctx, `SELECT role FROM team_members WHERE team_id=$1 AND user_id=$2 FOR UPDATE`, teamID, userID).Scan(&current)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if (role == "owner" || current == "owner") && myRole != "owner" && !p.IsAdmin {
			return errf(ErrForbidden, "only owners manage owners")
		}
		if current == "owner" && role != "owner" {
			if err := lastOwnerCheck(ctx, tx, teamID); err != nil {
				return err
			}
		}
		_, err = tx.Exec(ctx, `INSERT INTO team_members(team_id, user_id, role) VALUES($1,$2,$3)
			ON CONFLICT (team_id, user_id) DO UPDATE SET role=EXCLUDED.role`, teamID, userID, role)
		if db.IsForeignKeyViolation(err) {
			return errf(ErrNotFound, "user not found")
		}
		return err
	})
}

// RemoveMember removes a member (team admin, or the member itself).
func (s *Service) RemoveMember(ctx context.Context, p *Principal, teamID, userID string) error {
	if err := requireSession(p); err != nil {
		return err
	}
	return s.tx(ctx, func(tx pgx.Tx) error {
		myRole, err := s.teamRole(ctx, tx, p, teamID)
		if err != nil {
			return err
		}
		var current string
		err = tx.QueryRow(ctx, `SELECT role FROM team_members WHERE team_id=$1 AND user_id=$2 FOR UPDATE`, teamID, userID).Scan(&current)
		if errors.Is(err, pgx.ErrNoRows) {
			return errf(ErrNotFound, "member not found")
		}
		if err != nil {
			return err
		}
		self := userID == p.UserID
		if !self && myRole != "owner" && myRole != "admin" && !p.IsAdmin {
			return errf(ErrForbidden, "team admin required")
		}
		if current == "owner" {
			if !self && myRole != "owner" && !p.IsAdmin {
				return errf(ErrForbidden, "only owners remove owners")
			}
			if err := lastOwnerCheck(ctx, tx, teamID); err != nil {
				return err
			}
		}
		_, err = tx.Exec(ctx, `DELETE FROM team_members WHERE team_id=$1 AND user_id=$2`, teamID, userID)
		return err
	})
}

func lastOwnerCheck(ctx context.Context, tx pgx.Tx, teamID string) error {
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM team_members WHERE team_id=$1 AND role='owner'`, teamID).Scan(&n); err != nil {
		return err
	}
	if n <= 1 {
		return errf(ErrInvalid, "a team needs at least one owner")
	}
	return nil
}
