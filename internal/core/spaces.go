package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/tildoc/tildoc/internal/blob"
	"github.com/tildoc/tildoc/internal/db"
)

// Space is a top-level container.
type Space struct {
	ID           string        `json:"id"`
	Slug         string        `json:"slug"`
	Name         string        `json:"name"`
	OwnerUserID  *string       `json:"owner_user_id"`
	OwnerTeamID  *string       `json:"owner_team_id"`
	TeamName     string        `json:"team_name,omitempty"`
	RootFolderID string        `json:"root_folder_id"`
	Perm         Perm          `json:"perm"` // highest level, for simple UI checks
	Role         string        `json:"role"`
	Caps         []string      `json:"caps"`
	Shared       bool          `json:"shared"`    // access comes from a grant, not ownership/team
	HeldCaps     []string      `json:"held_caps"` // before the space's settings take anything away
	Settings     SpaceSettings `json:"settings"`
	CreatedAt    time.Time     `json:"created_at"`
}

// SpaceSettings are off by default: enabling one adds a way of sharing, stops
// all writing, or stops tildoc's OCR and image-only step.
// Write policies of a space (spaces.write_policy).
const (
	WriteOpen       = "open"
	WriteAppendOnly = "append_only" // new documents only; what exists stays as it is
	WriteReadOnly   = "read_only"   // the data is fixed; tags can still change
	WriteFrozen     = "frozen"      // nothing changes, tags and settings included
)

type SpaceSettings struct {
	AllowLoginLinks  bool   `json:"allow_login_links"`
	AllowPublicLinks bool   `json:"allow_public_links"`
	AllowDropLinks   bool   `json:"allow_drop_links"`
	AllowFileShares  bool   `json:"allow_file_shares"`
	AllowDropIn      bool   `json:"allow_drop_in"`
	WritePolicy      string `json:"write_policy"` // see the Write* constants
	NoOCR            bool   `json:"no_ocr"`
}

const spaceSelect = `
	SELECT s.id::text, s.slug, s.name, s.owner_user_id::text, s.owner_team_id::text, coalesce(t.name,''),
	       f.id::text, s.created_at, space_permissions(s.id, $1::uuid), space_permissions_held(s.id, $1::uuid),
	       s.allow_login_links, s.allow_public_links, s.allow_drop_links, s.allow_file_shares, s.allow_drop_in, s.write_policy, s.no_ocr,
	       NOT (s.owner_user_id IS NOT DISTINCT FROM $1::uuid
	            OR EXISTS (SELECT 1 FROM team_members m WHERE m.team_id = s.owner_team_id AND m.user_id = $1::uuid))
	FROM spaces s
	JOIN entries f ON f.space_id = s.id AND f.parent_id IS NULL AND f.kind = 'folder'
	LEFT JOIN teams t ON t.id = s.owner_team_id`

func scanSpace(row pgx.Row, p *Principal) (*Space, error) {
	sp := &Space{}
	var names, held []string
	st := &sp.Settings
	err := row.Scan(&sp.ID, &sp.Slug, &sp.Name, &sp.OwnerUserID, &sp.OwnerTeamID, &sp.TeamName, &sp.RootFolderID, &sp.CreatedAt, &names, &held,
		&st.AllowLoginLinks, &st.AllowPublicLinks, &st.AllowDropLinks, &st.AllowFileShares, &st.AllowDropIn, &st.WritePolicy, &st.NoOCR, &sp.Shared)
	if err != nil {
		return nil, err
	}
	caps := effectiveCaps(p, sp.ID, capsFromNames(names))
	sp.Perm, sp.Caps = capsPerm(caps), caps.Names()
	heldCaps := effectiveCaps(p, sp.ID, capsFromNames(held))
	sp.HeldCaps = heldCaps.Names()
	sp.Role = roleLabel(heldCaps) // what they are, whatever the settings allow right now
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

// UpdateSpaceSettings replaces the settings of a space. Switching OCR back
// on processes the scans that were left without it.
func (s *Service) UpdateSpaceSettings(ctx context.Context, p *Principal, spaceID string, st SpaceSettings) (*Space, error) {
	if err := requireSession(p); err != nil {
		return nil, err
	}
	switch st.WritePolicy {
	case "", WriteOpen, WriteAppendOnly, WriteReadOnly, WriteFrozen: // "": stays as it is
	default:
		return nil, errf(ErrInvalid, "write_policy must be open, append_only, read_only or frozen")
	}
	if err := s.requireCap(ctx, s.DB, p, spaceID, CapManage); err != nil {
		return nil, err
	}
	err := s.tx(ctx, func(tx pgx.Tx) error {
		var was SpaceSettings
		var hadNoOCR bool
		if err := tx.QueryRow(ctx, `SELECT allow_login_links, allow_public_links, allow_drop_links, allow_file_shares,
			allow_drop_in, write_policy, no_ocr FROM spaces WHERE id=$1 FOR UPDATE`, spaceID).
			Scan(&was.AllowLoginLinks, &was.AllowPublicLinks, &was.AllowDropLinks, &was.AllowFileShares, &was.AllowDropIn, &was.WritePolicy, &was.NoOCR); err != nil {
			return err
		}
		if st.WritePolicy == "" {
			st.WritePolicy = was.WritePolicy
		}
		if was.WritePolicy == WriteFrozen && st.WritePolicy == WriteFrozen && st != was {
			return errf(ErrForbidden, "this space is frozen: unfreeze it to change its settings")
		}
		hadNoOCR = was.NoOCR || was.WritePolicy == WriteFrozen
		if _, err := tx.Exec(ctx, `UPDATE spaces SET allow_login_links=$2, allow_public_links=$3, allow_drop_links=$4, allow_file_shares=$5, allow_drop_in=$6,
			write_policy=$7, no_ocr=$8 WHERE id=$1`, spaceID, st.AllowLoginLinks, st.AllowPublicLinks, st.AllowDropLinks, st.AllowFileShares, st.AllowDropIn,
			st.WritePolicy, st.NoOCR); err != nil {
			return err
		}
		if err := s.audit(ctx, tx, p, "space.settings", "space", spaceID, spaceID, map[string]any{"settings": st}); err != nil {
			return err
		}
		if !hadNoOCR || st.NoOCR || st.WritePolicy == WriteFrozen {
			return nil
		}
		rows, err := tx.Query(ctx, `SELECT DISTINCT encode(v.sha256, 'hex') FROM document_versions v
			JOIN documents d ON d.id = v.document_id JOIN blob_processing bp ON bp.sha256 = v.sha256
			WHERE d.space_id = $1 AND bp.status = 'done' AND bp.kind IN ('pdf-scanned', 'pdf-mixed', 'pdf-ocred')`, spaceID)
		if err != nil {
			return err
		}
		shas, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return err
		}
		for _, sha := range shas {
			if err := enqueueProcess(ctx, tx, sha, true); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.GetSpace(ctx, p, spaceID)
}

// OCRAllowed: content is stored and processed once, so tildoc's OCR runs if
// at least one space holding this content allows it.
func (s *Service) OCRAllowed(ctx context.Context, sha blob.SHA256) (bool, error) {
	var ok bool
	err := s.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM document_versions v JOIN documents d ON d.id = v.document_id
		JOIN spaces s ON s.id = d.space_id WHERE v.sha256 = $1 AND NOT s.no_ocr AND s.write_policy <> 'frozen')`, sha[:]).Scan(&ok)
	return ok, err
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
			q := `SELECT id::text FROM users WHERE username = lower($1) AND status = 'active'`
			if looksLikeUUID(toUser) {
				q = `SELECT id::text FROM users WHERE id = $1::uuid AND status = 'active'`
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
		if err := s.audit(ctx, tx, p, "space.transfer", "space", spaceID, spaceID, map[string]any{"from_user": fromUser, "from_team": fromTeam, "to_user": toU, "to_team": toT, "keep_role": keepRole}); err != nil {
			return err
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
		tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM spaces WHERE slug=$1)`, cand).Scan(&exists) //nolint:errcheck // the unique index decides in the end
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
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM documents WHERE space_id=$1 AND deleted_at IS NULL)`, sp.ID).Scan(&live); err != nil {
			return err
		}
		if live {
			return errf(ErrNotEmpty, "space still contains documents")
		}
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM spaces WHERE owner_user_id=$1`, p.UserID).Scan(&n); err != nil {
			return err
		}
		if sp.OwnerUserID != nil && n <= 1 {
			return errf(ErrInvalid, "cannot delete your last personal space")
		}
		_, err := tx.Exec(ctx, `DELETE FROM spaces WHERE id=$1`, sp.ID)
		return err
	})
}
