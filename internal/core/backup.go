package core

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5"
)

// JobBackup runs a server backup.
const JobBackup = "backup"

// Backup is a server backup: full (every blob) or differential (only blobs
// not contained in its base full backup; metadata is always complete).
type Backup struct {
	ID         string     `json:"id"`
	Mode       string     `json:"mode"`
	BaseID     *string    `json:"base_id"`
	Status     string     `json:"status"`
	File       string     `json:"file"`
	Size       int64      `json:"size"`
	BlobCount  int        `json:"blob_count"`
	Error      string     `json:"error,omitempty"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
}

// Settings are the server-wide settings.
type Settings struct {
	InstanceID    string `json:"instance_id"`
	OCRPreference string `json:"ocr_preference"` // existing | tildoc
	BackupKeep    int    `json:"backup_keep"`    // full backups kept (with their differentials)
	BackupDir     string `json:"backup_dir"`
	PublicLinks   bool   `json:"public_links"` // public and drop links may be created and used
}

// GetSettings returns the server settings (administrators).
func (s *Service) GetSettings(ctx context.Context, p *Principal) (*Settings, error) {
	if err := requireInst(p, InstSettings); err != nil {
		return nil, err
	}
	st := &Settings{BackupDir: s.Cfg.BackupDir}
	err := s.DB.QueryRow(ctx, `SELECT instance_id::text, ocr_preference, backup_keep, public_links FROM instance_settings`).
		Scan(&st.InstanceID, &st.OCRPreference, &st.BackupKeep, &st.PublicLinks)
	return st, err
}

// SettingsUpdate changes settings; nil fields stay unchanged.
type SettingsUpdate struct {
	OCRPreference *string `json:"ocr_preference"`
	BackupKeep    *int    `json:"backup_keep"`
	PublicLinks   *bool   `json:"public_links"`
}

// UpdateSettings changes the server settings (administrators).
func (s *Service) UpdateSettings(ctx context.Context, p *Principal, u SettingsUpdate) (*Settings, error) {
	if err := requireInst(p, InstSettings); err != nil {
		return nil, err
	}
	if u.OCRPreference != nil {
		if *u.OCRPreference != "existing" && *u.OCRPreference != "tildoc" {
			return nil, errf(ErrInvalid, "OCR preference must be existing or tildoc")
		}
		if _, err := s.DB.Exec(ctx, `UPDATE instance_settings SET ocr_preference=$1, updated_at=now()`, *u.OCRPreference); err != nil {
			return nil, err
		}
	}
	if u.PublicLinks != nil {
		if _, err := s.DB.Exec(ctx, `UPDATE instance_settings SET public_links=$1, updated_at=now()`, *u.PublicLinks); err != nil {
			return nil, err
		}
	}
	if u.BackupKeep != nil {
		if *u.BackupKeep < 1 || *u.BackupKeep > 100 {
			return nil, errf(ErrInvalid, "keep between 1 and 100 full backups")
		}
		if _, err := s.DB.Exec(ctx, `UPDATE instance_settings SET backup_keep=$1, updated_at=now()`, *u.BackupKeep); err != nil {
			return nil, err
		}
	}
	return s.GetSettings(ctx, p)
}

const backupSelect = `SELECT id::text, mode, base_id::text, status, file, size, blob_count, error, started_at, finished_at FROM backups`

func scanBackup(row pgx.Row) (*Backup, error) {
	b := &Backup{}
	err := row.Scan(&b.ID, &b.Mode, &b.BaseID, &b.Status, &b.File, &b.Size, &b.BlobCount, &b.Error, &b.StartedAt, &b.FinishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errf(ErrNotFound, "backup not found")
	}
	return b, err
}

// ListBackups lists backups, newest first (administrators).
func (s *Service) ListBackups(ctx context.Context, p *Principal) ([]Backup, error) {
	if err := requireInst(p, InstBackups); err != nil {
		return nil, err
	}
	rows, err := s.DB.Query(ctx, backupSelect+` ORDER BY started_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Backup{}
	for rows.Next() {
		b, err := scanBackup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *b)
	}
	return out, rows.Err()
}

// createBackupTx registers a backup; differentials use the newest finished
// full backup as base.
func (s *Service) createBackupTx(ctx context.Context, tx pgx.Tx, p *Principal, mode string) (*Backup, error) {
	if mode != "full" && mode != "differential" {
		return nil, errf(ErrInvalid, "mode must be full or differential")
	}
	var base *string
	if mode == "differential" {
		var id string
		err := tx.QueryRow(ctx, `SELECT id::text FROM backups WHERE mode='full' AND status='done' ORDER BY started_at DESC LIMIT 1`).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, errf(ErrInvalid, "a differential backup needs a full backup first")
		}
		if err != nil {
			return nil, err
		}
		base = &id
	}
	return scanBackup(tx.QueryRow(ctx, `INSERT INTO backups(mode, base_id, created_by) VALUES($1,$2,$3)
		RETURNING id::text, mode, base_id::text, status, file, size, blob_count, error, started_at, finished_at`, mode, base, p.userRef()))
}

// StartBackup queues a backup for the worker (administrators).
func (s *Service) StartBackup(ctx context.Context, p *Principal, mode string) (*Backup, error) {
	if err := requireInst(p, InstBackups); err != nil {
		return nil, err
	}
	var b *Backup
	err := s.tx(ctx, func(tx pgx.Tx) error {
		var err error
		if b, err = s.createBackupTx(ctx, tx, p, mode); err != nil {
			return err
		}
		return Enqueue(ctx, tx, JobBackup, map[string]string{"id": b.ID}, JobBackup+":"+b.ID, time.Time{})
	})
	return b, err
}

// BackupNow creates and runs a backup synchronously (CLI).
func (s *Service) BackupNow(ctx context.Context, mode string) (*Backup, error) {
	var b *Backup
	if err := s.tx(ctx, func(tx pgx.Tx) error {
		var err error
		b, err = s.createBackupTx(ctx, tx, SystemPrincipal, mode)
		return err
	}); err != nil {
		return nil, err
	}
	if err := s.RunBackup(ctx, b.ID); err != nil {
		return nil, err
	}
	return scanBackup(s.DB.QueryRow(ctx, backupSelect+` WHERE id=$1`, b.ID))
}

// serverManifest snapshots the whole instance.
func (s *Service) serverManifest(ctx context.Context, b *Backup, exclude map[string]bool) (*Manifest, error) {
	var m *Manifest
	err := s.snapshot(ctx, func(tx pgx.Tx) error {
		mb, err := newManifestBuilder(ctx, tx, "server")
		if err != nil {
			return err
		}
		m = mb.m
		m.Backup = &MBackup{ID: b.ID, Mode: b.Mode, BaseID: b.BaseID}
		m.Settings = &MSettings{}
		if err := tx.QueryRow(ctx, `SELECT instance_id::text, ocr_preference, backup_keep, public_links FROM instance_settings`).
			Scan(&m.Settings.InstanceID, &m.Settings.OCRPreference, &m.Settings.BackupKeep, &m.Settings.PublicLinks); err != nil {
			return err
		}
		if err := collectRows(ctx, tx, `SELECT id::text, username, display_name, disabled, pdf_viewer, created_at
			FROM users ORDER BY created_at, id`, func(r pgx.Rows) error {
			var u MUser
			if err := r.Scan(&u.ID, &u.Username, &u.DisplayName, &u.Disabled, &u.PDFViewer, &u.CreatedAt); err != nil {
				return err
			}
			m.Users = append(m.Users, u)
			return nil
		}); err != nil {
			return err
		}
		if err := collectRows(ctx, tx, `SELECT id::text, user_id::text, provider, subject, password_hash, created_at, last_login_at
			FROM identities ORDER BY created_at, id`, func(r pgx.Rows) error {
			var i MIdentity
			if err := r.Scan(&i.ID, &i.UserID, &i.Provider, &i.Subject, &i.PasswordHash, &i.CreatedAt, &i.LastLoginAt); err != nil {
				return err
			}
			m.Identities = append(m.Identities, i)
			return nil
		}); err != nil {
			return err
		}
		if err := collectRows(ctx, tx, `SELECT slug, name, scope, built_in,
			ARRAY(SELECT permission FROM role_permissions WHERE role_id = r.id ORDER BY permission) FROM roles r ORDER BY slug`, func(r pgx.Rows) error {
			var x MRole
			if err := r.Scan(&x.Slug, &x.Name, &x.Scope, &x.BuiltIn, &x.Permissions); err != nil {
				return err
			}
			m.Roles = append(m.Roles, x)
			return nil
		}); err != nil {
			return err
		}
		if err := collectRows(ctx, tx, `SELECT g.id::text, g.space_id::text, g.document_id::text, g.user_id::text, g.team_id::text, r.slug,
			ARRAY(SELECT permission FROM grant_permissions WHERE grant_id = g.id ORDER BY permission), g.created_by::text, g.created_at
			FROM grants g LEFT JOIN roles r ON r.id = g.role_id ORDER BY g.created_at, g.id`, func(r pgx.Rows) error {
			var g MGrant
			if err := r.Scan(&g.ID, &g.SpaceID, &g.DocumentID, &g.UserID, &g.TeamID, &g.Role, &g.Permissions, &g.CreatedBy, &g.CreatedAt); err != nil {
				return err
			}
			m.Grants = append(m.Grants, g)
			return nil
		}); err != nil {
			return err
		}
		if err := collectRows(ctx, tx, `SELECT id::text, token_hash, space_id::text, document_id::text, folder_id::text, kind, preset, label, password_hash,
			expires_at, created_by::text, created_at, revoked_at FROM share_links ORDER BY created_at, id`, func(r pgx.Rows) error {
			var l MShareLink
			if err := r.Scan(&l.ID, &l.TokenHash, &l.SpaceID, &l.DocumentID, &l.FolderID, &l.Kind, &l.Preset, &l.Label, &l.PasswordHash,
				&l.ExpiresAt, &l.CreatedBy, &l.CreatedAt, &l.RevokedAt); err != nil {
				return err
			}
			m.ShareLinks = append(m.ShareLinks, l)
			return nil
		}); err != nil {
			return err
		}
		if err := collectRows(ctx, tx, `SELECT k.id::text, k.user_id::text, k.secret_hash, k.label, k.expires_at, k.created_at, k.last_used_at, k.revoked_at,
			ARRAY(SELECT permission FROM api_key_permissions WHERE key_id = k.id ORDER BY permission),
			ARRAY(SELECT space_id::text FROM api_key_spaces WHERE key_id = k.id ORDER BY space_id) FROM api_keys k ORDER BY k.created_at, k.id`, func(r pgx.Rows) error {
			var k MAPIKey
			if err := r.Scan(&k.ID, &k.UserID, &k.SecretHash, &k.Label, &k.ExpiresAt, &k.CreatedAt, &k.LastUsedAt, &k.RevokedAt, &k.Permissions, &k.SpaceIDs); err != nil {
				return err
			}
			m.APIKeys = append(m.APIKeys, k)
			return nil
		}); err != nil {
			return err
		}
		if err := collectRows(ctx, tx, `SELECT id::text, name, created_at FROM teams ORDER BY created_at, id`, func(r pgx.Rows) error {
			var t MTeam
			if err := r.Scan(&t.ID, &t.Name, &t.CreatedAt); err != nil {
				return err
			}
			t.Members = []MMember{}
			m.Teams = append(m.Teams, t)
			return nil
		}); err != nil {
			return err
		}
		for i := range m.Teams {
			t := &m.Teams[i]
			if err := collectRows(ctx, tx, `SELECT user_id::text, is_manager, created_at FROM team_members WHERE team_id=$1 ORDER BY user_id`, func(r pgx.Rows) error {
				var mm MMember
				if err := r.Scan(&mm.UserID, &mm.IsManager, &mm.CreatedAt); err != nil {
					return err
				}
				t.Members = append(t.Members, mm)
				return nil
			}, t.ID); err != nil {
				return err
			}
		}
		var spaceIDs []string
		if err := collectRows(ctx, tx, `SELECT id::text FROM spaces ORDER BY created_at, id`, func(r pgx.Rows) error {
			var id string
			if err := r.Scan(&id); err != nil {
				return err
			}
			spaceIDs = append(spaceIDs, id)
			return nil
		}); err != nil {
			return err
		}
		for _, id := range spaceIDs {
			sp, err := mb.space(id, true)
			if err != nil {
				return err
			}
			m.Spaces = append(m.Spaces, *sp)
		}
		if err := mb.collections(`true OR $1::text IS NULL`, "", nil); err != nil {
			return err
		}
		if err := collectRows(ctx, tx, `SELECT id::text, user_id::text, space_id::text, protocol, client_root, created_at, last_activity_at
			FROM imports ORDER BY created_at, id`, func(r pgx.Rows) error {
			var im MImport
			if err := r.Scan(&im.ID, &im.UserID, &im.SpaceID, &im.Protocol, &im.ClientRoot, &im.CreatedAt, &im.LastActivityAt); err != nil {
				return err
			}
			m.Imports = append(m.Imports, im)
			return nil
		}); err != nil {
			return err
		}
		if err := collectRows(ctx, tx, `SELECT id, user_id::text, secret_enc, label, space_id::text, read_only, created_at, last_used_at, revoked_at
			FROM access_keys ORDER BY created_at, id`, func(r pgx.Rows) error {
			var k MAccessKey
			if err := r.Scan(&k.ID, &k.UserID, &k.SecretEnc, &k.Label, &k.SpaceID, &k.ReadOnly, &k.CreatedAt, &k.LastUsedAt, &k.RevokedAt); err != nil {
				return err
			}
			m.AccessKeys = append(m.AccessKeys, k)
			return nil
		}); err != nil {
			return err
		}
		if err := collectRows(ctx, tx, `SELECT id::text, user_id::text, fingerprint, public_key, label, created_at, last_used_at
			FROM ssh_keys ORDER BY created_at, id`, func(r pgx.Rows) error {
			var k MSSHKey
			if err := r.Scan(&k.ID, &k.UserID, &k.Fingerprint, &k.PublicKey, &k.Label, &k.CreatedAt, &k.LastUsedAt); err != nil {
				return err
			}
			m.SSHKeys = append(m.SSHKeys, k)
			return nil
		}); err != nil {
			return err
		}
		// all tags (not only used ones)
		if err := collectRows(ctx, tx, `SELECT id::text FROM tags`, func(r pgx.Rows) error {
			var id string
			if err := r.Scan(&id); err != nil {
				return err
			}
			mb.tags[id] = true
			return nil
		}); err != nil {
			return err
		}
		return mb.finish(exclude)
	})
	if m != nil {
		m.People = []MPerson{}
	}
	return m, err
}

func collectRows(ctx context.Context, tx pgx.Tx, sql string, fn func(pgx.Rows) error, args ...any) error {
	rows, err := tx.Query(ctx, sql, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := fn(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

// RunBackup writes a queued backup to the backup directory (worker / CLI).
func (s *Service) RunBackup(ctx context.Context, id string) (err error) {
	b, err := scanBackup(s.DB.QueryRow(ctx, `UPDATE backups SET status='running' WHERE id=$1 AND status IN ('queued','running')
		RETURNING id::text, mode, base_id::text, status, file, size, blob_count, error, started_at, finished_at`, id))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		return err
	}
	var tmpName string
	defer func() {
		if err != nil {
			if tmpName != "" {
				os.Remove(tmpName)
			}
			msg := err.Error()
			s.DB.Exec(context.Background(), `UPDATE backups SET status='failed', error=$2, finished_at=now() WHERE id=$1`, id, msg)
			s.Log.Error("backup failed", "id", id, "err", err)
		}
	}()
	exclude := map[string]bool{}
	if b.BaseID != nil {
		rows, err := s.DB.Query(ctx, `SELECT sha256 FROM backup_blobs WHERE backup_id=$1`, *b.BaseID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var sha []byte
			rows.Scan(&sha)
			exclude[hex.EncodeToString(sha)] = true
		}
		rows.Close()
	}
	m, err := s.serverManifest(ctx, b, exclude)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.Cfg.BackupDir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.Cfg.BackupDir, ".backup-*")
	if err != nil {
		return err
	}
	tmpName = tmp.Name()
	n, err := s.writeBundle(ctx, m, tmp, nil)
	if err == nil {
		err = tmp.Sync()
	}
	tmp.Close()
	if err != nil {
		return err
	}
	name := fmt.Sprintf("tildoc-%s-%s-%s.dtbackup", b.StartedAt.UTC().Format("20060102-150405"), b.Mode, b.ID[:8])
	final := filepath.Join(s.Cfg.BackupDir, name)
	if err := os.Rename(tmpName, final); err != nil {
		return err
	}
	tmpName = ""
	st, err := os.Stat(final)
	if err != nil {
		return err
	}
	err = s.tx(ctx, func(tx pgx.Tx) error {
		if b.Mode == "full" {
			shas := make([][]byte, 0, len(m.Blobs))
			for _, mb := range m.Blobs {
				raw, _ := hex.DecodeString(mb.SHA256)
				shas = append(shas, raw)
			}
			if _, err := tx.Exec(ctx, `INSERT INTO backup_blobs(backup_id, sha256) SELECT $1, unnest($2::bytea[])`, id, shas); err != nil {
				return err
			}
		}
		_, err := tx.Exec(ctx, `UPDATE backups SET status='done', file=$2, size=$3, blob_count=$4, finished_at=now() WHERE id=$1`,
			id, name, st.Size(), n)
		return err
	})
	if err != nil {
		return err
	}
	if b.Mode == "full" {
		if err := s.applyBackupRetention(ctx); err != nil {
			s.Log.Warn("backup retention", "err", err)
		}
	}
	return nil
}

// applyBackupRetention keeps the newest N full backups and their
// differentials.
func (s *Service) applyBackupRetention(ctx context.Context) error {
	var keep int
	if err := s.DB.QueryRow(ctx, `SELECT backup_keep FROM instance_settings`).Scan(&keep); err != nil {
		return err
	}
	rows, err := s.DB.Query(ctx, `SELECT id::text FROM backups WHERE mode='full' AND status='done' ORDER BY started_at DESC OFFSET $1`, keep)
	if err != nil {
		return err
	}
	var old []string
	for rows.Next() {
		var id string
		rows.Scan(&id)
		old = append(old, id)
	}
	rows.Close()
	for _, id := range old {
		if err := s.deleteBackup(ctx, id, true); err != nil {
			return err
		}
	}
	return nil
}

// DeleteBackup deletes a backup and its file (administrators). A full
// backup with differentials cannot be deleted on its own.
func (s *Service) DeleteBackup(ctx context.Context, p *Principal, id string) error {
	if err := requireInst(p, InstBackups); err != nil {
		return err
	}
	if !looksLikeUUID(id) {
		return errf(ErrNotFound, "backup not found")
	}
	return s.deleteBackup(ctx, id, false)
}

func (s *Service) deleteBackup(ctx context.Context, id string, withDependents bool) error {
	var deps []Backup
	rows, err := s.DB.Query(ctx, backupSelect+` WHERE base_id=$1`, id)
	if err != nil {
		return err
	}
	for rows.Next() {
		b, err := scanBackup(rows)
		if err != nil {
			rows.Close()
			return err
		}
		deps = append(deps, *b)
	}
	rows.Close()
	if len(deps) > 0 && !withDependents {
		return errf(ErrConflict, "%d differential backups depend on this full backup; delete them first", len(deps))
	}
	for _, d := range deps {
		if err := s.deleteBackup(ctx, d.ID, true); err != nil {
			return err
		}
	}
	b, err := scanBackup(s.DB.QueryRow(ctx, backupSelect+` WHERE id=$1`, id))
	if err != nil {
		return err
	}
	if b.Status == "running" || b.Status == "queued" {
		return errf(ErrConflict, "the backup is still running")
	}
	if b.File != "" {
		if err := os.Remove(filepath.Join(s.Cfg.BackupDir, filepath.Base(b.File))); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	_, err = s.DB.Exec(ctx, `DELETE FROM backups WHERE id=$1`, id)
	return err
}

// ---------------------------------------------------------------- verify & restore

// BackupSummary describes verified backup files.
type BackupSummary struct {
	BackupID  string    `json:"backup_id"`
	Mode      string    `json:"mode"`
	CreatedAt time.Time `json:"created_at"`
	Users     int       `json:"users"`
	Spaces    int       `json:"spaces"`
	Documents int       `json:"documents"`
	Blobs     int       `json:"blobs"`
}

// readBackupFiles opens a full backup and an optional differential and
// returns the authoritative manifest (the newest) and the archives in order.
func readBackupFiles(files []string) (*Manifest, error) {
	if len(files) == 0 || len(files) > 2 {
		return nil, errf(ErrInvalid, "give the full backup and optionally one differential backup")
	}
	var ms []*Manifest
	for _, f := range files {
		fh, err := os.Open(f)
		if err != nil {
			return nil, err
		}
		m, err := readManifest(tar.NewReader(fh))
		fh.Close()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		if err := validateManifest(m, true); err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		if m.Backup == nil {
			return nil, errf(ErrInvalid, "%s is not a server backup", f)
		}
		ms = append(ms, m)
	}
	if ms[0].Backup.Mode != "full" {
		return nil, errf(ErrInvalid, "the first file must be a full backup")
	}
	if len(ms) == 2 && (ms[1].Backup.Mode != "differential" || ms[1].Backup.BaseID == nil || *ms[1].Backup.BaseID != ms[0].Backup.ID) {
		return nil, errf(ErrInvalid, "the second file must be a differential backup of the first")
	}
	return ms[len(ms)-1], nil
}

// VerifyBackup checks backup files: manifests, the SHA-256 of every
// included blob, and that every referenced blob is present.
func VerifyBackup(files []string) (*BackupSummary, error) {
	m, err := readBackupFiles(files)
	if err != nil {
		return nil, err
	}
	present := map[string]bool{}
	for _, f := range files {
		fh, err := os.Open(f)
		if err != nil {
			return nil, err
		}
		tr := tar.NewReader(fh)
		if _, err := readManifest(tr); err != nil {
			fh.Close()
			return nil, err
		}
		for {
			hdr, err := tr.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				fh.Close()
				return nil, errf(ErrInvalid, "%s is damaged: %v", f, err)
			}
			if len(hdr.Name) > 6 && hdr.Name[:6] == "blobs/" {
				h := sha256.New()
				if _, err := io.Copy(h, tr); err != nil {
					fh.Close()
					return nil, err
				}
				got := hex.EncodeToString(h.Sum(nil))
				if got != hdr.Name[6:] {
					fh.Close()
					return nil, errf(ErrInvalid, "%s: content %s is damaged", f, hdr.Name[6:])
				}
				present[got] = true
			}
		}
		fh.Close()
	}
	sum := &BackupSummary{BackupID: m.Backup.ID, Mode: m.Backup.Mode, CreatedAt: m.CreatedAt, Users: len(m.Users), Spaces: len(m.Spaces), Blobs: len(m.Blobs)}
	for _, sp := range m.Spaces {
		sum.Documents += len(sp.Documents)
	}
	for _, mb := range m.Blobs {
		if !present[mb.SHA256] {
			return nil, errf(ErrInvalid, "content %s is missing (wrong base backup?)", mb.SHA256)
		}
	}
	return sum, nil
}

// RestoreServer restores a full backup (plus optionally its newest
// differential) into an empty instance, keeping every UUID.
func (s *Service) RestoreServer(ctx context.Context, files []string) (*BackupSummary, error) {
	sum, err := VerifyBackup(files)
	if err != nil {
		return nil, err
	}
	var users int
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&users); err != nil {
		return nil, err
	}
	if users > 0 {
		return nil, errf(ErrConflict, "restore needs an empty instance (this one has %d users)", users)
	}
	m, err := readBackupFiles(files)
	if err != nil {
		return nil, err
	}
	br := newBundleReader(s, m)
	br.skipUnknown = true
	for _, mb := range br.blobs {
		mb.Included = true // read from whichever file holds it
	}
	for _, f := range files {
		fh, err := os.Open(f)
		if err != nil {
			return nil, err
		}
		tr := tar.NewReader(fh)
		if _, err := readManifest(tr); err != nil {
			fh.Close()
			return nil, err
		}
		err = br.readEntries(tr, nil)
		fh.Close()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
	}
	err = s.tx(ctx, func(tx pgx.Tx) error { return s.applyRestore(ctx, tx, br) })
	if err != nil {
		return nil, err
	}
	return sum, nil
}

func (s *Service) applyRestore(ctx context.Context, tx pgx.Tx, br *bundleReader) error {
	m := br.m
	ex := func(sql string, args ...any) error {
		_, err := tx.Exec(ctx, sql, args...)
		return err
	}
	if err := ex(`UPDATE instance_settings SET instance_id=$1, ocr_preference=$2, backup_keep=$3, public_links=$4, updated_at=now()`,
		m.Settings.InstanceID, m.Settings.OCRPreference, m.Settings.BackupKeep, m.Settings.PublicLinks); err != nil {
		return err
	}
	for _, u := range m.Users {
		if err := ex(`INSERT INTO users(id, username, display_name, disabled, pdf_viewer, created_at)
			VALUES($1,$2,$3,$4,$5,$6)`, u.ID, u.Username, u.DisplayName, u.Disabled, u.PDFViewer, u.CreatedAt); err != nil {
			return err
		}
	}
	for _, i := range m.Identities {
		if err := ex(`INSERT INTO identities(id, user_id, provider, subject, password_hash, created_at, last_login_at) VALUES($1,$2,$3,$4,$5,$6,$7)`,
			i.ID, i.UserID, i.Provider, i.Subject, i.PasswordHash, i.CreatedAt, i.LastLoginAt); err != nil {
			return err
		}
	}
	for _, x := range m.Roles {
		if err := ex(`INSERT INTO roles(slug, name, scope, built_in) VALUES($1,$2,$3,$4) ON CONFLICT (slug) DO UPDATE SET name = EXCLUDED.name`,
			x.Slug, x.Name, x.Scope, x.BuiltIn); err != nil {
			return err
		}
		if err := ex(`DELETE FROM role_permissions WHERE role_id = (SELECT id FROM roles WHERE slug=$1)`, x.Slug); err != nil {
			return err
		}
		if err := ex(`INSERT INTO role_permissions(role_id, permission) SELECT r.id, unnest($2::text[]) FROM roles r WHERE r.slug=$1`, x.Slug, x.Permissions); err != nil {
			return err
		}
	}
	for _, t := range m.Teams {
		if err := ex(`INSERT INTO teams(id, name, created_at) VALUES($1,$2,$3)`, t.ID, t.Name, t.CreatedAt); err != nil {
			return err
		}
		for _, mm := range t.Members {
			if err := ex(`INSERT INTO team_members(team_id, user_id, is_manager, created_at) VALUES($1,$2,$3,$4)`, t.ID, mm.UserID, mm.IsManager, mm.CreatedAt); err != nil {
				return err
			}
		}
	}
	for _, sp := range m.Spaces {
		if err := ex(`INSERT INTO spaces(id, slug, name, owner_user_id, owner_team_id, created_at) VALUES($1,$2,$3,$4,$5,$6)`,
			sp.ID, sp.Slug, sp.Name, sp.OwnerUserID, sp.OwnerTeamID, sp.CreatedAt); err != nil {
			return err
		}
	}
	for _, im := range m.Imports {
		if err := ex(`INSERT INTO imports(id, user_id, space_id, protocol, client_root, created_at, last_activity_at) VALUES($1,$2,$3,$4,$5,$6,$7)`,
			im.ID, im.UserID, im.SpaceID, im.Protocol, im.ClientRoot, im.CreatedAt, im.LastActivityAt); err != nil {
			return err
		}
	}
	for _, mb := range m.Blobs {
		h := shaOf(mb.SHA256)
		md5, _ := hex.DecodeString(mb.MD5)
		if err := ex(`INSERT INTO blobs(sha256, md5, size, mime, created_at) VALUES($1,$2,$3,$4,$5)`, h[:], md5, mb.Size, mb.MIME, mb.CreatedAt); err != nil {
			return err
		}
		if pr := mb.Processing; pr != nil {
			if err := ex(`INSERT INTO blob_processing(sha256, status, kind, pages, error) VALUES($1,$2,$3,$4,$5)`,
				h[:], pr.Status, pr.Kind, pr.Pages, pr.Error); err != nil {
				return err
			}
			if pr.Status == StatusPending || pr.Status == StatusProcessing {
				if err := enqueueProcess(ctx, tx, mb.SHA256, true); err != nil {
					return err
				}
			}
		} else if err := ensureProcessingTx(ctx, tx, h); err != nil {
			return err
		}
		if text, ok := br.texts[mb.SHA256]; ok {
			if err := storeTextTx(ctx, tx, h, mb.TextMethod, text, true); err != nil {
				return err
			}
		}
	}
	for _, d := range m.Derivatives {
		src, der, in := shaOf(d.Source), shaOf(d.Derived), shaOf(d.Input)
		if d.Meta == nil {
			d.Meta = map[string]any{}
		}
		if err := ex(`INSERT INTO blob_derivatives(source_sha256, kind, derived_sha256, input_sha256, engine, meta, created_at)
			VALUES($1,$2,$3,$4,$5,$6,$7)`, src[:], d.Kind, der[:], in[:], d.Engine, d.Meta, d.CreatedAt); err != nil {
			return err
		}
	}
	for _, t := range m.Tags {
		if err := ex(`INSERT INTO tags(id, name, color, created_by, created_at) VALUES($1,$2,$3,$4,$5)`, t.ID, t.Name, t.Color, t.CreatedBy, t.CreatedAt); err != nil {
			return err
		}
	}
	var forks [][2]string
	for _, sp := range m.Spaces {
		for _, f := range sp.Folders {
			if err := ex(`INSERT INTO entries(id, space_id, kind, parent_id, name, created_at, created_by) VALUES($1,$2,'folder',$3,$4,$5,$6)`,
				f.ID, sp.ID, f.ParentID, f.Name, f.CreatedAt, f.CreatedBy); err != nil {
				return err
			}
		}
		for _, d := range sp.Documents {
			if err := ex(`INSERT INTO documents(id, space_id, current_version, created_at, updated_at, created_by, deleted_at)
				VALUES($1,$2,$3,$4,$5,$6,$7)`, d.ID, sp.ID, d.CurrentVersion, d.CreatedAt, d.UpdatedAt, d.CreatedBy, d.DeletedAt); err != nil {
				return err
			}
			if d.ForkedFrom != nil {
				forks = append(forks, [2]string{d.ID, *d.ForkedFrom})
			}
			for _, v := range d.Versions {
				h := shaOf(v.SHA256)
				if v.Meta == nil {
					v.Meta = map[string]any{}
				}
				if err := ex(`INSERT INTO document_versions(document_id, version, sha256, created_at, created_by, source, meta, restored_from)
					VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, d.ID, v.Version, h[:], v.CreatedAt, v.CreatedBy, v.Source, v.Meta, v.RestoredFrom); err != nil {
					return err
				}
			}
			for _, l := range d.Links {
				if err := ex(`INSERT INTO entries(id, space_id, kind, document_id, parent_id, name, import_id, original_path,
					created_at, created_by, updated_at, deleted_at, deleted_path) VALUES($1,$2,'link',$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
					l.ID, sp.ID, d.ID, l.FolderID, l.Name, l.ImportID, l.OriginalPath, l.CreatedAt, l.CreatedBy, l.UpdatedAt, l.DeletedAt, l.DeletedPath); err != nil {
					return err
				}
			}
			for _, t := range d.Tags {
				if err := ex(`INSERT INTO document_tags(document_id, tag_id) VALUES($1,$2)`, d.ID, t); err != nil {
					return err
				}
			}
		}
	}
	for _, f := range forks {
		if err := ex(`UPDATE documents SET forked_from=$2 WHERE id=$1 AND EXISTS (SELECT 1 FROM documents WHERE id=$2)`, f[0], f[1]); err != nil {
			return err
		}
	}
	for _, c := range m.Collections {
		if err := ex(`INSERT INTO collections(id, name, owner_user_id, owner_team_id, tag_id, created_by, created_at) VALUES($1,$2,$3,$4,$5,$6,$7)`,
			c.ID, c.Name, c.OwnerUserID, c.OwnerTeamID, c.TagID, c.CreatedBy, c.CreatedAt); err != nil {
			return err
		}
		for _, it := range c.Items {
			if err := ex(`INSERT INTO collection_items(collection_id, document_id, added_by, added_at) VALUES($1,$2,$3,$4)`,
				c.ID, it.DocumentID, it.AddedBy, it.AddedAt); err != nil {
				return err
			}
		}
	}
	for _, g := range m.Grants {
		if err := ex(`INSERT INTO grants(id, space_id, document_id, user_id, team_id, role_id, created_by, created_at)
			VALUES($1,$2::uuid,$3::uuid,$4::uuid,$5::uuid,(SELECT id FROM roles WHERE slug=$6),$7::uuid,$8)`,
			g.ID, g.SpaceID, g.DocumentID, g.UserID, g.TeamID, g.Role, g.CreatedBy, g.CreatedAt); err != nil {
			return err
		}
		if err := ex(`INSERT INTO grant_permissions(grant_id, permission) SELECT $1, unnest($2::text[])`, g.ID, g.Permissions); err != nil {
			return err
		}
	}
	for _, l := range m.ShareLinks {
		if err := ex(`INSERT INTO share_links(id, token_hash, space_id, document_id, folder_id, kind, preset, label, password_hash, expires_at, created_by, created_at, revoked_at)
			VALUES($1,$2,$3::uuid,$4::uuid,$5::uuid,$6,$7,$8,$9,$10,$11,$12,$13)`, l.ID, l.TokenHash, l.SpaceID, l.DocumentID, l.FolderID, l.Kind, l.Preset, l.Label,
			l.PasswordHash, l.ExpiresAt, l.CreatedBy, l.CreatedAt, l.RevokedAt); err != nil {
			return err
		}
	}
	for _, k := range m.APIKeys {
		if err := ex(`INSERT INTO api_keys(id, user_id, secret_hash, label, expires_at, created_at, last_used_at, revoked_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`,
			k.ID, k.UserID, k.SecretHash, k.Label, k.ExpiresAt, k.CreatedAt, k.LastUsedAt, k.RevokedAt); err != nil {
			return err
		}
		if err := ex(`INSERT INTO api_key_permissions(key_id, permission) SELECT $1, unnest($2::text[])`, k.ID, k.Permissions); err != nil {
			return err
		}
		if err := ex(`INSERT INTO api_key_spaces(key_id, space_id) SELECT $1, unnest($2::uuid[])`, k.ID, k.SpaceIDs); err != nil {
			return err
		}
	}
	for _, k := range m.AccessKeys {
		if err := ex(`INSERT INTO access_keys(id, user_id, secret_enc, label, space_id, read_only, created_at, last_used_at, revoked_at)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, k.ID, k.UserID, k.SecretEnc, k.Label, k.SpaceID, k.ReadOnly, k.CreatedAt, k.LastUsedAt, k.RevokedAt); err != nil {
			return err
		}
	}
	for _, k := range m.SSHKeys {
		if err := ex(`INSERT INTO ssh_keys(id, user_id, fingerprint, public_key, label, created_at, last_used_at) VALUES($1,$2,$3,$4,$5,$6,$7)`,
			k.ID, k.UserID, k.Fingerprint, k.PublicKey, k.Label, k.CreatedAt, k.LastUsedAt); err != nil {
			return err
		}
	}
	return nil
}
