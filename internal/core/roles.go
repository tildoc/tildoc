package core

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/tildoc/tildoc/internal/db"
)

// Role is a named set of permissions.
type Role struct {
	ID          string    `json:"id"`
	Slug        string    `json:"slug"`
	Name        string    `json:"name"`
	Scope       string    `json:"scope"` // space, instance
	BuiltIn     bool      `json:"built_in"`
	Permissions []string  `json:"permissions"`
	CreatedAt   time.Time `json:"created_at"`
}

// PermissionCatalogue lists every permission, for the matrix views.
type PermissionCatalogue struct {
	Space    []string `json:"space"`
	File     []string `json:"file"` // what a grant on a single file can give
	Instance []string `json:"instance"`
}

// Catalogue returns the permission catalogue.
func Catalogue() PermissionCatalogue {
	return PermissionCatalogue{Space: capAll.Names(), File: capFile.Names(), Instance: InstCap(^uint16(0)).Names()}
}

const roleSelect = `SELECT r.id::text, r.slug, r.name, r.scope, r.built_in, r.created_at,
	ARRAY(SELECT permission FROM role_permissions WHERE role_id = r.id ORDER BY permission) FROM roles r`

func scanRole(row pgx.Row) (*Role, error) {
	r := &Role{}
	err := row.Scan(&r.ID, &r.Slug, &r.Name, &r.Scope, &r.BuiltIn, &r.CreatedAt, &r.Permissions)
	return r, err
}

// ListRoles lists all roles (everyone who can share needs them).
func (s *Service) ListRoles(ctx context.Context, p *Principal) ([]Role, error) {
	if p == nil {
		return nil, ErrUnauthorized
	}
	rows, err := s.DB.Query(ctx, roleSelect+` ORDER BY r.scope, cardinality(ARRAY(SELECT 1 FROM role_permissions WHERE role_id = r.id)), r.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Role{}
	for rows.Next() {
		r, err := scanRole(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// roleByRefTx finds a role by id or slug and checks its scope.
func roleByRefTx(ctx context.Context, q db.Querier, ref, scope string) (*Role, error) {
	where := ` WHERE r.slug = $1`
	if looksLikeUUID(ref) {
		where = ` WHERE r.id = $1::uuid`
	}
	r, err := scanRole(q.QueryRow(ctx, roleSelect+where, ref))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errf(ErrInvalid, "unknown role %q", ref)
	}
	if err != nil {
		return nil, err
	}
	if r.Scope != scope {
		return nil, errf(ErrInvalid, "role %q cannot be used here", r.Name)
	}
	return r, nil
}

func validRolePerms(scope string, perms []string) error {
	known := capAll.Names()
	if scope == "instance" {
		known = InstCap(^uint16(0)).Names()
	}
	for _, x := range perms {
		ok := false
		for _, k := range known {
			ok = ok || k == x
		}
		if !ok {
			return errf(ErrInvalid, "unknown permission %q", x)
		}
	}
	return nil
}

func setRolePermsTx(ctx context.Context, tx pgx.Tx, roleID string, perms []string) error {
	if _, err := tx.Exec(ctx, `DELETE FROM role_permissions WHERE role_id=$1`, roleID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO role_permissions(role_id, permission) SELECT $1, unnest($2::text[])`, roleID, perms)
	return err
}

// CreateRole adds a role.
func (s *Service) CreateRole(ctx context.Context, p *Principal, name, scope string, perms []string) (*Role, error) {
	if err := requireInst(p, InstRoles); err != nil {
		return nil, err
	}
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 60 {
		return nil, errf(ErrInvalid, "role name must be 1-60 characters")
	}
	if scope != "space" && scope != "instance" {
		return nil, errf(ErrInvalid, "scope must be space or instance")
	}
	if err := validRolePerms(scope, perms); err != nil {
		return nil, err
	}
	var id string
	err := s.tx(ctx, func(tx pgx.Tx) error {
		slug := slugify(name)
		cand := slug
		for i := 2; ; i++ {
			var exists bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM roles WHERE slug=$1)`, cand).Scan(&exists); err != nil {
				return err
			}
			if !exists {
				break
			}
			cand = slug + "-" + strings.Repeat("x", i-1)
		}
		err := tx.QueryRow(ctx, `INSERT INTO roles(slug, name, scope) VALUES($1,$2,$3) RETURNING id::text`, cand, name, scope).Scan(&id)
		if db.IsUniqueViolation(err) {
			return errf(ErrConflict, "role %q exists", name)
		}
		if err != nil {
			return err
		}
		if err := setRolePermsTx(ctx, tx, id, perms); err != nil {
			return err
		}
		return s.audit(ctx, tx, p, "role.create", "role", id, "", map[string]any{"name": name, "scope": scope, "permissions": perms})
	})
	if err != nil {
		return nil, err
	}
	return scanRole(s.DB.QueryRow(ctx, roleSelect+` WHERE r.id = $1::uuid`, id))
}

// UpdateRole renames a role or replaces its permissions. Built-in roles keep
// their name; Administrator is fixed, so nobody can lock everyone out.
func (s *Service) UpdateRole(ctx context.Context, p *Principal, id string, name *string, perms []string) (*Role, error) {
	if err := requireInst(p, InstRoles); err != nil {
		return nil, err
	}
	if !looksLikeUUID(id) {
		return nil, errf(ErrNotFound, "role not found")
	}
	err := s.tx(ctx, func(tx pgx.Tx) error {
		r, err := scanRole(tx.QueryRow(ctx, roleSelect+` WHERE r.id = $1::uuid`, id))
		if errors.Is(err, pgx.ErrNoRows) {
			return errf(ErrNotFound, "role not found")
		}
		if err != nil {
			return err
		}
		if r.Slug == "administrator" {
			return errf(ErrInvalid, "the Administrator role cannot be changed")
		}
		if name != nil {
			n := strings.TrimSpace(*name)
			if r.BuiltIn && n != r.Name {
				return errf(ErrInvalid, "built-in roles keep their name")
			}
			if n == "" || len(n) > 60 {
				return errf(ErrInvalid, "role name must be 1-60 characters")
			}
			_, err := tx.Exec(ctx, `UPDATE roles SET name=$2 WHERE id=$1`, id, n)
			if db.IsUniqueViolation(err) {
				return errf(ErrConflict, "role %q exists", n)
			}
			if err != nil {
				return err
			}
		}
		if perms != nil {
			if err := validRolePerms(r.Scope, perms); err != nil {
				return err
			}
			if err := setRolePermsTx(ctx, tx, id, perms); err != nil {
				return err
			}
		}
		return s.audit(ctx, tx, p, "role.update", "role", id, "", map[string]any{"name": r.Name, "before": r.Permissions, "after": perms})
	})
	if err != nil {
		return nil, err
	}
	return scanRole(s.DB.QueryRow(ctx, roleSelect+` WHERE r.id = $1::uuid`, id))
}

// DeleteRole removes a role that is not built in and not in use.
func (s *Service) DeleteRole(ctx context.Context, p *Principal, id string) error {
	if err := requireInst(p, InstRoles); err != nil {
		return err
	}
	if !looksLikeUUID(id) {
		return errf(ErrNotFound, "role not found")
	}
	return s.tx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM roles WHERE id=$1 AND NOT built_in`, id)
		if db.IsForeignKeyViolation(err) {
			return errf(ErrConflict, "this role is still in use")
		}
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return errf(ErrInvalid, "built-in roles cannot be deleted")
		}
		return s.audit(ctx, tx, p, "role.delete", "role", id, "", nil)
	})
}
