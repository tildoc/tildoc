package core

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/tildoc/tildoc/internal/db"
)

// Grant is one row of a permission matrix: a user or team with a role and/or
// individual permissions, on a space, a document or the instance.
type Grant struct {
	ID          string    `json:"id"`
	SpaceID     *string   `json:"space_id"`
	DocumentID  *string   `json:"document_id"`
	UserID      *string   `json:"user_id"`
	Username    string    `json:"username,omitempty"`
	DisplayName string    `json:"display_name,omitempty"`
	TeamID      *string   `json:"team_id"`
	TeamName    string    `json:"team_name,omitempty"`
	Role        string    `json:"role"` // slug, "" for individual permissions only
	RoleName    string    `json:"role_name,omitempty"`
	Permissions []string  `json:"permissions"` // individually added
	CreatedAt   time.Time `json:"created_at"`
}

func (s *Service) listGrants(ctx context.Context, q db.Querier, where string, args ...any) ([]Grant, error) {
	rows, err := q.Query(ctx, `
		SELECT g.id::text, g.space_id::text, g.document_id::text, g.user_id::text, coalesce(u.username,''), coalesce(u.display_name,''),
		       g.team_id::text, coalesce(t.name,''), coalesce(r.slug,''), coalesce(r.name,''), g.created_at,
		       ARRAY(SELECT permission FROM grant_permissions WHERE grant_id = g.id ORDER BY permission)
		FROM grants g LEFT JOIN users u ON u.id = g.user_id LEFT JOIN teams t ON t.id = g.team_id LEFT JOIN roles r ON r.id = g.role_id
		WHERE `+where+` ORDER BY coalesce(u.username, t.name)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Grant{}
	for rows.Next() {
		var g Grant
		if err := rows.Scan(&g.ID, &g.SpaceID, &g.DocumentID, &g.UserID, &g.Username, &g.DisplayName, &g.TeamID, &g.TeamName, &g.Role, &g.RoleName, &g.CreatedAt, &g.Permissions); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// ListGrants lists who a space is shared with (anyone with access may see it).
func (s *Service) ListGrants(ctx context.Context, p *Principal, spaceID string) ([]Grant, error) {
	if err := s.requireCap(ctx, s.DB, p, spaceID, CapView); err != nil {
		return nil, err
	}
	return s.listGrants(ctx, s.DB, `g.space_id = $1`, spaceID)
}

// resolveSubject finds the user (id or username) or team a grant is for.
func (s *Service) resolveSubject(ctx context.Context, p *Principal, user, teamID string) (userID, team *string, err error) {
	switch {
	case user != "" && teamID == "":
		var id string
		q := `SELECT id::text FROM users WHERE username = lower($1) AND status = 'active'`
		if looksLikeUUID(user) {
			q = `SELECT id::text FROM users WHERE id = $1::uuid AND status = 'active'`
		}
		if err := s.DB.QueryRow(ctx, q, user).Scan(&id); err != nil {
			return nil, nil, errf(ErrNotFound, "user not found")
		}
		return &id, nil, nil
	case teamID != "" && user == "":
		if !looksLikeUUID(teamID) {
			return nil, nil, errf(ErrNotFound, "team not found")
		}
		var member bool
		if err := s.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM team_members WHERE team_id=$1 AND user_id=$2)`, teamID, p.UserID).Scan(&member); err != nil {
			return nil, nil, err
		}
		if !member && !p.Can(InstTeams) && !p.Can(InstRoles) {
			return nil, nil, errf(ErrNotFound, "team not found")
		}
		return nil, &teamID, nil
	}
	return nil, nil, errf(ErrInvalid, "share with either a user or a team")
}

// saveGrantTx creates or replaces one matrix row.
func saveGrantTx(ctx context.Context, tx pgx.Tx, p *Principal, spaceID, docID, userID, teamID *string, roleID *string, extra []string) (string, error) {
	conflict := func() string {
		switch {
		case spaceID != nil && userID != nil:
			return `(space_id, user_id) WHERE space_id IS NOT NULL AND user_id IS NOT NULL`
		case spaceID != nil:
			return `(space_id, team_id) WHERE space_id IS NOT NULL AND team_id IS NOT NULL`
		case docID != nil && userID != nil:
			return `(document_id, user_id) WHERE document_id IS NOT NULL AND user_id IS NOT NULL`
		case docID != nil:
			return `(document_id, team_id) WHERE document_id IS NOT NULL AND team_id IS NOT NULL`
		case userID != nil:
			return `(user_id) WHERE space_id IS NULL AND document_id IS NULL AND user_id IS NOT NULL`
		}
		return `(team_id) WHERE space_id IS NULL AND document_id IS NULL AND team_id IS NOT NULL`
	}()
	var id string
	if err := tx.QueryRow(ctx, `INSERT INTO grants(space_id, document_id, user_id, team_id, role_id, created_by)
		VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6::uuid)
		ON CONFLICT `+conflict+` DO UPDATE SET role_id = EXCLUDED.role_id RETURNING id::text`,
		spaceID, docID, userID, teamID, roleID, p.userRef()).Scan(&id); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM grant_permissions WHERE grant_id=$1`, id); err != nil {
		return "", err
	}
	_, err := tx.Exec(ctx, `INSERT INTO grant_permissions(grant_id, permission) SELECT $1, unnest($2::text[])`, id, extra)
	return id, err
}

func grantDetails(userID, team *string, role string, extra []string) map[string]any {
	return map[string]any{"user_id": userID, "team_id": team, "role": role, "permissions": extra}
}

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// grantRow validates a role + extras. held limits them: nobody grants more
// than they have.
func (s *Service) grantRow(ctx context.Context, q db.Querier, roleRef string, extra []string, mask, held Cap) (*string, Cap, error) {
	var want Cap
	var roleID *string
	if roleRef != "" {
		r, err := roleByRefTx(ctx, q, roleRef, "space")
		if err != nil {
			return nil, 0, err
		}
		roleID, want = &r.ID, capsFromNames(r.Permissions)
	}
	if roleRef == "" && len(extra) == 0 {
		return nil, 0, errf(ErrInvalid, "choose a role or individual permissions")
	}
	for _, x := range extra {
		c := capsFromNames([]string{x})
		if c == 0 || c&mask != c {
			return nil, 0, errf(ErrInvalid, "permission %q cannot be granted here", x)
		}
		want |= c
	}
	if want&mask&^held != 0 {
		return nil, 0, errf(ErrForbidden, "you cannot grant more than you have")
	}
	return roleID, want, nil
}

// SetGrant shares a space with a user or team: a role, individual
// permissions, or both.
func (s *Service) SetGrant(ctx context.Context, p *Principal, spaceID, user, teamID, role string, extra ...string) (*Grant, error) {
	if err := requireSession(p); err != nil {
		return nil, err
	}
	if err := s.requireCap(ctx, s.DB, p, spaceID, CapShareSpacePeople); err != nil {
		return nil, err
	}
	held, err := s.heldCaps(ctx, s.DB, p, spaceID)
	if err != nil {
		return nil, err
	}
	roleID, _, err := s.grantRow(ctx, s.DB, role, extra, capAll, held)
	if err != nil {
		return nil, err
	}
	userID, team, err := s.resolveSubject(ctx, p, user, teamID)
	if err != nil {
		return nil, err
	}
	var id string
	err = s.tx(ctx, func(tx pgx.Tx) error {
		var err error
		if id, err = saveGrantTx(ctx, tx, p, &spaceID, nil, userID, team, roleID, extra); err != nil {
			return err
		}
		return s.audit(ctx, tx, p, "grant.set", "space", spaceID, spaceID, grantDetails(userID, team, role, extra))
	})
	if err != nil {
		return nil, err
	}
	return s.oneGrant(ctx, id)
}

func (s *Service) oneGrant(ctx context.Context, id string) (*Grant, error) {
	gs, err := s.listGrants(ctx, s.DB, `g.id = $1`, id)
	if err != nil {
		return nil, err
	}
	if len(gs) == 0 {
		return nil, errf(ErrNotFound, "grant not found")
	}
	return &gs[0], nil
}

// DeleteGrant revokes a share of a space.
func (s *Service) DeleteGrant(ctx context.Context, p *Principal, spaceID, grantID string) error {
	if err := requireSession(p); err != nil {
		return err
	}
	if err := s.requireCap(ctx, s.DB, p, spaceID, CapShareSpacePeople); err != nil {
		return err
	}
	return s.deleteGrant(ctx, p, "space", spaceID, spaceID, `id=$1 AND space_id=$2`, grantID, spaceID)
}

func (s *Service) deleteGrant(ctx context.Context, p *Principal, targetType, targetID, spaceID, where string, args ...any) error {
	if id, _ := args[0].(string); !looksLikeUUID(id) {
		return errf(ErrNotFound, "grant not found")
	}
	return s.tx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM grants WHERE `+where, args...)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return errf(ErrNotFound, "grant not found")
		}
		return s.audit(ctx, tx, p, "grant.delete", targetType, targetID, spaceID, map[string]any{"grant_id": args[0]})
	})
}

// ---------------------------------------------------------------- single files

func (s *Service) docSpace(ctx context.Context, docID string) (string, error) {
	if !looksLikeUUID(docID) {
		return "", errf(ErrNotFound, "document not found")
	}
	var spaceID string
	if err := s.DB.QueryRow(ctx, `SELECT space_id::text FROM documents WHERE id=$1 AND deleted_at IS NULL`, docID).Scan(&spaceID); err != nil {
		return "", errf(ErrNotFound, "document not found")
	}
	return spaceID, nil
}

// ListDocumentGrants lists who a single file is shared with.
func (s *Service) ListDocumentGrants(ctx context.Context, p *Principal, docID string) ([]Grant, error) {
	spaceID, err := s.docSpace(ctx, docID)
	if err != nil {
		return nil, err
	}
	if err := s.requireDocCap(ctx, s.DB, p, docID, spaceID, CapShareFilePeople); err != nil {
		return nil, err
	}
	return s.listGrants(ctx, s.DB, `g.document_id = $1`, docID)
}

// SetDocumentGrant shares a single file with a user or team.
func (s *Service) SetDocumentGrant(ctx context.Context, p *Principal, docID, user, teamID, role string, extra ...string) (*Grant, error) {
	if err := requireSession(p); err != nil {
		return nil, err
	}
	spaceID, err := s.docSpace(ctx, docID)
	if err != nil {
		return nil, err
	}
	if err := s.requireDocCap(ctx, s.DB, p, docID, spaceID, CapShareFilePeople); err != nil {
		return nil, err
	}
	held, err := s.docCaps(ctx, s.DB, p, docID, spaceID)
	if err != nil {
		return nil, err
	}
	roleID, _, err := s.grantRow(ctx, s.DB, role, extra, capFile, held)
	if err != nil {
		return nil, err
	}
	userID, team, err := s.resolveSubject(ctx, p, user, teamID)
	if err != nil {
		return nil, err
	}
	var id string
	err = s.tx(ctx, func(tx pgx.Tx) error {
		var err error
		if id, err = saveGrantTx(ctx, tx, p, nil, &docID, userID, team, roleID, extra); err != nil {
			return err
		}
		return s.audit(ctx, tx, p, "grant.set", "document", docID, spaceID, grantDetails(userID, team, role, extra))
	})
	if err != nil {
		return nil, err
	}
	return s.oneGrant(ctx, id)
}

// DeleteDocumentGrant revokes a share of a single file.
func (s *Service) DeleteDocumentGrant(ctx context.Context, p *Principal, docID, grantID string) error {
	spaceID, err := s.docSpace(ctx, docID)
	if err != nil {
		return err
	}
	if err := s.requireDocCap(ctx, s.DB, p, docID, spaceID, CapShareFilePeople); err != nil {
		return err
	}
	return s.deleteGrant(ctx, p, "document", docID, spaceID, `id=$1 AND document_id=$2`, grantID, docID)
}

// ListSharedFiles lists single files shared with the caller.
func (s *Service) ListSharedFiles(ctx context.Context, p *Principal) ([]FileInfo, error) {
	if p == nil {
		return nil, ErrUnauthorized
	}
	files, err := scanFiles(s.DB.Query(ctx, fileSelect+`
		WHERE l.kind = 'link' AND l.deleted_at IS NULL
		  AND NOT EXISTS (SELECT 1 FROM entries e2 WHERE e2.document_id = l.document_id AND e2.kind = 'link' AND e2.deleted_at IS NULL AND e2.id < l.id)
		  AND l.document_id IN (SELECT g.document_id FROM grants g WHERE g.document_id IS NOT NULL
		      AND (g.user_id = $1 OR g.team_id IN (SELECT team_id FROM team_members WHERE user_id = $1)))
		ORDER BY l.name`, p.UserID))
	if err != nil {
		return nil, err
	}
	out := files[:0]
	for _, f := range files {
		caps, err := s.docCaps(ctx, s.DB, p, f.DocumentID, f.SpaceID)
		if err != nil {
			return nil, err
		}
		if !caps.Has(CapView) {
			continue
		}
		if s.requirePerm(ctx, s.DB, p, f.SpaceID, PermRead) != nil {
			f.FolderID, f.Path, f.OriginalPath = "", "/"+f.Name, ""
		}
		out = append(out, f)
	}
	return out, nil
}

// ---------------------------------------------------------------- instance

// ListInstanceGrants lists who holds instance permissions.
func (s *Service) ListInstanceGrants(ctx context.Context, p *Principal) ([]Grant, error) {
	if err := requireInst(p, InstRoles); err != nil {
		return nil, err
	}
	return s.listGrants(ctx, s.DB, `g.space_id IS NULL AND g.document_id IS NULL`)
}

// SetInstanceGrant gives a user or team instance permissions.
func (s *Service) SetInstanceGrant(ctx context.Context, p *Principal, user, teamID, role string, extra ...string) (*Grant, error) {
	if err := requireSession(p); err != nil {
		return nil, err
	}
	if err := requireInst(p, InstRoles); err != nil {
		return nil, err
	}
	if role == "" && len(extra) == 0 {
		return nil, errf(ErrInvalid, "choose a role or individual permissions")
	}
	var roleID *string
	if role != "" {
		r, err := roleByRefTx(ctx, s.DB, role, "instance")
		if err != nil {
			return nil, err
		}
		roleID = &r.ID
	}
	if err := validRolePerms("instance", extra); err != nil {
		return nil, err
	}
	userID, team, err := s.resolveSubject(ctx, p, user, teamID)
	if err != nil {
		return nil, err
	}
	var id string
	err = s.tx(ctx, func(tx pgx.Tx) error {
		if userID != nil && role != "administrator" {
			var admin bool
			if err := tx.QueryRow(ctx, `SELECT is_administrator($1::uuid)`, *userID).Scan(&admin); err != nil {
				return err
			}
			if admin {
				if err := lastAdminCheck(ctx, tx, *userID); err != nil {
					return err
				}
			}
		}
		var err error
		if id, err = saveGrantTx(ctx, tx, p, nil, nil, userID, team, roleID, extra); err != nil {
			return err
		}
		return s.audit(ctx, tx, p, "grant.set", "instance", "", "", grantDetails(userID, team, role, extra))
	})
	if err != nil {
		return nil, err
	}
	return s.oneGrant(ctx, id)
}

// DeleteInstanceGrant takes instance permissions away.
func (s *Service) DeleteInstanceGrant(ctx context.Context, p *Principal, grantID string) error {
	if err := requireSession(p); err != nil {
		return err
	}
	if err := requireInst(p, InstRoles); err != nil {
		return err
	}
	if !looksLikeUUID(grantID) {
		return errf(ErrNotFound, "grant not found")
	}
	return s.tx(ctx, func(tx pgx.Tx) error {
		var userID *string
		var admin bool
		err := tx.QueryRow(ctx, `SELECT g.user_id::text, coalesce(r.slug = 'administrator', false)
			FROM grants g LEFT JOIN roles r ON r.id = g.role_id WHERE g.id=$1 AND g.space_id IS NULL AND g.document_id IS NULL`, grantID).Scan(&userID, &admin)
		if err != nil {
			return errf(ErrNotFound, "grant not found")
		}
		if admin && userID != nil {
			if err := lastAdminCheck(ctx, tx, *userID); err != nil {
				return err
			}
		}
		if _, err = tx.Exec(ctx, `DELETE FROM grants WHERE id=$1`, grantID); err != nil {
			return err
		}
		return s.audit(ctx, tx, p, "grant.delete", "instance", "", "", map[string]any{"grant_id": grantID})
	})
}
