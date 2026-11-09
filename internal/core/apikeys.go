package core

import (
	"context"
	"crypto/subtle"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/tildoc/tildoc/internal/blob"
)

// APIKeyPrefix starts every API key, so secret scanners can find leaked ones.
const APIKeyPrefix = "tildoc_"

// APIKey is REST access on behalf of a user. It never exceeds the user's
// current rights and never carries instance permissions.
type APIKey struct {
	ID          string     `json:"id"`
	Label       string     `json:"label"`
	Permissions []string   `json:"permissions"`
	SpaceIDs    []string   `json:"space_ids"`
	AllSpaces   bool       `json:"all_spaces"`
	FolderID    *string    `json:"folder_id"` // Drop-In keys: where uploads land
	ExpiresAt   *time.Time `json:"expires_at"`
	CreatedAt   time.Time  `json:"created_at"`
	LastUsedAt  *time.Time `json:"last_used_at"`
	RevokedAt   *time.Time `json:"revoked_at"`
}

const apiKeySelect = `SELECT k.id::text, k.label, k.folder_id::text, k.all_spaces, k.expires_at, k.created_at, k.last_used_at, k.revoked_at,
	ARRAY(SELECT permission FROM api_key_permissions WHERE key_id = k.id ORDER BY permission),
	ARRAY(SELECT space_id::text FROM api_key_spaces WHERE key_id = k.id ORDER BY space_id)
	FROM api_keys k`

func scanAPIKey(row pgx.Row) (*APIKey, error) {
	k := &APIKey{}
	err := row.Scan(&k.ID, &k.Label, &k.FolderID, &k.AllSpaces, &k.ExpiresAt, &k.CreatedAt, &k.LastUsedAt, &k.RevokedAt, &k.Permissions, &k.SpaceIDs)
	return k, err
}

// CreateAPIKey issues a key; the token is returned only here.
func (s *Service) CreateAPIKey(ctx context.Context, p *Principal, label string, perms, spaces []string, expires *time.Time) (*APIKey, string, error) {
	if len(perms) == 0 {
		return nil, "", errf(ErrInvalid, "choose at least one permission")
	}
	for _, x := range perms {
		if x == "drop_in" {
			return nil, "", errf(ErrInvalid, "Drop-In is a key type of its own")
		}
	}
	return s.createKey(ctx, p, label, perms, spaces, "", expires)
}

// CreateDropInKey issues a key that can only upload into one space (and,
// if given, one folder of it).
func (s *Service) CreateDropInKey(ctx context.Context, p *Principal, label, spaceID, folderID string, expires *time.Time) (*APIKey, string, error) {
	if err := requireSession(p); err != nil {
		return nil, "", err
	}
	if err := s.requireCap(ctx, s.DB, p, spaceID, CapDropIn); err != nil {
		return nil, "", err
	}
	if folderID != "" {
		f, err := s.GetFolder(ctx, p, folderID)
		if err != nil {
			return nil, "", err
		}
		if f.SpaceID != spaceID {
			return nil, "", errf(ErrInvalid, "the folder is not in this space")
		}
	}
	return s.createKey(ctx, p, label, []string{"drop_in"}, []string{spaceID}, folderID, expires)
}

func (s *Service) createKey(ctx context.Context, p *Principal, label string, perms, spaces []string, folderID string, expires *time.Time) (*APIKey, string, error) {
	if err := requireSession(p); err != nil {
		return nil, "", err
	}
	if err := validRolePerms("space", perms); err != nil {
		return nil, "", err
	}
	if expires != nil && !expires.After(s.now()) {
		return nil, "", errf(ErrInvalid, "the expiry must be in the future")
	}
	for _, id := range spaces {
		if err := s.requireCap(ctx, s.DB, p, id, CapView); err != nil {
			return nil, "", err
		}
	}
	secret := randomToken(32)
	var id string
	err := s.tx(ctx, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `INSERT INTO api_keys(user_id, secret_hash, label, folder_id, all_spaces, expires_at) VALUES($1,$2,$3,$4::uuid,$5,$6) RETURNING id::text`,
			p.UserID, tokenHash(secret), strings.TrimSpace(label), nonEmpty(folderID), len(spaces) == 0, expires).Scan(&id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO api_key_permissions(key_id, permission) SELECT $1, unnest($2::text[])`, id, perms); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO api_key_spaces(key_id, space_id) SELECT $1, unnest($2::uuid[])`, id, spaces)
		return err
	})
	if err != nil {
		return nil, "", err
	}
	k, err := scanAPIKey(s.DB.QueryRow(ctx, apiKeySelect+` WHERE k.id = $1`, id))
	return k, APIKeyPrefix + id + "_" + secret, err
}

// ListAPIKeys lists the caller's keys.
func (s *Service) ListAPIKeys(ctx context.Context, p *Principal) ([]APIKey, error) {
	if err := requireSession(p); err != nil {
		return nil, err
	}
	rows, err := s.DB.Query(ctx, apiKeySelect+` WHERE k.user_id = $1 ORDER BY k.created_at DESC`, p.UserID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []APIKey{}
	for rows.Next() {
		k, err := scanAPIKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *k)
	}
	return out, rows.Err()
}

// RevokeAPIKey ends one of the caller's keys.
func (s *Service) RevokeAPIKey(ctx context.Context, p *Principal, id string) error {
	if err := requireSession(p); err != nil {
		return err
	}
	if !looksLikeUUID(id) {
		return errf(ErrNotFound, "API key not found")
	}
	tag, err := s.DB.Exec(ctx, `UPDATE api_keys SET revoked_at = now() WHERE id=$1 AND user_id=$2 AND revoked_at IS NULL`, id, p.UserID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errf(ErrNotFound, "API key not found")
	}
	return nil
}

// DropIn stores an upload through a Drop-In key: always a new file. The key
// needs nothing else, in particular no right to see what is there.
func (s *Service) DropIn(ctx context.Context, p *Principal, name string, r io.Reader) error {
	if p == nil || p.Via != "apikey" || len(p.KeySpaces) != 1 || p.KeyCaps != CapDropIn {
		return errf(ErrForbidden, "this needs a Drop-In key")
	}
	spaceID := p.KeySpaces[0]
	caps, err := s.spaceCaps(ctx, s.DB, p, spaceID)
	if err != nil {
		return err
	}
	if !caps.Has(CapDropIn) {
		return errf(ErrForbidden, "Drop-In is not available in this space")
	}
	name, err = NormalizeName(name)
	if err != nil {
		return err
	}
	var folder *FolderInfo
	if p.KeyFolder != nil {
		folder, err = s.folderInfo(ctx, s.DB, *p.KeyFolder)
	} else {
		var root string
		if err = s.DB.QueryRow(ctx, `SELECT id::text FROM entries WHERE space_id=$1 AND parent_id IS NULL AND kind='folder'`, spaceID).Scan(&root); err == nil {
			folder, err = s.folderInfo(ctx, s.DB, root)
		}
	}
	if err != nil {
		return errf(ErrNotFound, "the target folder no longer exists")
	}
	st, err := s.Blobs.Stage(r, s.Cfg.MaxUploadBytes)
	if errors.Is(err, blob.ErrTooLarge) {
		return errf(ErrTooLarge, "file exceeds the upload limit")
	}
	if err != nil {
		return err
	}
	_, err = s.createStaged(ctx, p, folder, name, st, "drop", true)
	return err
}

// APIKeyPrincipal resolves "tildoc_<id>_<secret>".
func (s *Service) APIKeyPrincipal(ctx context.Context, token string) (*Principal, error) {
	id, secret, ok := strings.Cut(strings.TrimPrefix(token, APIKeyPrefix), "_")
	if !ok || !looksLikeUUID(id) {
		return nil, errf(ErrUnauthorized, "invalid API key")
	}
	p := &Principal{Via: "apikey", KeyID: id}
	var hash []byte
	var perms []string
	err := s.DB.QueryRow(ctx, `SELECT k.secret_hash, u.id::text, u.username,
		ARRAY(SELECT permission FROM api_key_permissions WHERE key_id = k.id),
		ARRAY(SELECT space_id::text FROM api_key_spaces WHERE key_id = k.id), k.folder_id::text, k.all_spaces
		FROM api_keys k JOIN users u ON u.id = k.user_id
		WHERE k.id = $1 AND k.revoked_at IS NULL AND (k.expires_at IS NULL OR k.expires_at > now()) AND NOT u.disabled AND u.status = 'active'`, id).
		Scan(&hash, &p.UserID, &p.Username, &perms, &p.KeySpaces, &p.KeyFolder, &p.KeyAllSpaces)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errf(ErrUnauthorized, "invalid API key")
	}
	if err != nil {
		return nil, err
	}
	if subtle.ConstantTimeCompare(hash, tokenHash(secret)) != 1 {
		return nil, errf(ErrUnauthorized, "invalid API key")
	}
	p.KeyCaps = capsFromNames(perms)
	s.DB.Exec(ctx, `UPDATE api_keys SET last_used_at = now() WHERE id = $1 AND (last_used_at IS NULL OR last_used_at < now() - interval '1 minute')`, id)
	return p, nil
}
