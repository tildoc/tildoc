package core

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// JobTransfer runs an import transfer.
const JobTransfer = "transfer"

// Transfer is an import job: the uploaded file is staged on the server and
// applied by a worker.
type Transfer struct {
	ID         string          `json:"id"`
	Kind       string          `json:"kind"` // import_bundle | import_zip
	Status     string          `json:"status"`
	Options    ImportOptions   `json:"options"`
	Size       int64           `json:"size"`
	Progress   float64         `json:"progress"`
	Result     json.RawMessage `json:"result"`
	Error      string          `json:"error,omitempty"`
	CreatedAt  time.Time       `json:"created_at"`
	FinishedAt *time.Time      `json:"finished_at"`
}

func (s *Service) transferDir() string { return filepath.Join(s.Cfg.DataDir, "transfers") }

// CreateTransfer stages an uploaded bundle or ZIP and queues its import.
func (s *Service) CreateTransfer(ctx context.Context, p *Principal, kind string, r io.Reader, opt ImportOptions) (*Transfer, error) {
	if err := requireSession(p); err != nil {
		return nil, err
	}
	if p.ReadOnly {
		return nil, errf(ErrForbidden, "read-only access")
	}
	if kind != "import_bundle" && kind != "import_zip" {
		return nil, errf(ErrInvalid, "unknown transfer kind %q", kind)
	}
	if kind == "import_zip" && opt.TargetFolderID == "" {
		return nil, errf(ErrInvalid, "a ZIP is imported into a folder")
	}
	if opt.TargetFolderID != "" {
		f, err := s.GetFolder(ctx, p, opt.TargetFolderID)
		if err != nil {
			return nil, err
		}
		if err := s.requireCap(ctx, s.DB, p, f.SpaceID, CapAdd); err != nil {
			return nil, err
		}
	}
	if err := os.MkdirAll(s.transferDir(), 0o700); err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp(s.transferDir(), "upload-*")
	if err != nil {
		return nil, err
	}
	limit := s.Cfg.MaxUploadBytes
	n, err := io.Copy(tmp, io.LimitReader(r, limit+1))
	if cerr := tmp.Close(); err == nil {
		err = cerr // a failed close means a possibly incomplete file
	}
	if err == nil && n > limit {
		err = errf(ErrTooLarge, "file exceeds the upload limit")
	}
	if err != nil {
		os.Remove(tmp.Name())
		return nil, err
	}
	optJSON, _ := json.Marshal(opt)
	t := &Transfer{Kind: kind, Options: opt, Size: n, Status: "queued"}
	err = s.tx(ctx, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `INSERT INTO transfers(kind, user_id, options, file, size) VALUES($1,$2,$3,$4,$5)
			RETURNING id::text, created_at`, kind, p.UserID, optJSON, tmp.Name(), n).Scan(&t.ID, &t.CreatedAt); err != nil {
			return err
		}
		return Enqueue(ctx, tx, JobTransfer, map[string]string{"id": t.ID}, JobTransfer+":"+t.ID, time.Time{})
	})
	if err != nil {
		os.Remove(tmp.Name())
		return nil, err
	}
	return t, nil
}

const transferSelect = `SELECT id::text, kind, status, options, size, progress, result, error, created_at, finished_at FROM transfers`

func scanTransfer(row pgx.Row) (*Transfer, error) {
	t := &Transfer{}
	var opts []byte
	err := row.Scan(&t.ID, &t.Kind, &t.Status, &opts, &t.Size, &t.Progress, &t.Result, &t.Error, &t.CreatedAt, &t.FinishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errf(ErrNotFound, "transfer not found")
	}
	if err == nil {
		json.Unmarshal(opts, &t.Options) //nolint:errcheck // only shown; a status list must not fail on it
	}
	return t, err
}

// GetTransfer returns one of the caller's transfers.
func (s *Service) GetTransfer(ctx context.Context, p *Principal, id string) (*Transfer, error) {
	if !looksLikeUUID(id) {
		return nil, errf(ErrNotFound, "transfer not found")
	}
	return scanTransfer(s.DB.QueryRow(ctx, transferSelect+` WHERE id=$1 AND user_id=$2`, id, p.UserID))
}

// ListTransfers lists the caller's recent transfers.
func (s *Service) ListTransfers(ctx context.Context, p *Principal) ([]Transfer, error) {
	rows, err := s.DB.Query(ctx, transferSelect+` WHERE user_id=$1 ORDER BY created_at DESC LIMIT 50`, p.UserID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Transfer{}
	for rows.Next() {
		t, err := scanTransfer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

// principalOf builds the principal of a stored user (for background jobs
// acting on behalf of the user who started them).
func (s *Service) principalOf(ctx context.Context, userID string) (*Principal, error) {
	p := &Principal{UserID: userID, Via: "session"}
	var inst []string
	err := s.DB.QueryRow(ctx, `SELECT username, instance_permissions(id) FROM users WHERE id=$1 AND status = 'active'`, userID).Scan(&p.Username, &inst)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errf(ErrForbidden, "user no longer active")
	}
	p.Inst = instFromNames(inst)
	return p, err
}

// RunTransfer applies a staged transfer (worker). Failures are recorded on
// the transfer; the staged file is removed either way.
func (s *Service) RunTransfer(ctx context.Context, id string) error {
	var userID, kind, file, status string
	var opts []byte
	err := s.DB.QueryRow(ctx, `UPDATE transfers SET status='running' WHERE id=$1 AND status IN ('queued','running')
		RETURNING user_id::text, kind, file, status, options`, id).Scan(&userID, &kind, &file, &status, &opts)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // finished already
	}
	if err != nil {
		return err
	}
	var opt ImportOptions
	optErr := json.Unmarshal(opts, &opt)
	finish := func(result any, runErr error) error {
		os.Remove(file)
		res, _ := json.Marshal(result)
		if runErr != nil || string(res) == "null" {
			res = []byte("{}")
		}
		st, msg := "done", ""
		if runErr != nil {
			st, msg = "failed", runErr.Error()
			var ce *Error
			if errors.As(runErr, &ce) {
				msg = ce.Msg
			}
		}
		_, err := s.DB.Exec(context.Background(), `UPDATE transfers SET status=$2, result=$3, error=$4, progress=1, finished_at=now() WHERE id=$1`,
			id, st, res, msg)
		return err
	}
	if optErr != nil {
		return finish(nil, fmt.Errorf("transfer options: %w", optErr))
	}
	p, err := s.principalOf(ctx, userID)
	if err != nil {
		return finish(nil, err)
	}
	last := time.Now()
	progress := func(f float64) {
		if time.Since(last) > time.Second {
			last = time.Now()
			s.DB.Exec(context.Background(), `UPDATE transfers SET progress=$2 WHERE id=$1`, id, f*0.95) //nolint:errcheck // only a progress display
		}
	}
	switch kind {
	case "import_bundle":
		f, err := os.Open(file)
		if err != nil {
			return finish(nil, err)
		}
		res, err := s.ImportBundle(ctx, p, f, nil, opt, progress)
		f.Close()
		return finish(res, err)
	case "import_zip":
		res, err := s.ImportZip(ctx, p, opt.TargetFolderID, file, progress)
		return finish(res, err)
	}
	return finish(nil, errf(ErrInvalid, "unknown transfer kind"))
}

// ZipImportResult summarizes a ZIP import.
type ZipImportResult struct {
	Files        int `json:"files"`
	Deduplicated int `json:"deduplicated"`
	NewVersions  int `json:"new_versions"`
	Folders      int `json:"folders"`
}

// ImportZip unpacks a ZIP into a folder through the normal upload path
// (deduplication, versions on name clashes, provenance per import batch).
func (s *Service) ImportZip(ctx context.Context, p *Principal, folderID, file string, progress func(float64)) (*ZipImportResult, error) {
	folder, err := s.GetFolder(ctx, p, folderID)
	if err != nil {
		return nil, err
	}
	if err := s.requireCap(ctx, s.DB, p, folder.SpaceID, CapAdd); err != nil {
		return nil, err
	}
	zr, err := zip.OpenReader(file)
	if err != nil {
		return nil, errf(ErrInvalid, "not a ZIP archive")
	}
	defer zr.Close()
	if len(zr.File) > 200000 {
		return nil, errf(ErrInvalid, "the archive has too many entries")
	}
	base, err := SplitPath(folder.Path)
	if err != nil {
		return nil, err
	}
	im, err := s.CreateImport(ctx, p, folder.SpaceID, "web", strings.TrimSuffix(filepath.Base(file), filepath.Ext(file)))
	if err != nil {
		return nil, err
	}
	res := &ZipImportResult{}
	for i, f := range zr.File {
		name := strings.ReplaceAll(f.Name, "\\", "/")
		if strings.HasPrefix(name, "__MACOSX/") || strings.HasSuffix(name, "/.DS_Store") || name == ".DS_Store" {
			continue
		}
		segs, err := SplitPath(name)
		if err != nil {
			return res, errf(ErrInvalid, "invalid name in archive %q: %v", f.Name, err)
		}
		if len(segs) == 0 {
			continue
		}
		path := append(append([]string{}, base...), segs...)
		if f.FileInfo().IsDir() {
			if _, err := s.MkdirAll(ctx, p, folder.SpaceID, path); err != nil {
				return res, err
			}
			res.Folders++
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return res, errf(ErrInvalid, "damaged archive entry %q", f.Name)
		}
		r, err := s.PutFile(ctx, p, folder.SpaceID, path, rc, PutOptions{Protocol: "web", ImportID: im.ID, CreateParents: true})
		rc.Close()
		if err != nil {
			return res, err
		}
		res.Files++
		if r.Deduplicated {
			res.Deduplicated++
		}
		if r.NewVersion {
			res.NewVersions++
		}
		if progress != nil {
			progress(float64(i+1) / float64(len(zr.File)))
		}
	}
	return res, nil
}

// PrincipalByUsername acts as a user (CLI commands run on behalf of a user).
func (s *Service) PrincipalByUsername(ctx context.Context, username string) (*Principal, error) {
	u, err := s.UserByName(ctx, username)
	if err != nil {
		return nil, err
	}
	return s.principalOf(ctx, u.ID)
}
