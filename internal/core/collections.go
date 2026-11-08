package core

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/tildoc/tildoc/internal/db"
)

// Collection groups documents across spaces. It never grants access: its
// items are filtered by the spaces the caller can read.
type Collection struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	OwnerUserID *string   `json:"owner_user_id"`
	OwnerTeamID *string   `json:"owner_team_id"`
	TeamName    string    `json:"team_name,omitempty"`
	TagID       *string   `json:"tag_id"`
	TagName     string    `json:"tag_name,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	Count       int       `json:"count"` // visible live documents
	CanEdit     bool      `json:"can_edit"`
	CanManage   bool      `json:"can_manage"`
}

// CollectionItem is one visible document of a collection, shown through one
// of its places.
type CollectionItem struct {
	FileInfo
	Manual  bool       `json:"manual"`   // added by hand (else only through the tag)
	AddedAt *time.Time `json:"added_at"` // manual items only
}

// CollectionRef names a collection a document belongs to.
type CollectionRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

const (
	collNone = iota
	collView
	collEdit
	collManage
)

const collectionSelect = `
	SELECT c.id::text, c.name, c.owner_user_id::text, c.owner_team_id::text, coalesce(tm.name, ''),
	       c.tag_id::text, coalesce(t.name, ''), c.created_at,
	       CASE WHEN c.owner_user_id = $1 THEN 'owner' ELSE coalesce(m.role, '') END
	FROM collections c
	LEFT JOIN teams tm ON tm.id = c.owner_team_id
	LEFT JOIN team_members m ON m.team_id = c.owner_team_id AND m.user_id = $1
	LEFT JOIN tags t ON t.id = c.tag_id`

func collectionLevel(role string) int {
	switch role {
	case "owner", "admin":
		return collManage
	case "member":
		return collEdit
	case "viewer":
		return collView
	}
	return collNone
}

func scanCollection(row pgx.Row, p *Principal) (*Collection, int, error) {
	c := &Collection{}
	var role string
	if err := row.Scan(&c.ID, &c.Name, &c.OwnerUserID, &c.OwnerTeamID, &c.TeamName, &c.TagID, &c.TagName, &c.CreatedAt, &role); err != nil {
		return nil, 0, err
	}
	lvl := collectionLevel(role)
	if p.ReadOnly && lvl > collView {
		lvl = collView
	}
	c.CanEdit, c.CanManage = lvl >= collEdit, lvl >= collManage
	return c, lvl, nil
}

func (s *Service) collection(ctx context.Context, q db.Querier, p *Principal, id string, need int) (*Collection, error) {
	if p == nil || p.UserID == "" {
		return nil, ErrUnauthorized
	}
	if !looksLikeUUID(id) {
		return nil, errf(ErrNotFound, "collection not found")
	}
	c, lvl, err := scanCollection(q.QueryRow(ctx, collectionSelect+` WHERE c.id = $2`, p.UserID, id), p)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && lvl == collNone) {
		return nil, errf(ErrNotFound, "collection not found")
	}
	if err != nil {
		return nil, err
	}
	if lvl < need {
		return nil, errf(ErrForbidden, "insufficient permission for this collection")
	}
	return c, nil
}

// visibleCount counts live documents of a collection in readable spaces.
func (s *Service) visibleCount(ctx context.Context, id string, spaces []string) (int, error) {
	var n int
	err := s.DB.QueryRow(ctx, `SELECT count(*) FROM documents d
		WHERE d.id IN (SELECT collection_documents($1)) AND d.deleted_at IS NULL AND d.space_id = ANY($2::uuid[])`,
		id, spaces).Scan(&n)
	return n, err
}

// ListCollections lists the caller's personal collections and those of
// their teams.
func (s *Service) ListCollections(ctx context.Context, p *Principal) ([]Collection, error) {
	if p == nil || p.UserID == "" {
		return nil, ErrUnauthorized
	}
	spaces, err := s.readableSpaceIDs(ctx, p)
	if err != nil {
		return nil, err
	}
	rows, err := s.DB.Query(ctx, collectionSelect+` WHERE c.owner_user_id = $1 OR m.user_id IS NOT NULL
		ORDER BY c.owner_team_id IS NOT NULL, lower(c.name)`, p.UserID)
	if err != nil {
		return nil, err
	}
	var out []Collection
	for rows.Next() {
		c, lvl, err := scanCollection(rows, p)
		if err != nil {
			rows.Close()
			return nil, err
		}
		if lvl > collNone {
			out = append(out, *c)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if out[i].Count, err = s.visibleCount(ctx, out[i].ID, spaces); err != nil {
			return nil, err
		}
	}
	if out == nil {
		out = []Collection{}
	}
	return out, nil
}

// GetCollection returns a collection the caller can view.
func (s *Service) GetCollection(ctx context.Context, p *Principal, id string) (*Collection, error) {
	c, err := s.collection(ctx, s.DB, p, id, collView)
	if err != nil {
		return nil, err
	}
	spaces, err := s.readableSpaceIDs(ctx, p)
	if err != nil {
		return nil, err
	}
	c.Count, err = s.visibleCount(ctx, c.ID, spaces)
	return c, err
}

func normalizeCollectionName(name string) (string, error) {
	name, err := NormalizeName(strings.TrimSpace(name))
	if err != nil {
		return "", errf(ErrInvalid, "invalid collection name: %v", err)
	}
	return name, nil
}

func (s *Service) checkTagRef(ctx context.Context, tagID *string) (*string, error) {
	if tagID == nil || *tagID == "" {
		return nil, nil
	}
	if _, err := s.getTag(ctx, s.DB, *tagID); err != nil {
		return nil, err
	}
	return tagID, nil
}

// CreateCollection creates a personal collection (teamID empty) or a team
// collection (team owner or admin).
func (s *Service) CreateCollection(ctx context.Context, p *Principal, name, teamID string, tagID *string) (*Collection, error) {
	if err := requireSession(p); err != nil {
		return nil, err
	}
	name, err := normalizeCollectionName(name)
	if err != nil {
		return nil, err
	}
	if tagID, err = s.checkTagRef(ctx, tagID); err != nil {
		return nil, err
	}
	var owner, team *string
	if teamID == "" {
		owner = &p.UserID
	} else {
		role, err := s.teamRole(ctx, s.DB, p, teamID)
		if err != nil {
			return nil, err
		}
		if collectionLevel(role) < collManage {
			return nil, errf(ErrForbidden, "only team owners and admins can create team collections")
		}
		team = &teamID
	}
	var id string
	err = s.DB.QueryRow(ctx, `INSERT INTO collections(name, owner_user_id, owner_team_id, tag_id, created_by)
		VALUES($1,$2,$3,$4,$5) RETURNING id::text`, name, owner, team, tagID, p.userRef()).Scan(&id)
	if db.IsUniqueViolation(err) {
		return nil, errf(ErrConflict, "a collection named %q exists", name)
	}
	if err != nil {
		return nil, err
	}
	return s.GetCollection(ctx, p, id)
}

// CollectionUpdate changes a collection; nil fields stay unchanged, an empty
// TagID removes the tag rule.
type CollectionUpdate struct {
	Name  *string `json:"name"`
	TagID *string `json:"tag_id"`
}

// UpdateCollection renames a collection or changes its tag rule.
func (s *Service) UpdateCollection(ctx context.Context, p *Principal, id string, upd CollectionUpdate) (*Collection, error) {
	if _, err := s.collection(ctx, s.DB, p, id, collManage); err != nil {
		return nil, err
	}
	if upd.Name != nil {
		n, err := normalizeCollectionName(*upd.Name)
		if err != nil {
			return nil, err
		}
		_, err = s.DB.Exec(ctx, `UPDATE collections SET name=$2 WHERE id=$1`, id, n)
		if db.IsUniqueViolation(err) {
			return nil, errf(ErrConflict, "a collection named %q exists", n)
		}
		if err != nil {
			return nil, err
		}
	}
	if upd.TagID != nil {
		tag, err := s.checkTagRef(ctx, upd.TagID)
		if err != nil {
			return nil, err
		}
		if _, err := s.DB.Exec(ctx, `UPDATE collections SET tag_id=$2 WHERE id=$1`, id, tag); err != nil {
			return nil, err
		}
	}
	return s.GetCollection(ctx, p, id)
}

// DeleteCollection deletes a collection (its documents stay untouched).
func (s *Service) DeleteCollection(ctx context.Context, p *Principal, id string) error {
	if _, err := s.collection(ctx, s.DB, p, id, collManage); err != nil {
		return err
	}
	_, err := s.DB.Exec(ctx, `DELETE FROM collections WHERE id=$1`, id)
	return err
}

// AddToCollection adds documents the caller can read.
func (s *Service) AddToCollection(ctx context.Context, p *Principal, id string, docIDs []string) error {
	if _, err := s.collection(ctx, s.DB, p, id, collEdit); err != nil {
		return err
	}
	for _, d := range docIDs {
		if _, err := s.GetDocument(ctx, p, d); err != nil {
			return err
		}
	}
	_, err := s.DB.Exec(ctx, `INSERT INTO collection_items(collection_id, document_id, added_by)
		SELECT $1, unnest($2::uuid[]), $3 ON CONFLICT DO NOTHING`, id, docIDs, p.userRef())
	return err
}

// RemoveFromCollection removes manually added documents. Documents that are
// in the collection through its tag stay until the tag is removed from them.
func (s *Service) RemoveFromCollection(ctx context.Context, p *Principal, id string, docIDs []string) error {
	if _, err := s.collection(ctx, s.DB, p, id, collEdit); err != nil {
		return err
	}
	_, err := s.DB.Exec(ctx, `DELETE FROM collection_items WHERE collection_id=$1 AND document_id = ANY($2::uuid[])`, id, docIDs)
	return err
}

// CollectionItems lists the visible live documents of a collection, each
// through its oldest live place.
func (s *Service) CollectionItems(ctx context.Context, p *Principal, id string) ([]CollectionItem, error) {
	if _, err := s.collection(ctx, s.DB, p, id, collView); err != nil {
		return nil, err
	}
	spaces, err := s.readableSpaceIDs(ctx, p)
	if err != nil {
		return nil, err
	}
	rows, err := s.DB.Query(ctx, fileSelect+`
		WHERE l.id IN (
			SELECT DISTINCT ON (e.document_id) e.id FROM entries e
			WHERE e.document_id IN (SELECT collection_documents($1)) AND e.kind = 'link' AND e.deleted_at IS NULL
			  AND e.space_id = ANY($2::uuid[])
			ORDER BY e.document_id, e.created_at, e.id)
		ORDER BY lower(l.name), l.id`, id, spaces)
	files, err := scanFiles(rows, err)
	if err != nil {
		return nil, err
	}
	manual := map[string]time.Time{}
	mrows, err := s.DB.Query(ctx, `SELECT document_id::text, added_at FROM collection_items WHERE collection_id=$1`, id)
	if err != nil {
		return nil, err
	}
	for mrows.Next() {
		var d string
		var at time.Time
		if err := mrows.Scan(&d, &at); err != nil {
			mrows.Close()
			return nil, err
		}
		manual[d] = at
	}
	mrows.Close()
	out := make([]CollectionItem, len(files))
	for i, f := range files {
		out[i] = CollectionItem{FileInfo: f}
		if at, ok := manual[f.DocumentID]; ok {
			out[i].Manual, out[i].AddedAt = true, &at
		}
	}
	return out, mrows.Err()
}

// documentCollections lists the caller's collections containing a document.
func (s *Service) documentCollections(ctx context.Context, p *Principal, docID string) ([]CollectionRef, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT c.id::text, c.name FROM collections c
		LEFT JOIN team_members m ON m.team_id = c.owner_team_id AND m.user_id = $1
		WHERE (c.owner_user_id = $1 OR m.user_id IS NOT NULL) AND $2::uuid IN (SELECT collection_documents(c.id))
		ORDER BY lower(c.name)`, p.UserID, docID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CollectionRef{}
	for rows.Next() {
		var c CollectionRef
		if err := rows.Scan(&c.ID, &c.Name); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
