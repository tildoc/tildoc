package core

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/tildoc/tildoc/internal/db"
)

// Cap is a set of permissions on a space or document. The catalogue is the
// `permissions` table; roles and grants are data. Every check goes through
// spaceCaps or docCaps.
type Cap uint32

// Space permissions.
const (
	CapView     Cap = 1 << iota // list, preview, search, read metadata
	CapDownload                 // get the file itself
	CapExport                   // bulk export (ZIP, bundle)
	CapWrite                    // upload, edit, move within the space, tag
	CapMove                     // move out of the space
	CapDelete                   // move to the trash
	CapPurge                    // delete for good, empty the trash
	CapManage                   // rename and delete the space
	CapTransfer                 // hand the space to another owner
	CapShareSpacePeople
	CapShareSpaceLogin
	CapShareSpacePublic
	CapShareSpaceDrop
	CapShareFilePeople
	CapShareFileLogin
	CapShareFilePublic
)

var capNames = []struct {
	c Cap
	n string
}{{CapView, "view"}, {CapDownload, "download"}, {CapExport, "export"}, {CapWrite, "write"}, {CapMove, "move"},
	{CapDelete, "delete"}, {CapPurge, "purge"}, {CapManage, "manage"}, {CapTransfer, "transfer"},
	{CapShareSpacePeople, "share.space.people"}, {CapShareSpaceLogin, "share.space.login_link"},
	{CapShareSpacePublic, "share.space.public_link"}, {CapShareSpaceDrop, "share.space.drop_link"},
	{CapShareFilePeople, "share.file.people"}, {CapShareFileLogin, "share.file.login_link"}, {CapShareFilePublic, "share.file.public_link"}}

const (
	capRead  = CapView | CapDownload | CapExport
	capShare = CapShareSpacePeople | CapShareSpaceLogin | CapShareSpacePublic | CapShareSpaceDrop |
		CapShareFilePeople | CapShareFileLogin | CapShareFilePublic
	capAll = capRead | CapWrite | CapMove | CapDelete | CapPurge | CapManage | CapTransfer | capShare
	// capFile: what a grant on a single file can give
	capFile = CapView | CapDownload | CapWrite
)

// Has reports whether all of want are present.
func (c Cap) Has(want Cap) bool { return c&want == want }

// Names lists the permissions (for API clients).
func (c Cap) Names() []string {
	out := []string{}
	for _, x := range capNames {
		if c.Has(x.c) {
			out = append(out, x.n)
		}
	}
	return out
}

func capsFromNames(names []string) Cap {
	var c Cap
	for _, n := range names {
		for _, x := range capNames {
			if x.n == n {
				c |= x.c
			}
		}
	}
	return c
}

// InstCap is a set of instance permissions. They never give access to documents.
type InstCap uint16

// Instance permissions.
const (
	InstUsers InstCap = 1 << iota
	InstTeams
	InstTeamsCreate
	InstRoles
	InstSpacesCreate
	InstSpacesRecover
	InstTags
	InstBackups
	InstSettings
)

var instNames = []struct {
	c InstCap
	n string
}{{InstUsers, "users.manage"}, {InstTeams, "teams.manage"}, {InstTeamsCreate, "teams.create"}, {InstRoles, "roles.manage"},
	{InstSpacesCreate, "spaces.create"}, {InstSpacesRecover, "spaces.recover"}, {InstTags, "tags.manage"},
	{InstBackups, "backups.manage"}, {InstSettings, "settings.manage"}}

func instFromNames(names []string) InstCap {
	var c InstCap
	for _, n := range names {
		for _, x := range instNames {
			if x.n == n {
				c |= x.c
			}
		}
	}
	return c
}

// Names lists the instance permissions.
func (c InstCap) Names() []string {
	out := []string{}
	for _, x := range instNames {
		if c&x.c == x.c {
			out = append(out, x.n)
		}
	}
	return out
}

// Can reports an instance permission. Background jobs may do everything.
func (p *Principal) Can(want InstCap) bool {
	return p != nil && (p.Via == "system" || p.Inst&want == want)
}

func requireInst(p *Principal, want InstCap) error {
	if p == nil {
		return ErrUnauthorized
	}
	if !p.Can(want) {
		return errf(ErrForbidden, "not allowed")
	}
	return nil
}

// effectiveCaps applies key restrictions to what a user holds.
func effectiveCaps(p *Principal, spaceID string, c Cap) Cap {
	if p.Via == "system" {
		return capAll
	}
	if p.ScopeSpaceID != "" && p.ScopeSpaceID != spaceID {
		return 0
	}
	if p.ReadOnly {
		c &= capRead
	}
	if p.Via == "apikey" {
		c &= p.KeyCaps
		if len(p.KeySpaces) > 0 {
			found := false
			for _, id := range p.KeySpaces {
				found = found || id == spaceID
			}
			if !found {
				return 0
			}
		}
	}
	return c
}

// permCap maps the permission levels used by many callers to permissions.
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

// capsPerm summarizes permissions as the highest level (for the UI).
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

// roleLabel names a permission set for display: a built-in role if it matches.
func roleLabel(c Cap) string {
	switch c {
	case 0:
		return ""
	case capAll:
		return "owner"
	case CapView:
		return "restricted_viewer"
	case capRead:
		return "viewer"
	case capRead | CapWrite:
		return "editor"
	case capAll &^ CapTransfer:
		return "manager"
	}
	return "custom"
}

// spaceCaps returns the caller's permissions on a space.
func (s *Service) spaceCaps(ctx context.Context, q db.Querier, p *Principal, spaceID string) (Cap, error) {
	if p == nil {
		return 0, ErrUnauthorized
	}
	if !looksLikeUUID(spaceID) {
		return 0, errf(ErrNotFound, "space not found")
	}
	var names []string
	err := q.QueryRow(ctx, `SELECT space_permissions(id, $2::uuid) FROM spaces WHERE id=$1`, spaceID, p.userRef()).Scan(&names)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, errf(ErrNotFound, "space not found")
	}
	if err != nil {
		return 0, err
	}
	return effectiveCaps(p, spaceID, capsFromNames(names)), nil
}

// docCaps returns the caller's permissions on a document: those of its space
// plus grants on the document itself.
func (s *Service) docCaps(ctx context.Context, q db.Querier, p *Principal, docID, spaceID string) (Cap, error) {
	if p == nil {
		return 0, ErrUnauthorized
	}
	var names []string
	if err := q.QueryRow(ctx, `SELECT document_permissions($1::uuid, $2::uuid)`, docID, p.userRef()).Scan(&names); err != nil {
		return 0, err
	}
	return effectiveCaps(p, spaceID, capsFromNames(names)), nil
}

// requireCap checks permissions on a space. Spaces the caller cannot view
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

// requireDocCap is requireCap for one document; grants on it count.
func (s *Service) requireDocCap(ctx context.Context, q db.Querier, p *Principal, docID, spaceID string, want Cap) error {
	got, err := s.docCaps(ctx, q, p, docID, spaceID)
	if err != nil {
		return err
	}
	if !got.Has(CapView) {
		return errf(ErrNotFound, "document not found")
	}
	if !got.Has(want) {
		return errf(ErrForbidden, "insufficient permission on document")
	}
	return nil
}
