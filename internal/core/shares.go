package core

import (
	"context"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/tildoc/tildoc/internal/blob"
)

// ShareSessionTTL is how long a browser stays in an opened link.
const ShareSessionTTL = 12 * time.Hour

// ShareLink gives access to a space or a file by URL.
type ShareLink struct {
	ID          string     `json:"id"`
	SpaceID     *string    `json:"space_id"`
	DocumentID  *string    `json:"document_id"`
	FolderID    *string    `json:"folder_id"`
	Kind        string     `json:"kind"`   // login, public, drop
	Preset      string     `json:"preset"` // restricted, view, drop
	Label       string     `json:"label"`
	HasPassword bool       `json:"has_password"`
	ExpiresAt   *time.Time `json:"expires_at"`
	CreatedBy   string     `json:"created_by"`
	CreatedAt   time.Time  `json:"created_at"`
	RevokedAt   *time.Time `json:"revoked_at"`
	creatorID   string
}

const linkSelect = `SELECT l.id::text, l.space_id::text, l.document_id::text, l.folder_id::text, l.kind, l.preset, l.label,
	l.password_hash IS NOT NULL, l.expires_at, u.username, l.created_at, l.revoked_at, l.created_by::text
	FROM share_links l JOIN users u ON u.id = l.created_by`

func scanLink(row pgx.Row) (*ShareLink, error) {
	l := &ShareLink{}
	err := row.Scan(&l.ID, &l.SpaceID, &l.DocumentID, &l.FolderID, &l.Kind, &l.Preset, &l.Label, &l.HasPassword, &l.ExpiresAt, &l.CreatedBy, &l.CreatedAt, &l.RevokedAt, &l.creatorID)
	return l, err
}

// NewShareLink describes a link to create.
type NewShareLink struct {
	SpaceID    string     `json:"space_id"`
	DocumentID string     `json:"document_id"`
	FolderID   string     `json:"folder_id"`
	Kind       string     `json:"kind"`
	Preset     string     `json:"preset"`
	Label      string     `json:"label"`
	Password   string     `json:"password"`
	ExpiresAt  *time.Time `json:"expires_at"`
}

// presetNeed is what the link's creator must hold, now and on every use.
func presetNeed(preset string) Cap {
	switch preset {
	case "restricted":
		return CapView
	case "view":
		return CapView | CapDownload
	case "drop":
		return CapWrite
	}
	return 0
}

func (s *Service) publicLinksEnabled(ctx context.Context) (bool, error) {
	var on bool
	err := s.DB.QueryRow(ctx, `SELECT public_links FROM instance_settings`).Scan(&on)
	return on, err
}

// CreateShareLink makes a link; its token is returned only here.
func (s *Service) CreateShareLink(ctx context.Context, p *Principal, n NewShareLink) (*ShareLink, string, error) {
	if err := requireSession(p); err != nil {
		return nil, "", err
	}
	if (n.SpaceID == "") == (n.DocumentID == "") {
		return nil, "", errf(ErrInvalid, "a link is for either a space or a file")
	}
	if n.Kind == "drop" {
		n.Preset = "drop"
	}
	if presetNeed(n.Preset) == 0 || (n.Kind == "drop") != (n.Preset == "drop") {
		return nil, "", errf(ErrInvalid, "invalid link type")
	}
	if n.Kind == "public" || n.Kind == "drop" {
		on, err := s.publicLinksEnabled(ctx)
		if err != nil {
			return nil, "", err
		}
		if !on {
			return nil, "", errf(ErrForbidden, "public links are turned off on this server")
		}
	}
	if n.ExpiresAt != nil && !n.ExpiresAt.After(s.now()) {
		return nil, "", errf(ErrInvalid, "the expiry must be in the future")
	}
	var perm Cap
	switch {
	case n.SpaceID != "" && n.Kind == "login":
		perm = CapShareSpaceLogin
	case n.SpaceID != "" && n.Kind == "public":
		perm = CapShareSpacePublic
	case n.SpaceID != "" && n.Kind == "drop":
		perm = CapShareSpaceDrop
	case n.DocumentID != "" && n.Kind == "login":
		perm = CapShareFileLogin
	case n.DocumentID != "" && n.Kind == "public":
		perm = CapShareFilePublic
	default:
		return nil, "", errf(ErrInvalid, "this link type is not available here")
	}
	var folderID *string
	if n.SpaceID != "" {
		if err := s.requireCap(ctx, s.DB, p, n.SpaceID, perm|presetNeed(n.Preset)); err != nil {
			return nil, "", err
		}
		if n.Kind == "drop" && n.FolderID != "" {
			f, err := s.GetFolder(ctx, p, n.FolderID)
			if err != nil {
				return nil, "", err
			}
			if f.SpaceID != n.SpaceID {
				return nil, "", errf(ErrInvalid, "the folder is not in this space")
			}
			folderID = &n.FolderID
		}
	} else {
		spaceID, err := s.docSpace(ctx, n.DocumentID)
		if err != nil {
			return nil, "", err
		}
		if err := s.requireDocCap(ctx, s.DB, p, n.DocumentID, spaceID, perm|presetNeed(n.Preset)); err != nil {
			return nil, "", err
		}
	}
	var pwHash *string
	if n.Password != "" {
		if len(n.Password) > 1024 {
			return nil, "", errf(ErrInvalid, "password too long")
		}
		h, err := HashPassword(n.Password)
		if err != nil {
			return nil, "", err
		}
		pwHash = &h
	}
	token := randomToken(32)
	var id string
	if err := s.DB.QueryRow(ctx, `INSERT INTO share_links(token_hash, space_id, document_id, folder_id, kind, preset, label, password_hash, expires_at, created_by)
		VALUES($1,$2::uuid,$3::uuid,$4::uuid,$5,$6,$7,$8,$9,$10) RETURNING id::text`,
		tokenHash(token), nonEmpty(n.SpaceID), nonEmpty(n.DocumentID), folderID, n.Kind, n.Preset, strings.TrimSpace(n.Label), pwHash, n.ExpiresAt, p.UserID).Scan(&id); err != nil {
		return nil, "", err
	}
	l, err := scanLink(s.DB.QueryRow(ctx, linkSelect+` WHERE l.id = $1`, id))
	return l, token, err
}

// ListShareLinks lists the active links of a space or a file.
func (s *Service) ListShareLinks(ctx context.Context, p *Principal, spaceID, docID string) ([]ShareLink, error) {
	if err := requireSession(p); err != nil {
		return nil, err
	}
	where, arg := ` WHERE l.space_id = $1 AND l.revoked_at IS NULL`, spaceID
	if docID != "" {
		where, arg = ` WHERE l.document_id = $1 AND l.revoked_at IS NULL`, docID
		sp, err := s.docSpace(ctx, docID)
		if err != nil {
			return nil, err
		}
		caps, err := s.docCaps(ctx, s.DB, p, docID, sp)
		if err != nil {
			return nil, err
		}
		if caps&capShare == 0 {
			return nil, errf(ErrForbidden, "not allowed to see the links")
		}
	} else {
		caps, err := s.spaceCaps(ctx, s.DB, p, spaceID)
		if err != nil {
			return nil, err
		}
		if caps&capShare == 0 {
			return nil, errf(ErrForbidden, "not allowed to see the links")
		}
	}
	rows, err := s.DB.Query(ctx, linkSelect+where+` ORDER BY l.created_at DESC`, arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ShareLink{}
	for rows.Next() {
		l, err := scanLink(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *l)
	}
	return out, rows.Err()
}

// RevokeShareLink ends a link: its creator, or anyone who may manage the space.
func (s *Service) RevokeShareLink(ctx context.Context, p *Principal, id string) error {
	if err := requireSession(p); err != nil {
		return err
	}
	if !looksLikeUUID(id) {
		return errf(ErrNotFound, "link not found")
	}
	l, err := scanLink(s.DB.QueryRow(ctx, linkSelect+` WHERE l.id = $1 AND l.revoked_at IS NULL`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return errf(ErrNotFound, "link not found")
	}
	if err != nil {
		return err
	}
	if l.creatorID != p.UserID {
		spaceID := ""
		if l.SpaceID != nil {
			spaceID = *l.SpaceID
		} else if spaceID, err = s.docSpace(ctx, *l.DocumentID); err != nil {
			return err
		}
		if err := s.requireCap(ctx, s.DB, p, spaceID, CapManage); err != nil {
			return err
		}
	}
	_, err = s.DB.Exec(ctx, `UPDATE share_links SET revoked_at = now() WHERE id = $1`, id)
	return err
}

// ErrSharePassword: the link needs a (correct) password.
var ErrSharePassword = errors.New("password required")

// ErrShareLogin: the link is for signed-in users.
var ErrShareLogin = errors.New("sign in to open this link")

// ShareAccess is an opened, currently valid link.
type ShareAccess struct {
	Link *ShareLink
	can  Cap
}

// access validates a link and works out what it may still do: never more
// than its creator holds right now.
func (s *Service) access(ctx context.Context, l *ShareLink) (*ShareAccess, error) {
	gone := errf(ErrNotFound, "this link is no longer available")
	if l.RevokedAt != nil || (l.ExpiresAt != nil && !l.ExpiresAt.After(s.now())) {
		return nil, gone
	}
	if l.Kind != "login" {
		on, err := s.publicLinksEnabled(ctx)
		if err != nil {
			return nil, err
		}
		if !on {
			return nil, gone
		}
	}
	// the space must (still) allow this kind of link
	var allowed bool
	if err := s.DB.QueryRow(ctx, `SELECT CASE $2 WHEN 'login' THEN s.allow_login_links WHEN 'public' THEN s.allow_public_links ELSE s.allow_drop_links END
		FROM spaces s WHERE s.id = coalesce($1::uuid, (SELECT space_id FROM documents WHERE id = $3::uuid))`,
		l.SpaceID, l.Kind, l.DocumentID).Scan(&allowed); err != nil || !allowed {
		return nil, gone
	}
	creator, err := s.principalOf(ctx, l.creatorID)
	if err != nil {
		return nil, gone
	}
	var held Cap
	if l.SpaceID != nil {
		held, err = s.spaceCaps(ctx, s.DB, creator, *l.SpaceID)
	} else {
		var spaceID string
		if spaceID, err = s.docSpace(ctx, *l.DocumentID); err != nil {
			return nil, gone
		}
		held, err = s.docCaps(ctx, s.DB, creator, *l.DocumentID, spaceID)
	}
	if err != nil {
		return nil, err
	}
	need := presetNeed(l.Preset)
	if !held.Has(need) {
		return nil, gone
	}
	return &ShareAccess{Link: l, can: need}, nil
}

// OpenShare exchanges a link's token (and password) for a browser session.
// user is the signed-in user, if any. An existing session skips the password.
func (s *Service) OpenShare(ctx context.Context, token, password, existing string, user *Principal) (*ShareAccess, string, error) {
	var hash *string
	l, err := func() (*ShareLink, error) {
		var linkID string
		err := s.DB.QueryRow(ctx, `SELECT id::text, password_hash FROM share_links WHERE token_hash = $1`, tokenHash(token)).Scan(&linkID, &hash)
		if err != nil {
			return nil, err
		}
		return scanLink(s.DB.QueryRow(ctx, linkSelect+` WHERE l.id = $1`, linkID))
	}()
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", errf(ErrNotFound, "this link is no longer available")
	}
	if err != nil {
		return nil, "", err
	}
	a, err := s.access(ctx, l)
	if err != nil {
		return nil, "", err
	}
	if l.Kind == "login" && (user == nil || user.Via != "session") {
		return a, "", ErrShareLogin
	}
	var uid *string
	if l.Kind == "login" {
		uid = &user.UserID
	}
	if existing != "" {
		var ok bool
		s.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM share_sessions WHERE token_hash=$1 AND link_id=$2 AND expires_at > now()
			AND user_id IS NOT DISTINCT FROM $3::uuid)`, tokenHash(existing), l.ID, uid).Scan(&ok)
		if ok {
			return a, existing, nil
		}
	}
	if hash != nil && !VerifyPassword(password, *hash) {
		return a, "", ErrSharePassword
	}
	sess := randomToken(32)
	if _, err := s.DB.Exec(ctx, `INSERT INTO share_sessions(token_hash, link_id, user_id, expires_at) VALUES($1,$2,$3::uuid,$4)`,
		tokenHash(sess), l.ID, uid, s.now().Add(ShareSessionTTL)); err != nil {
		return nil, "", err
	}
	return a, sess, nil
}

// ResolveShare checks a browser session on every request.
func (s *Service) ResolveShare(ctx context.Context, linkID, sess string, user *Principal) (*ShareAccess, error) {
	gone := errf(ErrNotFound, "this link is no longer available")
	if !looksLikeUUID(linkID) || sess == "" {
		return nil, gone
	}
	var owner *string
	err := s.DB.QueryRow(ctx, `SELECT user_id::text FROM share_sessions WHERE token_hash=$1 AND link_id=$2 AND expires_at > now()`, tokenHash(sess), linkID).Scan(&owner)
	if err != nil {
		return nil, gone
	}
	l, err := scanLink(s.DB.QueryRow(ctx, linkSelect+` WHERE l.id = $1`, linkID))
	if err != nil {
		return nil, gone
	}
	if l.Kind == "login" && (user == nil || owner == nil || *owner != user.UserID) {
		return nil, gone
	}
	return s.access(ctx, l)
}

// PruneShareSessions deletes expired link sessions (housekeeping).
func (s *Service) PruneShareSessions(ctx context.Context) error {
	_, err := s.DB.Exec(ctx, `DELETE FROM share_sessions WHERE expires_at < now()`)
	return err
}

// ShareInfo is what a link's page shows.
type ShareInfo struct {
	ID           string     `json:"id"`
	Kind         string     `json:"kind"`
	Preset       string     `json:"preset"`
	CanDownload  bool       `json:"can_download"`
	Name         string     `json:"name"`
	RootFolderID string     `json:"root_folder_id,omitempty"`
	File         *ShareFile `json:"file,omitempty"`
}

// ShareFile is a file as a link's visitor sees it.
type ShareFile struct {
	LinkID     string    `json:"link_id,omitempty"`
	Name       string    `json:"name"`
	Size       int64     `json:"size"`
	MIME       string    `json:"mime"`
	ModifiedAt time.Time `json:"modified_at"`
}

// ShareFolderItem is a subfolder.
type ShareFolderItem struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ShareListing is a folder as a link's visitor sees it.
type ShareListing struct {
	ParentID string            `json:"parent_id,omitempty"`
	Name     string            `json:"name"`
	Folders  []ShareFolderItem `json:"folders"`
	Files    []ShareFile       `json:"files"`
}

// Info describes the link's target. Nothing else about the server is shown.
func (s *Service) ShareInfo(ctx context.Context, a *ShareAccess) (*ShareInfo, error) {
	l := a.Link
	info := &ShareInfo{ID: l.ID, Kind: l.Kind, Preset: l.Preset, CanDownload: a.can.Has(CapDownload)}
	if l.DocumentID != nil {
		d, err := s.GetDocument(ctx, SystemPrincipal, *l.DocumentID)
		if err != nil || d.DeletedAt != nil {
			return nil, errf(ErrNotFound, "this link is no longer available")
		}
		info.Name = d.Name
		info.File = &ShareFile{Name: d.Name, Size: d.Size, MIME: d.MIME, ModifiedAt: d.UpdatedAt}
		return info, nil
	}
	sp, err := s.GetSpace(ctx, SystemPrincipal, *l.SpaceID)
	if err != nil {
		return nil, errf(ErrNotFound, "this link is no longer available")
	}
	info.Name = sp.Name
	if l.Kind == "drop" {
		if l.FolderID != nil {
			f, err := s.folderInfo(ctx, s.DB, *l.FolderID)
			if err == nil && f.Name != "" {
				info.Name = f.Name
			}
		}
		return info, nil // no browsing, no listing
	}
	info.RootFolderID = sp.RootFolderID
	return info, nil
}

// ShareFolder lists a folder of a space link.
func (s *Service) ShareFolder(ctx context.Context, a *ShareAccess, folderID string) (*ShareListing, error) {
	notFound := errf(ErrNotFound, "folder not found")
	if a.Link.SpaceID == nil || a.Link.Kind == "drop" || !looksLikeUUID(folderID) {
		return nil, notFound
	}
	folder, subfolders, files, err := s.ListFolder(ctx, SystemPrincipal, folderID)
	if err != nil || folder.SpaceID != *a.Link.SpaceID {
		return nil, notFound
	}
	out := &ShareListing{Name: folder.Name, Folders: []ShareFolderItem{}, Files: []ShareFile{}}
	if folder.ParentID != nil {
		out.ParentID = *folder.ParentID
	}
	for _, f := range subfolders {
		out.Folders = append(out.Folders, ShareFolderItem{ID: f.ID, Name: f.Name})
	}
	for _, f := range files {
		out.Files = append(out.Files, ShareFile{LinkID: f.LinkID, Name: f.Name, Size: f.Size, MIME: f.MIME, ModifiedAt: f.ModifiedAt})
	}
	return out, nil
}

// OpenShareContent opens a file of a link. fileLink names the file in a
// space link; a file link ignores it. inline: for display in the browser.
func (s *Service) OpenShareContent(ctx context.Context, a *ShareAccess, fileLink string, inline bool) (blob.File, *ContentInfo, error) {
	notFound := errf(ErrNotFound, "file not found")
	if a.Link.Kind == "drop" {
		return nil, nil, notFound
	}
	docID := ""
	if a.Link.DocumentID != nil {
		docID = *a.Link.DocumentID
	} else {
		if !looksLikeUUID(fileLink) {
			return nil, nil, notFound
		}
		f, err := s.GetFile(ctx, SystemPrincipal, fileLink)
		if err != nil || f.SpaceID != *a.Link.SpaceID {
			return nil, nil, notFound
		}
		docID = f.DocumentID
	}
	if !inline && !a.can.Has(CapDownload) {
		return nil, nil, errf(ErrForbidden, "this link does not allow downloads")
	}
	f, ci, err := s.OpenDocument(ctx, SystemPrincipal, docID, 0, "", true)
	if err != nil {
		return nil, nil, notFound
	}
	if !InlineSafe(ci.MIME) && !a.can.Has(CapDownload) {
		f.Close()
		return nil, nil, errf(ErrForbidden, "this file type cannot be shown here")
	}
	return f, ci, nil
}

// ShareUpload stores an upload through a drop link: always a new file, never
// touching or revealing what is already there.
func (s *Service) ShareUpload(ctx context.Context, a *ShareAccess, name string, r io.Reader) error {
	if a.Link.Kind != "drop" {
		return errf(ErrForbidden, "this link does not take uploads")
	}
	name, err := NormalizeName(name)
	if err != nil {
		return err
	}
	creator, err := s.principalOf(ctx, a.Link.creatorID)
	if err != nil {
		return errf(ErrNotFound, "this link is no longer available")
	}
	var folder *FolderInfo
	if a.Link.FolderID != nil {
		folder, err = s.folderInfo(ctx, s.DB, *a.Link.FolderID)
	} else {
		var sp *Space
		if sp, err = s.GetSpace(ctx, SystemPrincipal, *a.Link.SpaceID); err == nil {
			folder, err = s.folderInfo(ctx, s.DB, sp.RootFolderID)
		}
	}
	if err != nil {
		return errf(ErrNotFound, "this link is no longer available")
	}
	st, err := s.Blobs.Stage(r, s.Cfg.MaxUploadBytes)
	if errors.Is(err, blob.ErrTooLarge) {
		return errf(ErrTooLarge, "file exceeds the upload limit")
	}
	if err != nil {
		return err
	}
	_, err = s.createStaged(ctx, creator, folder, name, st, "drop", true)
	return err
}
