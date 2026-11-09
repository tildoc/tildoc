package core

import (
	"context"
	"errors"
	"strings"

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
	var hash string
	var disabled bool
	var inst []string
	err := a.s.DB.QueryRow(ctx, `SELECT u.id::text, i.id::text, i.password_hash, u.disabled, instance_permissions(u.id)
		FROM users u JOIN identities i ON i.user_id = u.id AND i.provider = $2 WHERE u.username = $1`,
		username, localProvider).Scan(&p.UserID, &p.IdentityID, &hash, &disabled, &inst)
	if errors.Is(err, pgx.ErrNoRows) {
		VerifyPassword(password, dummyHash)
		return nil, errf(ErrUnauthorized, "invalid credentials")
	}
	if err != nil {
		return nil, err
	}
	if !VerifyPassword(password, hash) || disabled {
		return nil, errf(ErrUnauthorized, "invalid credentials")
	}
	p.Inst = instFromNames(inst)
	a.s.DB.Exec(ctx, `UPDATE identities SET last_login_at = now() WHERE id = $1`, p.IdentityID)
	return p, nil
}

// Authenticate logs in with username and password.
func (s *Service) Authenticate(ctx context.Context, username, password string) (*Principal, error) {
	return localAuthenticator{s}.Authenticate(ctx, username, password)
}
