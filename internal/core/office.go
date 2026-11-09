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

// Office documents are edited in Collabora Online over WOPI. The co-editors
// of a document share one session, identified by Collabora's lock. Saves
// during the session (autosave, Ctrl+S) replace the working copy; the end of
// the session (exit save, UNLOCK or lock expiry) turns it into one version.
// Other channels are not blocked: the last save wins (like the text editor).
const (
	OfficeLockTTL  = 30 * time.Minute
	OfficeTokenTTL = 10 * time.Hour
)

// OfficeSession tells that a document is open in Collabora.
type OfficeSession struct {
	StartedAt time.Time  `json:"started_at"`
	SavedAt   *time.Time `json:"saved_at"`
}

// OfficeLockConflict is returned when another lock holds the document.
// Current is empty when the document is not locked at all.
type OfficeLockConflict struct{ Current string }

func (e *OfficeLockConflict) Error() string { return "the document is locked differently" }
func (e *OfficeLockConflict) Unwrap() error { return ErrConflict }

// OfficeFile is what Collabora needs to know about a file and its user.
type OfficeFile struct {
	Name     string
	OwnerID  string
	Size     int64
	Version  string // changes with the content
	UserID   string
	UserName string
	CanWrite bool
	Modified time.Time
}

// OfficeMIME tells whether a type may open in Collabora: everything but the
// types the viewer and the text editor handle themselves.
func OfficeMIME(mime string) bool {
	return !strings.HasPrefix(mime, "text/") && !strings.HasPrefix(mime, "application/pdf") &&
		!strings.Contains(mime, "json") && !strings.HasPrefix(mime, "image/")
}

// officeDocument loads a live document p may read and Collabora may open.
func (s *Service) officeDocument(ctx context.Context, p *Principal, docID string) (*Document, error) {
	d, err := s.GetDocument(ctx, p, docID)
	if err != nil {
		return nil, err
	}
	if d.DeletedAt != nil {
		return nil, ErrNotFound
	}
	if !OfficeMIME(d.MIME) {
		return nil, errf(ErrInvalid, "this file type does not open in Office")
	}
	return d, nil
}

// CreateOfficeToken issues the token Collabora uses on behalf of the caller.
// It ends with the login session; rights are checked again on every use.
func (s *Service) CreateOfficeToken(ctx context.Context, p *Principal, docID string) (token string, expires time.Time, d *Document, canWrite bool, err error) {
	if err = requireSession(p); err != nil {
		return
	}
	if p.Via != "session" {
		err = errf(ErrForbidden, "needs a login session")
		return
	}
	if d, err = s.officeDocument(ctx, p, docID); err != nil {
		return
	}
	canWrite = s.requirePerm(ctx, s.DB, p, d.SpaceID, PermWrite) == nil
	token = randomToken(32)
	expires = s.now().Add(OfficeTokenTTL)
	_, err = s.DB.Exec(ctx, `INSERT INTO wopi_tokens(token_hash, document_id, session_hash, expires_at) VALUES($1,$2,$3,$4)`,
		tokenHash(token), docID, p.SessionHash, expires)
	return
}

// OfficePrincipal resolves a token for one document.
func (s *Service) OfficePrincipal(ctx context.Context, token, docID string) (*Principal, error) {
	if token == "" || !looksLikeUUID(docID) {
		return nil, ErrUnauthorized
	}
	p := &Principal{Via: "wopi"}
	err := s.DB.QueryRow(ctx, `SELECT u.id::text, u.username, u.is_admin
		FROM wopi_tokens t JOIN sessions se ON se.token_hash = t.session_hash JOIN users u ON u.id = se.user_id
		WHERE t.token_hash = $1 AND t.document_id = $2 AND t.expires_at > now() AND se.expires_at > now() AND NOT u.disabled`,
		tokenHash(token), docID).Scan(&p.UserID, &p.Username, &p.IsAdmin)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errf(ErrUnauthorized, "invalid token")
	}
	if err != nil {
		return nil, err
	}
	return p, nil
}

// officeSession returns the running session of a document (or nil).
func (s *Service) officeSession(ctx context.Context, docID string) (*OfficeSession, error) {
	o := &OfficeSession{}
	err := s.DB.QueryRow(ctx, `SELECT started_at, saved_at FROM office_sessions WHERE document_id = $1 AND expires_at > now()`, docID).
		Scan(&o.StartedAt, &o.SavedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return o, err
}

// OfficeFileInfo describes the file as the session sees it.
func (s *Service) OfficeFileInfo(ctx context.Context, p *Principal, docID string) (*OfficeFile, error) {
	d, err := s.officeDocument(ctx, p, docID)
	if err != nil {
		return nil, err
	}
	f := &OfficeFile{Name: d.Name, OwnerID: d.SpaceID, Size: d.Size, Version: d.SHA256.Hex(), UserID: p.UserID, Modified: d.UpdatedAt,
		CanWrite: s.requirePerm(ctx, s.DB, p, d.SpaceID, PermWrite) == nil}
	if err := s.DB.QueryRow(ctx, `SELECT coalesce(nullif(display_name, ''), username) FROM users WHERE id = $1`, p.UserID).Scan(&f.UserName); err != nil {
		return nil, err
	}
	var sha []byte
	var saved *time.Time
	var size *int64
	err = s.DB.QueryRow(ctx, `SELECT o.working_sha256, o.saved_at, b.size FROM office_sessions o LEFT JOIN blobs b ON b.sha256 = o.working_sha256
		WHERE o.document_id = $1`, docID).Scan(&sha, &saved, &size)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if sha != nil {
		var h blob.SHA256
		copy(h[:], sha)
		f.Version, f.Size, f.Modified = h.Hex(), *size, *saved
	}
	return f, nil
}

// OpenOfficeFile returns the working copy during a session, so that joining
// editors get the state of the session, else the current version.
func (s *Service) OpenOfficeFile(ctx context.Context, p *Principal, docID string) (blob.File, error) {
	d, err := s.officeDocument(ctx, p, docID)
	if err != nil {
		return nil, err
	}
	sha := d.SHA256
	var work []byte
	err = s.DB.QueryRow(ctx, `SELECT working_sha256 FROM office_sessions WHERE document_id = $1`, docID).Scan(&work)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if work != nil {
		copy(sha[:], work)
	}
	return s.Blobs.Open(sha)
}

type officeSessionRow struct {
	exists, expired bool
	lock            string
}

// lockOfficeTx locks the document row (serializes all session changes of a
// document) and reads its session.
func lockOfficeTx(ctx context.Context, tx pgx.Tx, docID string) (officeSessionRow, error) {
	var r officeSessionRow
	var one int
	if err := tx.QueryRow(ctx, `SELECT 1 FROM documents WHERE id = $1 FOR UPDATE`, docID).Scan(&one); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return r, ErrNotFound
		}
		return r, err
	}
	err := tx.QueryRow(ctx, `SELECT lock_id, expires_at <= now() FROM office_sessions WHERE document_id = $1`, docID).Scan(&r.lock, &r.expired)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, nil
	}
	r.exists = err == nil
	return r, err
}

func startOfficeTx(ctx context.Context, tx pgx.Tx, docID, lock string) error {
	_, err := tx.Exec(ctx, `INSERT INTO office_sessions(document_id, lock_id, expires_at) VALUES($1, $2, now() + make_interval(secs => $3))`,
		docID, lock, OfficeLockTTL.Seconds())
	return err
}

// finishOfficeTx ends a session: a working copy that differs from the
// current version becomes a new version, by the user of the last save.
func finishOfficeTx(ctx context.Context, tx pgx.Tx, docID string) error {
	var work []byte
	var by *string
	err := tx.QueryRow(ctx, `DELETE FROM office_sessions WHERE document_id = $1 RETURNING working_sha256, saved_by::text`, docID).Scan(&work, &by)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && work == nil) {
		return nil
	}
	if err != nil {
		return err
	}
	var cur []byte
	var ver int
	if err := tx.QueryRow(ctx, `SELECT v.sha256, d.current_version FROM documents d
		JOIN document_versions v ON v.document_id = d.id AND v.version = d.current_version WHERE d.id = $1`, docID).Scan(&cur, &ver); err != nil {
		return err
	}
	if string(cur) == string(work) {
		return nil
	}
	if _, err := tx.Exec(ctx, `INSERT INTO document_versions(document_id, version, sha256, created_by, source) VALUES($1,$2,$3,$4,'office')`,
		docID, ver+1, work, by); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE documents SET current_version = $2, updated_at = now() WHERE id = $1`, docID, ver+1); err != nil {
		return err
	}
	var sha blob.SHA256
	copy(sha[:], work)
	return ensureProcessingTx(ctx, tx, sha) // working copies are not processed before
}

func validOfficeLock(lock string) error {
	if lock == "" || len(lock) > 1024 {
		return errf(ErrInvalid, "invalid lock")
	}
	return nil
}

// LockOffice takes or re-takes the lock (LOCK). With oldLock the lock is
// replaced (unlock and relock). An expired session of someone else ends here.
func (s *Service) LockOffice(ctx context.Context, p *Principal, docID, lock, oldLock string) error {
	if err := validOfficeLock(lock); err != nil {
		return err
	}
	d, err := s.officeDocument(ctx, p, docID)
	if err != nil {
		return err
	}
	if err := s.requirePerm(ctx, s.DB, p, d.SpaceID, PermWrite); err != nil {
		return err
	}
	return s.tx(ctx, func(tx pgx.Tx) error {
		r, err := lockOfficeTx(ctx, tx, docID)
		if err != nil {
			return err
		}
		switch {
		case oldLock != "" && (!r.exists || r.lock != oldLock):
			return &OfficeLockConflict{Current: r.lock}
		case r.exists && (r.lock == lock || r.lock == oldLock):
			return refreshOfficeTx(ctx, tx, docID, lock)
		case r.exists && !r.expired:
			return &OfficeLockConflict{Current: r.lock}
		case r.exists:
			if err := finishOfficeTx(ctx, tx, docID); err != nil {
				return err
			}
		}
		return startOfficeTx(ctx, tx, docID, lock)
	})
}

func refreshOfficeTx(ctx context.Context, tx pgx.Tx, docID, lock string) error {
	_, err := tx.Exec(ctx, `UPDATE office_sessions SET lock_id = $2, expires_at = now() + make_interval(secs => $3) WHERE document_id = $1`,
		docID, lock, OfficeLockTTL.Seconds())
	return err
}

// RefreshOfficeLock extends the lock (REFRESH_LOCK).
func (s *Service) RefreshOfficeLock(ctx context.Context, p *Principal, docID, lock string) error {
	if _, err := s.officeDocument(ctx, p, docID); err != nil {
		return err
	}
	return s.tx(ctx, func(tx pgx.Tx) error {
		r, err := lockOfficeTx(ctx, tx, docID)
		if err != nil {
			return err
		}
		if !r.exists || r.lock != lock {
			return &OfficeLockConflict{Current: r.lock}
		}
		return refreshOfficeTx(ctx, tx, docID, lock)
	})
}

// UnlockOffice ends the session (UNLOCK). Collabora writes nothing on exit
// when the last autosave already holds everything, so this also promotes the
// working copy. Without a session it does nothing: an exit save came first.
func (s *Service) UnlockOffice(ctx context.Context, p *Principal, docID, lock string) error {
	if _, err := s.officeDocument(ctx, p, docID); err != nil {
		return err
	}
	return s.tx(ctx, func(tx pgx.Tx) error {
		r, err := lockOfficeTx(ctx, tx, docID)
		if err != nil || !r.exists {
			return err
		}
		if r.lock != lock {
			return &OfficeLockConflict{Current: r.lock}
		}
		return finishOfficeTx(ctx, tx, docID)
	})
}

// OfficeLockOf returns the current lock ("" = none) (GET_LOCK).
func (s *Service) OfficeLockOf(ctx context.Context, p *Principal, docID string) (string, error) {
	if _, err := s.officeDocument(ctx, p, docID); err != nil {
		return "", err
	}
	var lock string
	err := s.DB.QueryRow(ctx, `SELECT lock_id FROM office_sessions WHERE document_id = $1 AND expires_at > now()`, docID).Scan(&lock)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return lock, err
}

// SaveOffice stores a save of the session as its working copy. exit marks
// the save that ends the session. A save with a lock but no session comes
// from a session whose lock expired meanwhile: it starts a new one instead
// of losing the text.
func (s *Service) SaveOffice(ctx context.Context, p *Principal, docID, lock string, body io.Reader, exit bool) (time.Time, error) {
	var saved time.Time
	d, err := s.officeDocument(ctx, p, docID)
	if err != nil {
		return saved, err
	}
	if err := s.requirePerm(ctx, s.DB, p, d.SpaceID, PermWrite); err != nil {
		return saved, err
	}
	st, err := s.Blobs.Stage(body, s.Cfg.MaxUploadBytes)
	if errors.Is(err, blob.ErrTooLarge) {
		return saved, errf(ErrTooLarge, "file exceeds the upload limit")
	}
	if err != nil {
		return saved, err
	}
	c := s.claim(st)
	defer c.discardUnused()
	if st.Size == 0 {
		return saved, errf(ErrInvalid, "empty file")
	}
	var old []byte
	err = s.tx(ctx, func(tx pgx.Tx) error {
		old = nil
		if err := c.commit(ctx, tx); err != nil {
			return err
		}
		// a row without processing: only a version gets processed
		if _, err := tx.Exec(ctx, `INSERT INTO blobs(sha256, md5, size, mime) VALUES($1,$2,$3,$4) ON CONFLICT (sha256) DO NOTHING`,
			st.SHA256[:], st.MD5[:], st.Size, DetectMIME(st.Head)); err != nil {
			return err
		}
		r, err := lockOfficeTx(ctx, tx, docID)
		if err != nil {
			return err
		}
		switch {
		case lock == "" || (r.exists && r.lock != lock):
			return &OfficeLockConflict{Current: r.lock}
		case !r.exists:
			if err := validOfficeLock(lock); err != nil {
				return err
			}
			if err := startOfficeTx(ctx, tx, docID, lock); err != nil {
				return err
			}
		}
		if err := tx.QueryRow(ctx, `SELECT working_sha256 FROM office_sessions WHERE document_id = $1`, docID).Scan(&old); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `UPDATE office_sessions SET working_sha256 = $2, saved_by = $3, saved_at = now(),
			expires_at = now() + make_interval(secs => $4) WHERE document_id = $1 RETURNING saved_at`,
			docID, st.SHA256[:], p.userRef(), OfficeLockTTL.Seconds()).Scan(&saved); err != nil {
			return err
		}
		if exit {
			return finishOfficeTx(ctx, tx, docID)
		}
		return nil
	})
	if err != nil {
		return saved, err
	}
	if old != nil && string(old) != string(st.SHA256[:]) {
		s.purgeContent(ctx, [][]byte{old}) // intermediate states are not kept
	}
	return saved, nil
}

// ExpireOfficeSessions ends sessions whose lock expired (Collabora crashed
// or lost its connection) and deletes expired tokens.
func (s *Service) ExpireOfficeSessions(ctx context.Context) (int, error) {
	if _, err := s.DB.Exec(ctx, `DELETE FROM wopi_tokens WHERE expires_at < now()`); err != nil {
		return 0, err
	}
	rows, err := s.DB.Query(ctx, `SELECT document_id::text FROM office_sessions WHERE expires_at <= now()`)
	if err != nil {
		return 0, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return 0, err
	}
	n := 0
	for _, id := range ids {
		var ended bool
		err := s.tx(ctx, func(tx pgx.Tx) error {
			ended = false
			r, err := lockOfficeTx(ctx, tx, id)
			if err != nil || !r.exists || !r.expired { // refreshed meanwhile
				return err
			}
			ended = true
			return finishOfficeTx(ctx, tx, id)
		})
		if err != nil {
			return n, err
		}
		if ended {
			n++
		}
	}
	return n, nil
}
