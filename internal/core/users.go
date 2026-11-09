package core

import (
	"context"
	"crypto/subtle"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/tildoc/tildoc/internal/db"
)

// User is an account.
type User struct {
	ID          string    `json:"id"`
	Username    string    `json:"username"`
	DisplayName string    `json:"display_name"`
	IsAdmin     bool      `json:"is_admin"`
	Status      string    `json:"status"` // active, pending (waiting for approval)
	Disabled    bool      `json:"disabled"`
	CreatedAt   time.Time `json:"created_at"`
}

const userCols = `id::text, username, display_name, is_administrator(id), status = 'disabled', status, created_at`

func scanUser(row pgx.Row) (*User, error) {
	u := &User{}
	err := row.Scan(&u.ID, &u.Username, &u.DisplayName, &u.IsAdmin, &u.Disabled, &u.Status, &u.CreatedAt)
	if err == pgx.ErrNoRows {
		return nil, errf(ErrNotFound, "user not found")
	}
	return u, err
}

func validatePassword(pw string) error {
	if len(pw) < 8 || len(pw) > 1024 {
		return errf(ErrInvalid, "password must be 8-1024 characters")
	}
	return nil
}

// NeedsSetup reports whether no user exists yet.
func (s *Service) NeedsSetup(ctx context.Context) (bool, error) {
	var exists bool
	err := s.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users)`).Scan(&exists)
	return !exists, err
}

// SetupAdmin creates the first user as administrator. Fails once any user exists.
func (s *Service) SetupAdmin(ctx context.Context, username, password, displayName string) (*User, error) {
	var u *User
	err := s.tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `LOCK TABLE users IN EXCLUSIVE MODE`); err != nil {
			return err
		}
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users)`).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return errf(ErrForbidden, "setup already completed")
		}
		var err error
		u, err = s.createUserTx(ctx, tx, username, password, displayName, true)
		return err
	})
	return u, err
}

// CreateUser creates a user (admin only) including a personal space.
func (s *Service) CreateUser(ctx context.Context, p *Principal, username, password, displayName string, isAdmin bool) (*User, error) {
	if err := requireInst(p, InstUsers); err != nil {
		return nil, err
	}
	var u *User
	err := s.tx(ctx, func(tx pgx.Tx) error {
		var err error
		if u, err = s.createUserTx(ctx, tx, username, password, displayName, isAdmin); err != nil {
			return err
		}
		return s.audit(ctx, tx, p, "user.create", "user", u.ID, "", map[string]any{"username": u.Username, "administrator": isAdmin})
	})
	return u, err
}

func (s *Service) createUserTx(ctx context.Context, tx pgx.Tx, username, password, displayName string, isAdmin bool) (*User, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	if err := ValidateUsername(username); err != nil {
		return nil, err
	}
	if err := validatePassword(password); err != nil {
		return nil, err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return nil, err
	}
	if displayName == "" {
		displayName = username
	}
	u, err := scanUser(tx.QueryRow(ctx, `INSERT INTO users(username, display_name)
		VALUES($1,$2) RETURNING `+userCols, username, displayName))
	if db.IsUniqueViolation(err) {
		return nil, errf(ErrConflict, "username %q already taken", username)
	}
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO identities(user_id, provider, issuer, subject, password_hash) VALUES($1::uuid,$2,$2,$1::text,$3)`,
		u.ID, localProvider, hash); err != nil {
		return nil, err
	}
	role := "user"
	if isAdmin {
		role = "administrator"
	}
	if err := setInstanceRoleTx(ctx, tx, u.ID, role); err != nil {
		return nil, err
	}
	u.IsAdmin = isAdmin
	if _, err := s.createSpaceTx(ctx, tx, s.uniqueSlug(ctx, tx, username), displayName, u.ID, ""); err != nil {
		return nil, err
	}
	return u, nil
}

func setInstanceRoleTx(ctx context.Context, tx pgx.Tx, userID, slug string) error {
	_, err := tx.Exec(ctx, `INSERT INTO grants(user_id, role_id) SELECT $1, id FROM roles WHERE slug=$2
		ON CONFLICT (user_id) WHERE space_id IS NULL AND document_id IS NULL AND user_id IS NOT NULL
		DO UPDATE SET role_id = EXCLUDED.role_id`, userID, slug)
	return err
}

// lastAdminCheck refuses to take away the last active administrator.
func lastAdminCheck(ctx context.Context, tx pgx.Tx, userID string) error {
	var others bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id <> $1 AND status = 'active' AND is_administrator(id))`, userID).Scan(&others); err != nil {
		return err
	}
	if !others {
		return errf(ErrInvalid, "at least one active administrator must remain")
	}
	return nil
}

// GetUser returns a user by id.
func (s *Service) GetUser(ctx context.Context, id string) (*User, error) {
	return scanUser(s.DB.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE id=$1`, id))
}

// UserByName returns a user by username.
func (s *Service) UserByName(ctx context.Context, username string) (*User, error) {
	return scanUser(s.DB.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE username=$1`,
		strings.ToLower(strings.TrimSpace(username))))
}

// ListUsers lists all users. Admins see all fields; regular users get a
// directory (needed to add team members).
func (s *Service) ListUsers(ctx context.Context, p *Principal) ([]User, error) {
	if p == nil {
		return nil, ErrUnauthorized
	}
	// waiting people are only for those who decide about them
	rows, err := s.DB.Query(ctx, `SELECT `+userCols+` FROM users WHERE status = 'active' OR $1 ORDER BY username`, p.Can(InstUsers))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		if !p.Can(InstUsers) {
			u.Disabled = false
		}
		out = append(out, *u)
	}
	return out, rows.Err()
}

// UserUpdate holds optional changes.
type UserUpdate struct {
	DisplayName *string `json:"display_name"`
	IsAdmin     *bool   `json:"is_admin"`
	Disabled    *bool   `json:"disabled"`
	Password    *string `json:"password"`
}

// UpdateUser changes a user. Admins may change everything; users only their
// display name.
func (s *Service) UpdateUser(ctx context.Context, p *Principal, id string, upd UserUpdate) (*User, error) {
	self := p != nil && p.UserID == id
	if p == nil || (!p.Can(InstUsers) && !self) {
		return nil, errf(ErrForbidden, "not allowed")
	}
	if !p.Can(InstUsers) && (upd.IsAdmin != nil || upd.Disabled != nil || upd.Password != nil) {
		return nil, errf(ErrForbidden, "admin only")
	}
	if self && ((upd.IsAdmin != nil && !*upd.IsAdmin) || (upd.Disabled != nil && *upd.Disabled)) {
		return nil, errf(ErrInvalid, "cannot demote or disable yourself")
	}
	var u *User
	err := s.tx(ctx, func(tx pgx.Tx) error {
		if upd.DisplayName != nil {
			if _, err := tx.Exec(ctx, `UPDATE users SET display_name=$2 WHERE id=$1`, id, strings.TrimSpace(*upd.DisplayName)); err != nil {
				return err
			}
		}
		if upd.IsAdmin != nil {
			role := "user"
			if *upd.IsAdmin {
				role = "administrator"
			} else if err := lastAdminCheck(ctx, tx, id); err != nil {
				return err
			}
			if err := setInstanceRoleTx(ctx, tx, id, role); err != nil {
				return err
			}
		}
		if upd.Disabled != nil {
			if _, err := tx.Exec(ctx, `UPDATE users SET status = CASE WHEN $2 THEN 'disabled' ELSE 'active' END WHERE id=$1 AND status <> 'pending'`, id, *upd.Disabled); err != nil {
				return err
			}
			if *upd.Disabled {
				if err := lastAdminCheck(ctx, tx, id); err != nil {
					var admin bool
					if qerr := tx.QueryRow(ctx, `SELECT is_administrator($1)`, id).Scan(&admin); qerr != nil || admin {
						return err
					}
				}
				if _, err := tx.Exec(ctx, `DELETE FROM sessions WHERE user_id=$1`, id); err != nil {
					return err
				}
			}
		}
		if upd.Password != nil {
			if err := s.setPasswordTx(ctx, tx, id, *upd.Password); err != nil {
				return err
			}
		}
		var err error
		if u, err = scanUser(tx.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE id=$1`, id)); err != nil {
			return err
		}
		if upd.IsAdmin == nil && upd.Disabled == nil && upd.Password == nil {
			return nil // only a display name
		}
		return s.audit(ctx, tx, p, "user.update", "user", id, "", map[string]any{"administrator": upd.IsAdmin, "disabled": upd.Disabled, "password_changed": upd.Password != nil})
	})
	return u, err
}

// ChangePassword lets a user change their own password.
func (s *Service) ChangePassword(ctx context.Context, p *Principal, oldPW, newPW string) error {
	var hash string
	err := s.DB.QueryRow(ctx, `SELECT password_hash FROM identities WHERE user_id=$1 AND provider=$2`, p.UserID, localProvider).Scan(&hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return errf(ErrInvalid, "this account has no password")
	}
	if err != nil {
		return err
	}
	if !VerifyPassword(oldPW, hash) {
		return errf(ErrForbidden, "current password is wrong")
	}
	return s.tx(ctx, func(tx pgx.Tx) error { return s.setPasswordTx(ctx, tx, p.UserID, newPW) })
}

func (s *Service) setPasswordTx(ctx context.Context, tx pgx.Tx, id, pw string) error {
	if err := validatePassword(pw); err != nil {
		return err
	}
	hash, err := HashPassword(pw)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO identities(user_id, provider, issuer, subject, password_hash) VALUES($1::uuid,$3,$3,$1::text,$2)
		ON CONFLICT (issuer, subject) DO UPDATE SET password_hash = EXCLUDED.password_hash`, id, hash, localProvider); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `DELETE FROM sessions WHERE user_id=$1`, id)
	return err
}

// DeleteUser removes a user. Their empty spaces go too; spaces that hold
// documents must be transferred or deleted first.
func (s *Service) DeleteUser(ctx context.Context, p *Principal, id string) error {
	if err := requireInst(p, InstUsers); err != nil {
		return err
	}
	if p.UserID == id {
		return errf(ErrInvalid, "cannot delete yourself")
	}
	return s.tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM spaces s WHERE s.owner_user_id=$1 AND NOT EXISTS (SELECT 1 FROM documents d WHERE d.space_id=s.id)`, id); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `DELETE FROM users WHERE id=$1`, id)
		if db.IsForeignKeyViolation(err) {
			return errf(ErrConflict, "this user still owns spaces with documents: transfer or delete them first")
		}
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return errf(ErrNotFound, "user not found")
		}
		return s.audit(ctx, tx, p, "user.delete", "user", id, "", nil)
	})
}

// ---------------------------------------------------------------- sessions

// CreateSession creates a login session and returns its token.
func (s *Service) CreateSession(ctx context.Context, p *Principal) (string, time.Time, error) {
	tok := randomToken(32)
	exp := s.now().Add(s.Cfg.SessionTTL)
	_, err := s.DB.Exec(ctx, `INSERT INTO sessions(token_hash, user_id, identity_id, expires_at) VALUES($1,$2,nullif($3,'')::uuid,$4)`,
		tokenHash(tok), p.UserID, p.IdentityID, exp)
	return tok, exp, err
}

// SessionPrincipal resolves a session token.
func (s *Service) SessionPrincipal(ctx context.Context, token string) (*Principal, error) {
	if token == "" {
		return nil, ErrUnauthorized
	}
	p := &Principal{Via: "session", SessionHash: tokenHash(token)}
	var lastSeen time.Time
	var inst []string
	err := s.DB.QueryRow(ctx, `
		SELECT u.id::text, u.username, instance_permissions(u.id), s.last_seen_at
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token_hash=$1 AND s.expires_at > now() AND u.status = 'active'`, tokenHash(token)).
		Scan(&p.UserID, &p.Username, &inst, &lastSeen)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errf(ErrUnauthorized, "invalid session")
	}
	if err != nil {
		return nil, err
	}
	p.Inst = instFromNames(inst)
	if s.now().Sub(lastSeen) > 5*time.Minute {
		s.DB.Exec(ctx, `UPDATE sessions SET last_seen_at=now() WHERE token_hash=$1`, tokenHash(token)) //nolint:errcheck // only a usage hint
	}
	return p, nil
}

// DeleteSession logs out.
func (s *Service) DeleteSession(ctx context.Context, token string) error {
	_, err := s.DB.Exec(ctx, `DELETE FROM sessions WHERE token_hash=$1`, tokenHash(token))
	return err
}

// ---------------------------------------------------------------- access keys

// AccessKey describes an access key (secret omitted).
type AccessKey struct {
	ID         string     `json:"id"`
	Label      string     `json:"label"`
	SpaceID    *string    `json:"space_id"`
	ReadOnly   bool       `json:"read_only"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
}

// CreateAccessKey creates a key for the caller; the secret is returned once.
func (s *Service) CreateAccessKey(ctx context.Context, p *Principal, label, spaceID string, readOnly bool) (*AccessKey, string, error) {
	if err := requireSession(p); err != nil {
		return nil, "", err
	}
	var space *string
	if spaceID != "" {
		if err := s.requirePerm(ctx, s.DB, p, spaceID, PermRead); err != nil {
			return nil, "", err
		}
		space = &spaceID
	}
	id, secret := newAccessKeyID(), newSecret()
	enc, err := s.sealSecret(secret, id)
	if err != nil {
		return nil, "", err
	}
	k := &AccessKey{ID: id, Label: strings.TrimSpace(label), SpaceID: space, ReadOnly: readOnly}
	err = s.tx(ctx, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `INSERT INTO access_keys(id, user_id, secret_enc, label, space_id, read_only)
			VALUES($1,$2,$3,$4,$5,$6) RETURNING created_at`, id, p.UserID, enc, k.Label, space, readOnly).Scan(&k.CreatedAt); err != nil {
			return err
		}
		return s.audit(ctx, tx, p, "access_key.create", "access_key", id, spaceID, map[string]any{"label": k.Label, "read_only": readOnly})
	})
	return k, secret, err
}

// ListAccessKeys lists the caller's active keys.
func (s *Service) ListAccessKeys(ctx context.Context, p *Principal) ([]AccessKey, error) {
	rows, err := s.DB.Query(ctx, `SELECT id, label, space_id::text, read_only, created_at, last_used_at
		FROM access_keys WHERE user_id=$1 AND revoked_at IS NULL ORDER BY created_at`, p.UserID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AccessKey{}
	for rows.Next() {
		var k AccessKey
		if err := rows.Scan(&k.ID, &k.Label, &k.SpaceID, &k.ReadOnly, &k.CreatedAt, &k.LastUsedAt); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// RevokeAccessKey revokes one of the caller's keys.
func (s *Service) RevokeAccessKey(ctx context.Context, p *Principal, id string) error {
	return s.tx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE access_keys SET revoked_at=now() WHERE id=$1 AND user_id=$2 AND revoked_at IS NULL`, id, p.UserID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return errf(ErrNotFound, "access key not found")
		}
		return s.audit(ctx, tx, p, "access_key.revoke", "access_key", id, "", nil)
	})
}

// AccessKeySecret resolves a key id to its secret and principal (for SigV4).
func (s *Service) AccessKeySecret(ctx context.Context, keyID string) (string, *Principal, error) {
	var enc []byte
	var space *string
	p := &Principal{Via: "accesskey", KeyID: keyID}
	err := s.DB.QueryRow(ctx, `
		SELECT k.secret_enc, k.space_id::text, k.read_only, u.id::text, u.username
		FROM access_keys k JOIN users u ON u.id = k.user_id
		WHERE k.id=$1 AND k.revoked_at IS NULL AND u.status = 'active'`, keyID).
		Scan(&enc, &space, &p.ReadOnly, &p.UserID, &p.Username)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil, errf(ErrUnauthorized, "unknown access key")
	}
	if err != nil {
		return "", nil, err
	}
	if space != nil {
		p.ScopeSpaceID = *space
	}
	secret, err := s.openSecret(enc, keyID)
	if err != nil {
		return "", nil, err
	}
	return secret, p, nil // access keys never carry instance permissions
}

// TouchAccessKey records key usage (best effort, rate limited by caller).
func (s *Service) TouchAccessKey(ctx context.Context, keyID string) {
	s.DB.Exec(ctx, `UPDATE access_keys SET last_used_at=now() WHERE id=$1 AND (last_used_at IS NULL OR last_used_at < now() - interval '1 minute')`, keyID) //nolint:errcheck // only a usage hint
}

// AuthenticateAppPassword authenticates WebDAV/SFTP password logins. The
// login may be the username or the access key id; the password must be an
// access key secret.
func (s *Service) AuthenticateAppPassword(ctx context.Context, login, secret string) (*Principal, error) {
	login = strings.TrimSpace(login)
	var ids []string
	if strings.HasPrefix(login, "DK") && len(login) == 20 {
		ids = []string{login}
	} else {
		rows, err := s.DB.Query(ctx, `SELECT k.id FROM access_keys k JOIN users u ON u.id=k.user_id
			WHERE u.username=$1 AND k.revoked_at IS NULL`, strings.ToLower(login))
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			ids = append(ids, id)
		}
		rows.Close()
	}
	var found *Principal
	for _, id := range ids {
		sec, p, err := s.AccessKeySecret(ctx, id)
		if err != nil {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(sec), []byte(secret)) == 1 && found == nil {
			found = p
		}
	}
	if found == nil {
		return nil, errf(ErrUnauthorized, "invalid credentials")
	}
	s.TouchAccessKey(ctx, found.KeyID)
	return found, nil
}
