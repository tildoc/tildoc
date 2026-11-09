package core

import (
	"context"
	"errors"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"golang.org/x/text/unicode/norm"

	"github.com/tildoc/tildoc/internal/db"
)

// Tag is a server-wide label. Count is the number of live documents carrying
// it in spaces the caller can read (never a global count).
type Tag struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Color     string    `json:"color"`
	Count     int       `json:"count"`
	CreatedAt time.Time `json:"created_at"`
}

// TagRef is a tag as shown on a document.
type TagRef struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

var colorRe = regexp.MustCompile(`^#[0-9a-f]{6}$`)

// NormalizeTagName validates a tag name and returns its canonical form.
func NormalizeTagName(name string) (string, error) {
	name = strings.Join(strings.Fields(norm.NFC.String(name)), " ")
	if name == "" || len([]rune(name)) > 64 {
		return "", errf(ErrInvalid, "tag names need 1 to 64 characters")
	}
	for _, r := range name {
		if r < 0x20 || (r >= 0x7f && r <= 0x9f) {
			return "", errf(ErrInvalid, "tag name contains control characters")
		}
	}
	return name, nil
}

func normalizeColor(c string) (string, error) {
	c = strings.ToLower(strings.TrimSpace(c))
	if c != "" && !colorRe.MatchString(c) {
		return "", errf(ErrInvalid, "color must look like #1f4e79")
	}
	return c, nil
}

// readableSpaceIDs returns the ids of all spaces the caller can read.
func (s *Service) readableSpaceIDs(ctx context.Context, p *Principal) ([]string, error) {
	spaces, err := s.ListSpaces(ctx, p)
	if err != nil {
		return nil, err
	}
	ids := make([]string, len(spaces))
	for i, sp := range spaces {
		ids[i] = sp.ID
	}
	return ids, nil
}

// ListTags returns the whole vocabulary with per-caller usage counts.
func (s *Service) ListTags(ctx context.Context, p *Principal) ([]Tag, error) {
	if p == nil {
		return nil, ErrUnauthorized
	}
	ids, err := s.readableSpaceIDs(ctx, p)
	if err != nil {
		return nil, err
	}
	rows, err := s.DB.Query(ctx, `
		SELECT t.id::text, t.name, t.color, t.created_at,
		       (SELECT count(*) FROM document_tags dt JOIN documents d ON d.id = dt.document_id
		        WHERE dt.tag_id = t.id AND d.deleted_at IS NULL AND d.space_id = ANY($1::uuid[]))
		FROM tags t ORDER BY lower(t.name)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Tag{}
	for rows.Next() {
		var t Tag
		if err := rows.Scan(&t.ID, &t.Name, &t.Color, &t.CreatedAt, &t.Count); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Service) getTag(ctx context.Context, q db.Querier, id string) (*Tag, error) {
	if !looksLikeUUID(id) {
		return nil, errf(ErrNotFound, "tag not found")
	}
	t := &Tag{}
	err := q.QueryRow(ctx, `SELECT id::text, name, color, created_at FROM tags WHERE id=$1`, id).Scan(&t.ID, &t.Name, &t.Color, &t.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errf(ErrNotFound, "tag not found")
	}
	return t, err
}

// CreateTag adds a tag to the vocabulary (any signed-in user). If a tag with
// the same name (ignoring case) exists, that tag is returned.
func (s *Service) CreateTag(ctx context.Context, p *Principal, name, color string) (*Tag, error) {
	if p == nil || p.UserID == "" {
		return nil, ErrUnauthorized
	}
	if p.ReadOnly {
		return nil, errf(ErrForbidden, "read-only access")
	}
	name, err := NormalizeTagName(name)
	if err != nil {
		return nil, err
	}
	if color, err = normalizeColor(color); err != nil {
		return nil, err
	}
	var id string
	err = s.DB.QueryRow(ctx, `INSERT INTO tags(name, color, created_by) VALUES($1,$2,$3)
		ON CONFLICT (lower(name)) DO UPDATE SET name = tags.name RETURNING id::text`, name, color, p.userRef()).Scan(&id)
	if err != nil {
		return nil, err
	}
	return s.getTag(ctx, s.DB, id)
}

func requireTagAdmin(p *Principal) error {
	if !p.Can(InstTags) {
		return errf(ErrForbidden, "only administrators can change the tag list")
	}
	return nil
}

// UpdateTag renames or recolors a tag (administrators).
func (s *Service) UpdateTag(ctx context.Context, p *Principal, id string, name, color *string) (*Tag, error) {
	if err := requireTagAdmin(p); err != nil {
		return nil, err
	}
	if _, err := s.getTag(ctx, s.DB, id); err != nil {
		return nil, err
	}
	if name != nil {
		n, err := NormalizeTagName(*name)
		if err != nil {
			return nil, err
		}
		_, err = s.DB.Exec(ctx, `UPDATE tags SET name=$2 WHERE id=$1`, id, n)
		if db.IsUniqueViolation(err) {
			return nil, errf(ErrConflict, "a tag named %q exists; merge the tags instead", n)
		}
		if err != nil {
			return nil, err
		}
	}
	if color != nil {
		c, err := normalizeColor(*color)
		if err != nil {
			return nil, err
		}
		if _, err := s.DB.Exec(ctx, `UPDATE tags SET color=$2 WHERE id=$1`, id, c); err != nil {
			return nil, err
		}
	}
	return s.getTag(ctx, s.DB, id)
}

// DeleteTag removes a tag from the vocabulary and from all documents
// (administrators). Collections built on it keep their manual items.
func (s *Service) DeleteTag(ctx context.Context, p *Principal, id string) error {
	if err := requireTagAdmin(p); err != nil {
		return err
	}
	tag, err := s.DB.Exec(ctx, `DELETE FROM tags WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errf(ErrNotFound, "tag not found")
	}
	return nil
}

// MergeTags moves every use of tag from to tag into and deletes from
// (administrators).
func (s *Service) MergeTags(ctx context.Context, p *Principal, from, into string) (*Tag, error) {
	if err := requireTagAdmin(p); err != nil {
		return nil, err
	}
	if from == into {
		return nil, errf(ErrInvalid, "cannot merge a tag into itself")
	}
	err := s.tx(ctx, func(tx pgx.Tx) error {
		for _, id := range []string{from, into} {
			if _, err := s.getTag(ctx, tx, id); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO document_tags(document_id, tag_id, added_by, added_at)
			SELECT document_id, $2, added_by, added_at FROM document_tags WHERE tag_id=$1 ON CONFLICT DO NOTHING`, from, into); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE collections SET tag_id=$2 WHERE tag_id=$1`, from, into); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM tags WHERE id=$1`, from)
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.getTag(ctx, s.DB, into)
}

// writableDocument loads a live document the caller may change.
func (s *Service) writableDocument(ctx context.Context, p *Principal, docID string) (*Document, error) {
	d, err := s.GetDocument(ctx, p, docID)
	if err != nil {
		return nil, err
	}
	if err := s.requirePerm(ctx, s.DB, p, d.SpaceID, PermWrite); err != nil {
		return nil, err
	}
	return d, nil
}

// TagDocument attaches a tag to a document (write permission on its space).
func (s *Service) TagDocument(ctx context.Context, p *Principal, docID, tagID string) error {
	if _, err := s.writableDocument(ctx, p, docID); err != nil {
		return err
	}
	if _, err := s.getTag(ctx, s.DB, tagID); err != nil {
		return err
	}
	_, err := s.DB.Exec(ctx, `INSERT INTO document_tags(document_id, tag_id, added_by) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`,
		docID, tagID, p.userRef())
	return err
}

// UntagDocument removes a tag from a document (write permission on its space).
func (s *Service) UntagDocument(ctx context.Context, p *Principal, docID, tagID string) error {
	if _, err := s.writableDocument(ctx, p, docID); err != nil {
		return err
	}
	_, err := s.DB.Exec(ctx, `DELETE FROM document_tags WHERE document_id=$1 AND tag_id=$2`, docID, tagID)
	return err
}

// SetDocumentTagNames replaces the tags of a document by names, creating
// tags that do not exist yet (S3 object tagging).
func (s *Service) SetDocumentTagNames(ctx context.Context, p *Principal, docID string, names []string) error {
	if _, err := s.writableDocument(ctx, p, docID); err != nil {
		return err
	}
	var ids []string
	seen := map[string]bool{}
	for _, n := range names {
		t, err := s.CreateTag(ctx, p, n, "")
		if err != nil {
			return err
		}
		if !seen[t.ID] {
			seen[t.ID] = true
			ids = append(ids, t.ID)
		}
	}
	return s.tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM document_tags WHERE document_id=$1 AND NOT (tag_id = ANY($2::uuid[]))`, docID, ids); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO document_tags(document_id, tag_id, added_by)
			SELECT $1, unnest($2::uuid[]), $3 ON CONFLICT DO NOTHING`, docID, ids, p.userRef())
		return err
	})
}

// documentTags returns the tags of a document, sorted by name.
func documentTags(ctx context.Context, q db.Querier, docID string) ([]TagRef, error) {
	rows, err := q.Query(ctx, `SELECT t.id::text, t.name, t.color FROM document_tags dt JOIN tags t ON t.id = dt.tag_id
		WHERE dt.document_id=$1 ORDER BY lower(t.name)`, docID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TagRef{}
	for rows.Next() {
		var t TagRef
		if err := rows.Scan(&t.ID, &t.Name, &t.Color); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// tagNames returns the names of tags, sorted.
func tagNames(tags []TagRef) []string {
	out := make([]string, len(tags))
	for i, t := range tags {
		out[i] = t.Name
	}
	sort.Strings(out)
	return out
}
