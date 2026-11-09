package core

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

// Editing text documents in the web UI. One editor (browser tab, client id)
// per document at a time: it holds a lease renewed by heartbeat, which
// expires EditLeaseTTL after the last one. Other channels (WebDAV, SFTP, S3)
// are not blocked; the last save wins and earlier versions stay in history.
const (
	EditLeaseTTL = 15 * time.Second
	MaxEditBytes = 5 << 20
)

// EditLease is who edits a document right now (expired leases are not shown).
type EditLease struct {
	User      string    `json:"user"`
	Name      string    `json:"name"`
	Mine      bool      `json:"mine"` // held by the caller (in any tab)
	ExpiresAt time.Time `json:"expires_at"`
}

// editable checks that p may edit the document as text.
func (s *Service) editable(ctx context.Context, p *Principal, docID string) (*Document, error) {
	if err := requireSession(p); err != nil {
		return nil, err
	}
	d, err := s.GetDocument(ctx, p, docID)
	if err != nil {
		return nil, err
	}
	if err := s.requireDocCap(ctx, s.DB, p, docID, d.SpaceID, CapWrite); err != nil {
		return nil, err
	}
	switch {
	case d.DeletedAt != nil:
		return nil, errf(ErrInvalid, "the document is in the trash")
	case !strings.HasPrefix(d.MIME, "text/") && d.Size != 0: // a new, empty file can have any ending
		return nil, errf(ErrInvalid, "only text files can be edited")
	case d.Size > MaxEditBytes:
		return nil, errf(ErrTooLarge, "text files larger than 5 MB cannot be edited")
	}
	return d, nil
}

// AcquireEditLease takes the lease for clientID. It succeeds when the
// document is free, the lease expired, it is already clientID's, or
// takeover is set and the caller holds it in another tab. Otherwise it
// returns the current lease with an ErrConflict error.
func (s *Service) AcquireEditLease(ctx context.Context, p *Principal, docID, clientID string, takeover bool) (*EditLease, error) {
	if !looksLikeUUID(clientID) {
		return nil, errf(ErrInvalid, "invalid client id")
	}
	if _, err := s.editable(ctx, p, docID); err != nil {
		return nil, err
	}
	var exp time.Time
	err := s.DB.QueryRow(ctx, `
		INSERT INTO edit_leases(document_id, user_id, client_id, expires_at)
		VALUES($1, $2, $3, now() + make_interval(secs => $5))
		ON CONFLICT (document_id) DO UPDATE
		SET user_id = EXCLUDED.user_id, client_id = EXCLUDED.client_id, acquired_at = now(), expires_at = EXCLUDED.expires_at
		WHERE edit_leases.expires_at < now() OR edit_leases.client_id = EXCLUDED.client_id
		   OR ($4 AND edit_leases.user_id = EXCLUDED.user_id)
		RETURNING expires_at`, docID, p.UserID, clientID, takeover, EditLeaseTTL.Seconds()).Scan(&exp)
	if errors.Is(err, pgx.ErrNoRows) {
		l, lerr := s.editLease(ctx, p, docID)
		if lerr != nil {
			return nil, lerr
		}
		if l == nil { // expired between the two statements: try once more
			return s.AcquireEditLease(ctx, p, docID, clientID, takeover)
		}
		if l.Mine {
			return l, errf(ErrConflict, "you are editing this document in another window")
		}
		return l, errf(ErrConflict, "%s is editing this document", l.Name)
	}
	if err != nil {
		return nil, err
	}
	return s.editLease(ctx, p, docID)
}

// RenewEditLease is the heartbeat of the editing tab. ErrConflict: the lease
// was taken over (by another tab after it had expired, or on purpose).
func (s *Service) RenewEditLease(ctx context.Context, p *Principal, docID, clientID string) error {
	if err := requireSession(p); err != nil {
		return err
	}
	tag, err := s.DB.Exec(ctx, `UPDATE edit_leases SET expires_at = now() + make_interval(secs => $4)
		WHERE document_id = $1 AND client_id::text = $2 AND user_id = $3`, docID, clientID, p.UserID, EditLeaseTTL.Seconds())
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errf(ErrConflict, "the document is no longer reserved for this window")
	}
	return nil
}

// ReleaseEditLease frees the document (editor closed).
func (s *Service) ReleaseEditLease(ctx context.Context, p *Principal, docID, clientID string) error {
	if err := requireSession(p); err != nil {
		return err
	}
	_, err := s.DB.Exec(ctx, `DELETE FROM edit_leases WHERE document_id = $1 AND client_id::text = $2 AND user_id = $3`,
		docID, clientID, p.UserID)
	return err
}

// SaveText stores text from the editor as a new version (source "editor").
// The caller's window must hold the lease; the text must be UTF-8 and at
// most MaxEditBytes.
func (s *Service) SaveText(ctx context.Context, p *Principal, docID, clientID string, r io.Reader) (*Document, error) {
	if _, err := s.editable(ctx, p, docID); err != nil {
		return nil, err
	}
	var held bool
	if err := s.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM edit_leases WHERE document_id = $1 AND client_id::text = $2 AND user_id = $3)`,
		docID, clientID, p.UserID).Scan(&held); err != nil {
		return nil, err
	}
	if !held {
		return nil, errf(ErrConflict, "the document is not reserved for this window; open it again to edit")
	}
	b, err := io.ReadAll(io.LimitReader(r, MaxEditBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > MaxEditBytes {
		return nil, errf(ErrTooLarge, "text larger than 5 MB")
	}
	if !utf8.Valid(b) {
		return nil, errf(ErrInvalid, "the text is not valid UTF-8")
	}
	d, err := s.AddDocumentVersion(ctx, p, docID, bytes.NewReader(b), "editor")
	if err != nil {
		return nil, err
	}
	s.RenewEditLease(ctx, p, docID, clientID) //nolint:errcheck // the next heartbeat renews it
	d.EditLease, err = s.editLease(ctx, p, docID)
	return d, err
}

// editLease returns the current, unexpired lease of a document (or nil).
func (s *Service) editLease(ctx context.Context, p *Principal, docID string) (*EditLease, error) {
	l := &EditLease{}
	err := s.DB.QueryRow(ctx, `SELECT u.username, coalesce(nullif(u.display_name, ''), u.username), coalesce(l.user_id = $2::uuid, false), l.expires_at
		FROM edit_leases l JOIN users u ON u.id = l.user_id
		WHERE l.document_id = $1 AND l.expires_at > now()`, docID, p.userRef()).Scan(&l.User, &l.Name, &l.Mine, &l.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return l, err
}

// PruneEditLeases deletes leases expired for a while (housekeeping).
func (s *Service) PruneEditLeases(ctx context.Context) error {
	_, err := s.DB.Exec(ctx, `DELETE FROM edit_leases WHERE expires_at < now() - interval '1 hour'`)
	return err
}
