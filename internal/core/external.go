package core

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"
)

// ExternalRef is an application's own id for a document. It makes uploads safe
// to retry: the same reference always means the same document.
type ExternalRef struct {
	SpaceID    string    `json:"space_id"`
	Source     string    `json:"source"`
	Ref        string    `json:"ref"`
	DocumentID string    `json:"document_id"`
	CreatedAt  time.Time `json:"created_at"`
}

var externalSourceRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

func validateExternal(source, ref string) error {
	if !externalSourceRe.MatchString(source) {
		return errf(ErrInvalid, "source: lower-case letters, digits, dots, dashes and underscores, at most 64")
	}
	if ref == "" || len(ref) > 255 || strings.ContainsFunc(ref, unicode.IsControl) {
		return errf(ErrInvalid, "ref: 1 to 255 bytes without control characters")
	}
	return nil
}

func scanExternal(row pgx.Row) (*ExternalRef, error) {
	e := &ExternalRef{}
	err := row.Scan(&e.SpaceID, &e.Source, &e.Ref, &e.DocumentID, &e.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errf(ErrNotFound, "no such reference")
	}
	return e, err
}

const externalSelect = `SELECT space_id::text, source, ref, document_id::text, created_at FROM external_refs`

// GetExternal finds a reference.
func (s *Service) GetExternal(ctx context.Context, p *Principal, spaceID, source, ref string) (*ExternalRef, error) {
	if err := validateExternal(source, ref); err != nil {
		return nil, err
	}
	if err := s.requireCap(ctx, s.DB, p, spaceID, CapView); err != nil {
		return nil, err
	}
	return scanExternal(s.DB.QueryRow(ctx, externalSelect+` WHERE space_id=$1 AND source=$2 AND ref=$3`, spaceID, source, ref))
}

// PutExternal stores content under a reference: a new document at segs, or a
// new version of the document it already stands for (segs is then ignored).
// The same content again changes nothing.
func (s *Service) PutExternal(ctx context.Context, p *Principal, spaceID, source, ref string, segs []string, r io.Reader, opt PutOptions) (*PutResult, error) {
	e, err := s.GetExternal(ctx, p, spaceID, source, ref)
	switch {
	case err == nil:
		f, err := scanFile(s.DB.QueryRow(ctx, fileSelect+` WHERE l.document_id=$1 AND l.deleted_at IS NULL ORDER BY l.created_at LIMIT 1`, e.DocumentID))
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, errf(ErrConflict, "the document of this reference is in the trash")
		}
		if err != nil {
			return nil, err
		}
		if segs, err = SplitPath(f.Path); err != nil {
			return nil, err
		}
	case !errors.Is(err, ErrNotFound) || !s.canView(ctx, p, spaceID):
		return nil, err
	case len(segs) == 0:
		return nil, errf(ErrInvalid, "query parameter path (file name) required")
	}
	opt.CreateParents = true
	res, err := s.PutFile(ctx, p, spaceID, segs, r, opt)
	if err != nil {
		return nil, err
	}
	// a retry after a crash lands here too; a fork moves the reference along
	_, err = s.DB.Exec(ctx, `INSERT INTO external_refs(space_id, source, ref, document_id) VALUES($1,$2,$3,$4)
		ON CONFLICT (space_id, source, ref) DO UPDATE SET document_id = EXCLUDED.document_id`, spaceID, source, ref, res.File.DocumentID)
	return res, err
}

func (s *Service) canView(ctx context.Context, p *Principal, spaceID string) bool {
	return s.requireCap(ctx, s.DB, p, spaceID, CapView) == nil
}

// DeleteExternal removes a reference; the document stays.
func (s *Service) DeleteExternal(ctx context.Context, p *Principal, spaceID, source, ref string) error {
	if err := validateExternal(source, ref); err != nil {
		return err
	}
	if err := s.requireCap(ctx, s.DB, p, spaceID, CapTag); err != nil {
		return err
	}
	tag, err := s.DB.Exec(ctx, `DELETE FROM external_refs WHERE space_id=$1 AND source=$2 AND ref=$3`, spaceID, source, ref)
	if err == nil && tag.RowsAffected() == 0 {
		return errf(ErrNotFound, "no such reference")
	}
	return err
}

// ListExternal lists references page by page. cursor is the next value of a
// previous call; the returned one is empty on the last page.
func (s *Service) ListExternal(ctx context.Context, p *Principal, spaceID, source, cursor string, limit int) ([]ExternalRef, string, error) {
	if err := s.requireCap(ctx, s.DB, p, spaceID, CapView); err != nil {
		return nil, "", err
	}
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	var afterSource, afterRef string
	if cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil {
			return nil, "", errf(ErrInvalid, "invalid cursor")
		}
		var ok bool
		if afterSource, afterRef, ok = strings.Cut(string(raw), "\x00"); !ok {
			return nil, "", errf(ErrInvalid, "invalid cursor")
		}
	}
	rows, err := s.DB.Query(ctx, externalSelect+` WHERE space_id=$1 AND ($2 = '' OR source=$2) AND (source, ref) > ($3, $4)
		ORDER BY source, ref LIMIT $5`, spaceID, source, afterSource, afterRef, limit+1)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	out := []ExternalRef{}
	for rows.Next() {
		e, err := scanExternal(rows)
		if err != nil {
			return nil, "", err
		}
		out = append(out, *e)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > limit {
		out = out[:limit]
		last := out[limit-1]
		next = base64.RawURLEncoding.EncodeToString([]byte(last.Source + "\x00" + last.Ref))
	}
	return out, next, nil
}
