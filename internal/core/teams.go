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
	Role      string    `json:"role"` // the caller: manager, member, or "" (instance administrators)
	CreatedAt time.Time `json:"created_at"`
}

// Member is a team membership.
type Member struct {
	UserID      string `json:"user_id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
}

var validRoles = map[string]bool{"manager": true, "member": true}

const memberRole = `CASE WHEN tm.is_manager THEN 'manager' ELSE 'member' END`

func (s *Service) teamRole(ctx context.Context, q db.Querier, p *Principal, teamID string) (string, error) {
	var role string
	err := q.QueryRow(ctx, `SELECT `+memberRole+` FROM team_members tm WHERE team_id=$1 AND user_id=$2`, teamID, p.UserID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		var exists bool
		q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM teams WHERE id=$1)`, teamID).Scan(&exists)
		if !exists || !p.Can(InstTeams) {
			return "", errf(ErrNotFound, "team not found")
		}
		return "", nil
	}
	return role, err
}

// canManageTeam: the team's managers, and instance administrators.
func canManageTeam(p *Principal, role string) bool { return role == "manager" || p.Can(InstTeams) }

func requireSession(p *Principal) error {
	if p == nil {
		return ErrUnauthorized
	}
	switch p.Via {
	case "accesskey", "ssh", "apikey":
		return errf(ErrForbidden, "not allowed with keys")
	}
	return nil
}

// CreateTeam creates a team; the creator becomes owner.
func (s *Service) CreateTeam(ctx context.Context, p *Principal, name string) (*Team, error) {
	if err := requireSession(p); err != nil {
		return nil, err
	}
	if !p.Can(InstTeamsCreate) && !p.Can(InstTeams) {
		return nil, errf(ErrForbidden, "not allowed to create teams")
	}
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 100 {
		return nil, errf(ErrInvalid, "team name must be 1-100 characters")
	}
	t := &Team{Name: name, Role: "manager"}
	err := s.tx(ctx, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `INSERT INTO teams(name) VALUES($1) RETURNING id::text, created_at`, name).Scan(&t.ID, &t.CreatedAt)
		if db.IsUniqueViolation(err) {
			return errf(ErrConflict, "team %q exists", name)
		}
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO team_members(team_id, user_id, is_manager) VALUES($1,$2,true)`, t.ID, p.UserID)
		return err
	})
	return t, err
}

// ListTeams lists the caller's teams (all teams for system admins).
func (s *Service) ListTeams(ctx context.Context, p *Principal) ([]Team, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT t.id::text, t.name, CASE WHEN tm.user_id IS NULL THEN '' ELSE `+memberRole+` END, t.created_at
		FROM teams t LEFT JOIN team_members tm ON tm.team_id=t.id AND tm.user_id=$1
		WHERE tm.user_id IS NOT NULL OR $2
		ORDER BY t.name`, p.UserID, p.Can(InstTeams))
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
	if !canManageTeam(p, role) {
		return errf(ErrForbidden, "team manager required")
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
		if !canManageTeam(p, role) {
			return errf(ErrForbidden, "team manager required")
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
	rows, err := s.DB.Query(ctx, `SELECT u.id::text, u.username, u.display_name, `+memberRole+`
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

// SetMember adds a member or changes whether they manage the team.
func (s *Service) SetMember(ctx context.Context, p *Principal, teamID, userID, role string) error {
	if err := requireSession(p); err != nil {
		return err
	}
	if !validRoles[role] {
		return errf(ErrInvalid, "role must be manager or member")
	}
	return s.tx(ctx, func(tx pgx.Tx) error {
		myRole, err := s.teamRole(ctx, tx, p, teamID)
		if err != nil {
			return err
		}
		if !canManageTeam(p, myRole) {
			return errf(ErrForbidden, "team manager required")
		}
		var manager bool
		err = tx.QueryRow(ctx, `SELECT is_manager FROM team_members WHERE team_id=$1 AND user_id=$2 FOR UPDATE`, teamID, userID).Scan(&manager)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if manager && role != "manager" {
			if err := lastManagerCheck(ctx, tx, teamID); err != nil {
				return err
			}
		}
		_, err = tx.Exec(ctx, `INSERT INTO team_members(team_id, user_id, is_manager) VALUES($1,$2,$3)
			ON CONFLICT (team_id, user_id) DO UPDATE SET is_manager=EXCLUDED.is_manager`, teamID, userID, role == "manager")
		if db.IsForeignKeyViolation(err) {
			return errf(ErrNotFound, "user not found")
		}
		return err
	})
}

// RemoveMember removes a member (a team manager, or the member itself).
func (s *Service) RemoveMember(ctx context.Context, p *Principal, teamID, userID string) error {
	if err := requireSession(p); err != nil {
		return err
	}
	return s.tx(ctx, func(tx pgx.Tx) error {
		myRole, err := s.teamRole(ctx, tx, p, teamID)
		if err != nil {
			return err
		}
		var manager bool
		err = tx.QueryRow(ctx, `SELECT is_manager FROM team_members WHERE team_id=$1 AND user_id=$2 FOR UPDATE`, teamID, userID).Scan(&manager)
		if errors.Is(err, pgx.ErrNoRows) {
			return errf(ErrNotFound, "member not found")
		}
		if err != nil {
			return err
		}
		if userID != p.UserID && !canManageTeam(p, myRole) {
			return errf(ErrForbidden, "team manager required")
		}
		if manager {
			if err := lastManagerCheck(ctx, tx, teamID); err != nil {
				return err
			}
		}
		_, err = tx.Exec(ctx, `DELETE FROM team_members WHERE team_id=$1 AND user_id=$2`, teamID, userID)
		return err
	})
}

func lastManagerCheck(ctx context.Context, tx pgx.Tx, teamID string) error {
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM team_members WHERE team_id=$1 AND is_manager`, teamID).Scan(&n); err != nil {
		return err
	}
	if n <= 1 {
		return errf(ErrInvalid, "a team needs at least one manager")
	}
	return nil
}
