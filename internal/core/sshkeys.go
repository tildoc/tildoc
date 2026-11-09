package core

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/ssh"

	"github.com/tildoc/tildoc/internal/db"
)

// SSHKey is a registered public key for SFTP.
type SSHKey struct {
	ID          string     `json:"id"`
	Fingerprint string     `json:"fingerprint"`
	Type        string     `json:"type"`
	Label       string     `json:"label"`
	CreatedAt   time.Time  `json:"created_at"`
	LastUsedAt  *time.Time `json:"last_used_at"`
}

// AddSSHKey registers an authorized_keys formatted public key.
func (s *Service) AddSSHKey(ctx context.Context, p *Principal, authorizedKey, label string) (*SSHKey, error) {
	if p == nil || p.Via == "accesskey" || p.Via == "ssh" {
		return nil, errf(ErrForbidden, "ssh keys can only be managed from a login session")
	}
	pub, comment, _, _, err := ssh.ParseAuthorizedKey([]byte(strings.TrimSpace(authorizedKey)))
	if err != nil {
		return nil, errf(ErrInvalid, "invalid public key: %v", err)
	}
	if label = strings.TrimSpace(label); label == "" {
		label = comment
	}
	k := &SSHKey{Fingerprint: ssh.FingerprintSHA256(pub), Type: pub.Type(), Label: label}
	err = s.DB.QueryRow(ctx, `INSERT INTO ssh_keys(user_id, fingerprint, public_key, label) VALUES($1,$2,$3,$4)
		RETURNING id::text, created_at`, p.UserID, k.Fingerprint, string(ssh.MarshalAuthorizedKey(pub)), label).
		Scan(&k.ID, &k.CreatedAt)
	if db.IsUniqueViolation(err) {
		return nil, errf(ErrConflict, "key already registered")
	}
	return k, err
}

// ListSSHKeys lists the caller's SSH keys.
func (s *Service) ListSSHKeys(ctx context.Context, p *Principal) ([]SSHKey, error) {
	rows, err := s.DB.Query(ctx, `SELECT id::text, fingerprint, public_key, label, created_at, last_used_at
		FROM ssh_keys WHERE user_id=$1 ORDER BY created_at`, p.UserID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SSHKey{}
	for rows.Next() {
		var k SSHKey
		var pubText string
		if err := rows.Scan(&k.ID, &k.Fingerprint, &pubText, &k.Label, &k.CreatedAt, &k.LastUsedAt); err != nil {
			return nil, err
		}
		k.Type, _, _ = strings.Cut(pubText, " ")
		out = append(out, k)
	}
	return out, rows.Err()
}

// DeleteSSHKey removes one of the caller's keys.
func (s *Service) DeleteSSHKey(ctx context.Context, p *Principal, id string) error {
	tag, err := s.DB.Exec(ctx, `DELETE FROM ssh_keys WHERE id=$1 AND user_id=$2`, id, p.UserID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errf(ErrNotFound, "ssh key not found")
	}
	return nil
}

// SSHKeyPrincipal authenticates an SSH public key for a username.
func (s *Service) SSHKeyPrincipal(ctx context.Context, username string, pub ssh.PublicKey) (*Principal, error) {
	fp := ssh.FingerprintSHA256(pub)
	p := &Principal{Via: "ssh"}
	var stored string
	err := s.DB.QueryRow(ctx, `SELECT u.id::text, u.username, k.public_key FROM ssh_keys k JOIN users u ON u.id=k.user_id
		WHERE k.fingerprint=$1 AND u.username=$2 AND NOT u.disabled AND u.status = 'active'`, fp, strings.ToLower(username)).
		Scan(&p.UserID, &p.Username, &stored)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errf(ErrUnauthorized, "unknown key")
	}
	if err != nil {
		return nil, err
	}
	sk, _, _, _, err := ssh.ParseAuthorizedKey([]byte(stored))
	if err != nil || string(sk.Marshal()) != string(pub.Marshal()) {
		return nil, errf(ErrUnauthorized, "unknown key")
	}
	s.DB.Exec(ctx, `UPDATE ssh_keys SET last_used_at=now() WHERE fingerprint=$1`, fp)
	return p, nil
}
