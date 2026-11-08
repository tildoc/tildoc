package core

import (
	"context"
	"encoding/hex"
	"strings"
)

// SearchQuery filters and sorts files.
type SearchQuery struct {
	Q        string // websearch syntax over extracted text + names
	SpaceID  string // optional
	FolderID string // optional: restrict to subtree
	MIME     string // optional prefix, e.g. "application/pdf" or "text/"
	Status   string // optional processing status
	Sort     string // relevance | name | modified | created | size
	Desc     bool
	Limit    int
	Offset   int
}

// SearchHit is a matching file.
type SearchHit struct {
	FileInfo
	Rank    float32 `json:"rank"`
	Snippet string  `json:"snippet,omitempty"`
	// Page is the 1-based page of the first highlighted term (PDF text keeps
	// pdftotext form feeds as page breaks); nil without a content match.
	Page *int `json:"page,omitempty"`
}

// SearchResult is a page of hits.
type SearchResult struct {
	Total int         `json:"total"`
	Hits  []SearchHit `json:"hits"`
}

func likeEscape(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

// Search runs a full-text + name search over readable spaces.
func (s *Service) Search(ctx context.Context, p *Principal, q SearchQuery) (*SearchResult, error) {
	spaces, err := s.ListSpaces(ctx, p)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, sp := range spaces {
		if q.SpaceID == "" || sp.ID == q.SpaceID {
			ids = append(ids, sp.ID)
		}
	}
	if q.SpaceID != "" && len(ids) == 0 {
		return nil, errf(ErrNotFound, "space not found")
	}
	if q.Limit <= 0 || q.Limit > 500 {
		q.Limit = 100
	}
	if q.Offset < 0 {
		q.Offset = 0
	}
	q.Q = strings.TrimSpace(q.Q)
	order := map[string]string{
		"name":     `l.name COLLATE "C"`,
		"modified": `d.updated_at`,
		"created":  `l.created_at`,
		"size":     `b.size`,
	}[q.Sort]
	dir := " ASC"
	if q.Desc {
		dir = " DESC"
	}
	switch {
	case order != "":
		order += dir
	case q.Q != "":
		order = "rank DESC"
	default:
		order = "d.updated_at DESC"
	}
	rows, err := s.DB.Query(ctx, `
		WITH RECURSIVE sub(id) AS (
			SELECT id FROM folders WHERE $4 <> '' AND id = nullif($4,'')::uuid
			UNION ALL SELECT f.id FROM folders f JOIN sub ON f.parent_id = sub.id
		), hits AS (
			SELECT l.id::text AS link_id, l.document_id::text AS doc_id, d.space_id::text AS space_id, l.folder_id::text AS folder_id,
			       l.name, folder_path(l.folder_id) || '/' || l.name AS path,
			       b.size, b.sha256, b.md5, b.mime, d.current_version, coalesce(bp.status,'pending') AS status,
			       (SELECT count(*) FROM links x WHERE x.document_id = d.id AND x.deleted_at IS NULL) AS link_count,
			       l.created_at, d.updated_at, l.original_path, l.import_id::text AS import_id,
			       (CASE WHEN $2 = '' THEN 0 ELSE
			         coalesce(ts_rank(t.tsv, websearch_to_tsquery('simple', $2)), 0)
			         + CASE WHEN l.name ILIKE '%' || $3 || '%' ESCAPE '\' THEN 1 ELSE 0 END END)::real AS rank,
			       count(*) OVER () AS total
			FROM links l
			JOIN documents d ON d.id = l.document_id
			JOIN blobs b ON b.sha256 = d.current_sha256
			LEFT JOIN blob_processing bp ON bp.sha256 = d.current_sha256
			LEFT JOIN blob_texts t ON t.sha256 = d.current_sha256
			WHERE l.deleted_at IS NULL AND d.deleted_at IS NULL AND d.space_id = ANY($1::uuid[])
			  AND ($2 = '' OR t.tsv @@ websearch_to_tsquery('simple', $2)
			       OR to_tsvector('simple', regexp_replace(l.name, '[._-]+', ' ', 'g')) @@ websearch_to_tsquery('simple', $2)
			       OR l.name ILIKE '%' || $3 || '%' ESCAPE '\')
			  AND ($4 = '' OR l.folder_id IN (SELECT id FROM sub))
			  AND ($5 = '' OR b.mime LIKE $5 || '%')
			  AND ($6 = '' OR coalesce(bp.status,'pending') = $6)
			ORDER BY `+order+`, l.id
			LIMIT $7 OFFSET $8
		)
		SELECT h.*, CASE WHEN $2 = '' THEN '' ELSE coalesce((
			SELECT ts_headline('simple', left(bt.content, 200000), websearch_to_tsquery('simple', $2),
			       'MaxFragments=2,MinWords=6,MaxWords=20,StartSel=<<,StopSel=>>,FragmentDelimiter= … ')
			FROM blob_texts bt WHERE bt.sha256 = h.sha256 AND bt.tsv @@ websearch_to_tsquery('simple', $2)), '') END
		FROM hits h`,
		ids, q.Q, likeEscape(q.Q), q.FolderID, q.MIME, q.Status, q.Limit, q.Offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	res := &SearchResult{Hits: []SearchHit{}}
	for rows.Next() {
		var h SearchHit
		var sha, md5 []byte
		var folder *string
		var total int
		if err := rows.Scan(&h.LinkID, &h.DocumentID, &h.SpaceID, &folder, &h.Name, &h.Path, &h.Size, &sha, &md5, &h.MIME,
			&h.Version, &h.Status, &h.LinkCount, &h.CreatedAt, &h.ModifiedAt, &h.OriginalPath, &h.ImportID,
			&h.Rank, &total, &h.Snippet); err != nil {
			return nil, err
		}
		if folder != nil {
			h.FolderID = *folder
		}
		copy(h.SHA256[:], sha)
		h.MD5 = hex.EncodeToString(md5)
		h.MIME = displayMIME(h.Name, h.MIME)
		res.Total = total
		res.Hits = append(res.Hits, h)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	return res, s.fillHitPages(ctx, res.Hits)
}

// firstHighlight returns the first term ts_headline marked in a snippet.
func firstHighlight(snippet string) string {
	i := strings.Index(snippet, "<<")
	if i < 0 {
		return ""
	}
	rest := snippet[i+2:]
	j := strings.Index(rest, ">>")
	if j <= 0 {
		return ""
	}
	return rest[:j]
}

// fillHitPages sets SearchHit.Page by locating the first highlighted term in
// the extracted text and counting the form feeds before it.
func (s *Service) fillHitPages(ctx context.Context, hits []SearchHit) error {
	var idx []int
	var shas [][]byte
	var terms []string
	for i := range hits {
		if t := firstHighlight(hits[i].Snippet); t != "" {
			idx = append(idx, i)
			shas = append(shas, hits[i].SHA256[:])
			terms = append(terms, t)
		}
	}
	if len(idx) == 0 {
		return nil
	}
	rows, err := s.DB.Query(ctx, `
		SELECT x.ord, 1 + length(p.pre) - length(replace(p.pre, E'\f', ''))
		FROM unnest($1::bytea[], $2::text[]) WITH ORDINALITY AS x(sha, term, ord)
		JOIN blob_texts bt ON bt.sha256 = x.sha
		CROSS JOIN LATERAL (SELECT strpos(bt.content, x.term) AS pos) f
		CROSS JOIN LATERAL (SELECT left(bt.content, f.pos - 1) AS pre) p
		WHERE f.pos > 0`, shas, terms)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var ord int64
		var page int
		if err := rows.Scan(&ord, &page); err != nil {
			return err
		}
		pg := page
		hits[idx[ord-1]].Page = &pg
	}
	return rows.Err()
}

// Stats summarizes the installation (admin dashboard).
type Stats struct {
	Users      int            `json:"users"`
	Spaces     int            `json:"spaces"`
	Documents  int            `json:"documents"`
	Trashed    int            `json:"trashed"`
	Blobs      int            `json:"blobs"`
	BlobBytes  int64          `json:"blob_bytes"`
	Processing map[string]int `json:"processing"`
	Jobs       map[string]int `json:"jobs"`
	FailedJobs []JobInfo      `json:"failed_jobs"`
}

// JobInfo describes a job.
type JobInfo struct {
	ID        int64  `json:"id"`
	Kind      string `json:"kind"`
	Status    string `json:"status"`
	Attempts  int    `json:"attempts"`
	LastError string `json:"last_error"`
}

// AdminStats returns installation statistics (admin only).
func (s *Service) AdminStats(ctx context.Context, p *Principal) (*Stats, error) {
	if p == nil || !p.IsAdmin {
		return nil, errf(ErrForbidden, "admin only")
	}
	st := &Stats{Processing: map[string]int{}, Jobs: map[string]int{}, FailedJobs: []JobInfo{}}
	err := s.DB.QueryRow(ctx, `SELECT (SELECT count(*) FROM users), (SELECT count(*) FROM spaces),
		(SELECT count(*) FROM documents WHERE deleted_at IS NULL), (SELECT count(*) FROM documents WHERE deleted_at IS NOT NULL),
		(SELECT count(*) FROM blobs), (SELECT coalesce(sum(size),0) FROM blobs)`).
		Scan(&st.Users, &st.Spaces, &st.Documents, &st.Trashed, &st.Blobs, &st.BlobBytes)
	if err != nil {
		return nil, err
	}
	for _, q := range []struct {
		sql string
		m   map[string]int
	}{
		{`SELECT status, count(*) FROM blob_processing GROUP BY status`, st.Processing},
		{`SELECT status, count(*) FROM jobs GROUP BY status`, st.Jobs},
	} {
		rows, err := s.DB.Query(ctx, q.sql)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var k string
			var n int
			rows.Scan(&k, &n)
			q.m[k] = n
		}
		rows.Close()
	}
	rows, err := s.DB.Query(ctx, `SELECT id, kind, status, attempts, last_error FROM jobs WHERE status='failed' ORDER BY updated_at DESC LIMIT 50`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var j JobInfo
		rows.Scan(&j.ID, &j.Kind, &j.Status, &j.Attempts, &j.LastError)
		st.FailedJobs = append(st.FailedJobs, j)
	}
	return st, rows.Err()
}
