package core

import (
	"context"
	"time"
)

// Grant shares a space with a user or a team beyond its owners.
type Grant struct {
	ID          string    `json:"id"`
	SpaceID     string    `json:"space_id"`
	UserID      *string   `json:"user_id"`
	Username    string    `json:"username,omitempty"`
	DisplayName string    `json:"display_name,omitempty"`
	TeamID      *string   `json:"team_id"`
	TeamName    string    `json:"team_name,omitempty"`
	Role        string    `json:"role"`
	CreatedAt   time.Time `json:"created_at"`
}

var grantRoles = map[string]bool{"viewer": true, "member": true, "admin": true}

// ListGrants lists who a space is shared with (anyone with access may see it).
func (s *Service) ListGrants(ctx context.Context, p *Principal, spaceID string) ([]Grant, error) {
	if err := s.requireCap(ctx, s.DB, p, spaceID, CapView); err != nil {
		return nil, err
	}
	rows, err := s.DB.Query(ctx, `
		SELECT g.id::text, g.space_id::text, g.user_id::text, coalesce(u.username,''), coalesce(u.display_name,''),
		       g.team_id::text, coalesce(t.name,''), g.role, g.created_at
		FROM space_grants g LEFT JOIN users u ON u.id = g.user_id LEFT JOIN teams t ON t.id = g.team_id
		WHERE g.space_id = $1 ORDER BY role_rank(g.role) DESC, coalesce(u.username, t.name)`, spaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Grant{}
	for rows.Next() {
		var g Grant
		if err := rows.Scan(&g.ID, &g.SpaceID, &g.UserID, &g.Username, &g.DisplayName, &g.TeamID, &g.TeamName, &g.Role, &g.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// SetGrant shares a space with a user (by id or username) or a team the
// caller belongs to. Requires the share capability; nobody can grant more
// than their own role.
func (s *Service) SetGrant(ctx context.Context, p *Principal, spaceID, user, teamID, role string) (*Grant, error) {
	if err := requireSession(p); err != nil {
		return nil, err
	}
	if err := s.requireCap(ctx, s.DB, p, spaceID, CapShare); err != nil {
		return nil, err
	}
	if !grantRoles[role] {
		return nil, errf(ErrInvalid, "role must be viewer, member or admin")
	}
	var own string
	if err := s.DB.QueryRow(ctx, `SELECT coalesce(space_role($1, $2::uuid), '')`, spaceID, p.UserID).Scan(&own); err != nil {
		return nil, err
	}
	if roleRank(role) > roleRank(own) {
		return nil, errf(ErrForbidden, "you cannot grant more than your own role (%s)", own)
	}
	var userID, team *string
	switch {
	case user != "" && teamID == "":
		var id string
		q := `SELECT id::text FROM users WHERE username = lower($1)`
		if looksLikeUUID(user) {
			q = `SELECT id::text FROM users WHERE id = $1::uuid`
		}
		if err := s.DB.QueryRow(ctx, q, user).Scan(&id); err != nil {
			return nil, errf(ErrNotFound, "user not found")
		}
		userID = &id
	case teamID != "" && user == "":
		if !looksLikeUUID(teamID) {
			return nil, errf(ErrNotFound, "team not found")
		}
		var member bool
		s.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM team_members WHERE team_id=$1 AND user_id=$2)`, teamID, p.UserID).Scan(&member)
		if !member && !p.IsAdmin {
			return nil, errf(ErrNotFound, "team not found")
		}
		team = &teamID
	default:
		return nil, errf(ErrInvalid, "share with either a user or a team")
	}
	var id string
	conflict := `(space_id, user_id) WHERE user_id IS NOT NULL`
	if team != nil {
		conflict = `(space_id, team_id) WHERE team_id IS NOT NULL`
	}
	err := s.DB.QueryRow(ctx, `INSERT INTO space_grants(space_id, user_id, team_id, role, created_by) VALUES($1,$2,$3,$4,$5)
		ON CONFLICT `+conflict+` DO UPDATE SET role = EXCLUDED.role RETURNING id::text`,
		spaceID, userID, team, role, p.userRef()).Scan(&id)
	if err != nil {
		return nil, err
	}
	grants, err := s.ListGrants(ctx, p, spaceID)
	if err != nil {
		return nil, err
	}
	for i := range grants {
		if grants[i].ID == id {
			return &grants[i], nil
		}
	}
	return nil, errf(ErrNotFound, "grant not found")
}

// DeleteGrant revokes a share (share capability).
func (s *Service) DeleteGrant(ctx context.Context, p *Principal, spaceID, grantID string) error {
	if err := s.requireCap(ctx, s.DB, p, spaceID, CapShare); err != nil {
		return err
	}
	if !looksLikeUUID(grantID) {
		return errf(ErrNotFound, "grant not found")
	}
	tag, err := s.DB.Exec(ctx, `DELETE FROM space_grants WHERE id=$1 AND space_id=$2`, grantID, spaceID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errf(ErrNotFound, "grant not found")
	}
	return nil
}

func roleRank(r string) int {
	switch r {
	case "owner":
		return 4
	case "admin":
		return 3
	case "member":
		return 2
	case "viewer":
		return 1
	}
	return 0
}
