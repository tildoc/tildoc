package core

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/tildoc/tildoc/internal/db"
)

// Cap is a set of capabilities on a space. Roles are named capability sets;
// every permission check goes through spaceCaps.
type Cap uint16

// Capabilities.
const (
	CapView     Cap = 1 << iota // list, preview, search, read metadata
	CapDownload                 // read file content
	CapExport                   // bulk export (ZIP, bundle)
	CapWrite                    // upload, edit, move within the space, tag
	CapDelete                   // delete, empty trash, move out of the space
	CapShare                    // grant access to other users and teams
	CapManage                   // rename and delete the space
)

var capNames = []struct {
	c Cap
	n string
}{{CapView, "view"}, {CapDownload, "download"}, {CapExport, "export"}, {CapWrite, "write"},
	{CapDelete, "delete"}, {CapShare, "share"}, {CapManage, "manage"}}

const capRead = CapView | CapDownload | CapExport

// roleCaps maps roles to capabilities.
func roleCaps(role string) Cap {
	switch role {
	case "owner":
		return capRead | CapWrite | CapDelete | CapShare | CapManage
	case "admin":
		return capRead | CapWrite | CapDelete | CapShare
	case "member":
		return capRead | CapWrite
	case "viewer":
		return capRead
	}
	return 0
}

// Has reports whether all of want are present.
func (c Cap) Has(want Cap) bool { return c&want == want }

// Names lists the capabilities (for API clients).
func (c Cap) Names() []string {
	out := []string{}
	for _, x := range capNames {
		if c.Has(x.c) {
			out = append(out, x.n)
		}
	}
	return out
}

// effectiveCaps applies access-key restrictions to a role's capabilities.
func effectiveCaps(p *Principal, spaceID, role string) Cap {
	if p.Via == "system" {
		return roleCaps("owner")
	}
	c := roleCaps(role)
	if p.ScopeSpaceID != "" && p.ScopeSpaceID != spaceID {
		return 0
	}
	if p.ReadOnly {
		c &= capRead
	}
	return c
}

// permCap maps the legacy permission levels used by callers to capabilities.
func permCap(want Perm) Cap {
	switch want {
	case PermRead:
		return CapView
	case PermWrite:
		return CapWrite
	case PermDelete:
		return CapDelete
	case PermManage:
		return CapManage
	}
	return 0
}

// capsPerm summarizes capabilities as the highest level (for the UI).
func capsPerm(c Cap) Perm {
	switch {
	case c.Has(CapManage):
		return PermManage
	case c.Has(CapDelete):
		return PermDelete
	case c.Has(CapWrite):
		return PermWrite
	case c.Has(CapView):
		return PermRead
	}
	return PermNone
}

// spaceCaps returns the caller's capabilities on a space.
func (s *Service) spaceCaps(ctx context.Context, q db.Querier, p *Principal, spaceID string) (Cap, error) {
	if p == nil {
		return 0, ErrUnauthorized
	}
	if !looksLikeUUID(spaceID) {
		return 0, errf(ErrNotFound, "space not found")
	}
	var role *string
	err := q.QueryRow(ctx, `SELECT space_role(id, $2::uuid) FROM spaces WHERE id=$1`, spaceID, p.UserID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, errf(ErrNotFound, "space not found")
	}
	if err != nil {
		return 0, err
	}
	r := ""
	if role != nil {
		r = *role
	}
	return effectiveCaps(p, spaceID, r), nil
}

// requireCap checks capabilities on a space. Spaces the caller cannot view
// are reported as not found (no existence leak).
func (s *Service) requireCap(ctx context.Context, q db.Querier, p *Principal, spaceID string, want Cap) error {
	got, err := s.spaceCaps(ctx, q, p, spaceID)
	if err != nil {
		return err
	}
	if !got.Has(CapView) {
		return errf(ErrNotFound, "space not found")
	}
	if !got.Has(want) {
		return errf(ErrForbidden, "insufficient permission on space")
	}
	return nil
}
