package core

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// localProvider is the built-in username + password login.
const localProvider = "local"

// Authenticator is a way to log in. OIDC and other providers implement this
// next to the local one; accounts are matched by (provider, subject) in the
// identities table, never by e-mail address.
type Authenticator interface {
	Provider() string
	Authenticate(ctx context.Context, login, secret string) (*Principal, error)
}

type localAuthenticator struct{ s *Service }

func (a localAuthenticator) Provider() string { return localProvider }

// Authenticate verifies username + password.
func (a localAuthenticator) Authenticate(ctx context.Context, username, password string) (*Principal, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	p := &Principal{Username: username, Via: "password"}
	var hash, status string
	var inst []string
	err := a.s.DB.QueryRow(ctx, `SELECT u.id::text, i.id::text, i.password_hash, u.status, instance_permissions(u.id)
		FROM users u JOIN identities i ON i.user_id = u.id AND i.provider = $2 WHERE u.username = $1`,
		username, localProvider).Scan(&p.UserID, &p.IdentityID, &hash, &status, &inst)
	if errors.Is(err, pgx.ErrNoRows) {
		VerifyPassword(password, dummyHash)
		return nil, errf(ErrUnauthorized, "invalid credentials")
	}
	if err != nil {
		return nil, err
	}
	if !VerifyPassword(password, hash) || status != "active" {
		return nil, errf(ErrUnauthorized, "invalid credentials")
	}
	p.Inst = instFromNames(inst)
	a.s.DB.Exec(ctx, `UPDATE identities SET last_login_at = now() WHERE id = $1`, p.IdentityID) //nolint:errcheck // only a usage hint
	return p, nil
}

// Authenticate logs in with username and password.
func (s *Service) Authenticate(ctx context.Context, username, password string) (*Principal, error) {
	return localAuthenticator{s}.Authenticate(ctx, username, password)
}

// ExternalLogin is the result of a sign-in with an external provider.
type ExternalLogin struct {
	Principal *Principal // nil while the account waits for approval
	Pending   bool
}

// LoginExternal finds the account for (provider, subject). A first login
// creates an account that waits for an administrator's approval: it has no
// permissions and no personal space until then.
func (s *Service) LoginExternal(ctx context.Context, provider, issuer, subject, email, name string) (*ExternalLogin, error) {
	if subject == "" || issuer == "" || provider == localProvider {
		return nil, errf(ErrUnauthorized, "invalid login")
	}
	if need, err := s.NeedsSetup(ctx); err != nil {
		return nil, err
	} else if need {
		return nil, errf(ErrForbidden, "this server is not set up yet")
	}
	res := &ExternalLogin{}
	err := s.tx(ctx, func(tx pgx.Tx) error {
		*res = ExternalLogin{}
		p := &Principal{Via: "password"}
		var status string
		var inst []string
		err := tx.QueryRow(ctx, `SELECT u.id::text, u.username, i.id::text, u.status, instance_permissions(u.id)
			FROM identities i JOIN users u ON u.id = i.user_id WHERE i.issuer = $1 AND i.subject = $2`, issuer, subject).
			Scan(&p.UserID, &p.Username, &p.IdentityID, &status, &inst)
		if errors.Is(err, pgx.ErrNoRows) {
			return s.createPendingTx(ctx, tx, provider, issuer, subject, email, name, res)
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE identities SET last_login_at = now(), email = $2 WHERE id = $1`, p.IdentityID, email); err != nil {
			return err
		}
		switch status {
		case "disabled":
			return errf(ErrUnauthorized, "this account is disabled")
		case "pending":
			res.Pending = true
			return nil
		}
		p.Inst = instFromNames(inst)
		res.Principal = p
		return nil
	})
	return res, err
}

func (s *Service) createPendingTx(ctx context.Context, tx pgx.Tx, provider, issuer, subject, email, name string, res *ExternalLogin) error {
	base, _, _ := strings.Cut(strings.ToLower(email), "@")
	base = strings.Trim(usernameChars.ReplaceAllString(base, "-"), "-._")
	if len(base) < 2 {
		base = "user-" + base
	}
	if len(base) > 28 {
		base = base[:28]
	}
	username := base
	for i := 2; ; i++ {
		var taken bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE username = $1)`, username).Scan(&taken); err != nil {
			return err
		}
		if !taken {
			break
		}
		username = fmt.Sprintf("%s-%d", base, i)
	}
	if name == "" {
		name = username
	}
	var id string
	if err := tx.QueryRow(ctx, `INSERT INTO users(username, display_name, status) VALUES($1,$2,'pending') RETURNING id::text`, username, name).Scan(&id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO identities(user_id, provider, issuer, subject, email, last_login_at) VALUES($1,$2,$3,$4,$5,now())`, id, provider, issuer, subject, email); err != nil {
		return err
	}
	res.Pending = true
	return nil
}

var usernameChars = regexp.MustCompile(`[^a-z0-9._-]+`)

// ApproveUser lets a waiting account in: it gets the default role and a
// personal space. Declining is DeleteUser.
func (s *Service) ApproveUser(ctx context.Context, p *Principal, id string) (*User, error) {
	if err := requireInst(p, InstUsers); err != nil {
		return nil, err
	}
	if !looksLikeUUID(id) {
		return nil, errf(ErrNotFound, "user not found")
	}
	var u *User
	err := s.tx(ctx, func(tx pgx.Tx) error {
		var err error
		u, err = scanUser(tx.QueryRow(ctx, `UPDATE users SET status = 'active' WHERE id = $1 AND status = 'pending' RETURNING `+userCols, id))
		if err != nil {
			return err
		}
		if err := setInstanceRoleTx(ctx, tx, u.ID, "user"); err != nil {
			return err
		}
		if _, err = s.createSpaceTx(ctx, tx, s.uniqueSlug(ctx, tx, u.Username), u.DisplayName, u.ID, ""); err != nil {
			return err
		}
		return s.audit(ctx, tx, p, "user.approve", "user", u.ID, "", map[string]any{"username": u.Username})
	})
	return u, err
}

// OAuthLogin is a sign-in under way, kept between redirect and callback.
type OAuthLogin struct {
	Nonce, Verifier string
}

// StartOAuthLogin stores a sign-in under way; state is the browser's key to it.
func (s *Service) StartOAuthLogin(ctx context.Context, provider, state, nonce, verifier string) error {
	_, err := s.DB.Exec(ctx, `INSERT INTO oauth_logins(state_hash, provider, nonce, verifier, expires_at) VALUES($1,$2,$3,$4,$5)`,
		tokenHash(state), provider, nonce, verifier, s.now().Add(10*time.Minute))
	return err
}

// FinishOAuthLogin takes a sign-in under way; it can be used once.
func (s *Service) FinishOAuthLogin(ctx context.Context, provider, state string) (*OAuthLogin, error) {
	l := &OAuthLogin{}
	err := s.DB.QueryRow(ctx, `DELETE FROM oauth_logins WHERE state_hash = $1 AND provider = $2 AND expires_at > now() RETURNING nonce, verifier`,
		tokenHash(state), provider).Scan(&l.Nonce, &l.Verifier)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errf(ErrUnauthorized, "this sign-in has expired, try again")
	}
	return l, err
}

// PruneOAuthLogins deletes abandoned sign-ins (housekeeping).
func (s *Service) PruneOAuthLogins(ctx context.Context) error {
	_, err := s.DB.Exec(ctx, `DELETE FROM oauth_logins WHERE expires_at < now()`)
	return err
}
