package core

import (
	"archive/tar"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/tildoc/tildoc/internal/blob"
)

// ImportOptions selects where a bundle goes. Without TargetFolderID a new
// space is created (owned by the caller or by TeamID).
type ImportOptions struct {
	NewSpaceName   string `json:"space_name,omitempty"`
	NewSpaceSlug   string `json:"space_slug,omitempty"`
	TeamID         string `json:"team_id,omitempty"`
	TargetFolderID string `json:"folder_id,omitempty"`
}

// ImportResult summarizes an import.
type ImportResult struct {
	SpaceID       string `json:"space_id"`
	FolderID      string `json:"folder_id"`
	Documents     int    `json:"documents"`      // new documents
	Merged        int    `json:"merged"`         // joined an existing document with the same content
	Places        int    `json:"places"`         // links created
	BlobsNew      int    `json:"blobs_new"`      // contents stored for the first time
	BlobsExisting int    `json:"blobs_existing"` // contents already on the server
	Collections   int    `json:"collections"`
}

var (
	hex64 = regexp.MustCompile(`^[0-9a-f]{64}$`)
	hex32 = regexp.MustCompile(`^[0-9a-f]{32}$`)
)

// bundleReader reads a bundle: manifest first, then blobs and texts.
type bundleReader struct {
	svc      *Service
	m        *Manifest
	blobs    map[string]*MBlob
	staged   map[string]*blob.Staged // committed blobs of this archive
	texts    map[string]string
	newBlobs int
	// skipUnknown ignores blobs the manifest does not list (a full backup
	// restored with a newer differential holds blobs deleted since)
	skipUnknown bool
}

func readManifest(tr *tar.Reader) (*Manifest, error) {
	hdr, err := tr.Next()
	if err != nil {
		return nil, errf(ErrInvalid, "not a tildoc bundle (%v)", err)
	}
	if hdr.Name != manifestName || hdr.Size <= 0 || hdr.Size > maxManifest {
		return nil, errf(ErrInvalid, "not a tildoc bundle: %s must come first", manifestName)
	}
	dec := json.NewDecoder(io.LimitReader(tr, hdr.Size))
	dec.DisallowUnknownFields()
	m := &Manifest{}
	if err := dec.Decode(m); err != nil {
		return nil, errf(ErrInvalid, "invalid bundle manifest: %v", err)
	}
	if m.Format != BundleFormat {
		return nil, errf(ErrInvalid, "not a tildoc bundle")
	}
	if m.Version != BundleVersion {
		return nil, errf(ErrInvalid, "bundle format version %d is not supported (this server reads version %d)", m.Version, BundleVersion)
	}
	return m, nil
}

// readEntries consumes the remaining archive entries, verifying and storing
// every blob through the normal blob store (content addressed: bytes that
// exist already are not stored again).
func (br *bundleReader) readEntries(tr *tar.Reader, progress func(float64)) error {
	var total, done int64
	for _, b := range br.blobs {
		if b.Included {
			total += b.Size
		}
	}
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return errf(ErrInvalid, "damaged bundle: %v", err)
		}
		switch {
		case strings.HasPrefix(hdr.Name, "blobs/"):
			sha := strings.TrimPrefix(hdr.Name, "blobs/")
			mb := br.blobs[sha]
			if mb == nil && br.skipUnknown && hex64.MatchString(sha) {
				continue
			}
			if mb == nil || !mb.Included || br.staged[sha] != nil || hdr.Size != mb.Size {
				return errf(ErrInvalid, "unexpected bundle entry %q", hdr.Name)
			}
			st, err := br.svc.Blobs.Stage(io.LimitReader(tr, hdr.Size), br.svc.Cfg.MaxUploadBytes)
			if err != nil {
				return err
			}
			if st.SHA256.Hex() != sha {
				br.svc.Blobs.Discard(st)
				return errf(ErrInvalid, "content of %s does not match its SHA-256", sha)
			}
			isNew, err := br.svc.Blobs.Commit(st)
			if err != nil {
				br.svc.Blobs.Discard(st)
				return err
			}
			if isNew {
				br.newBlobs++
			}
			br.staged[sha] = st
			done += hdr.Size
			if progress != nil && total > 0 {
				progress(float64(done) / float64(total))
			}
		case strings.HasPrefix(hdr.Name, "text/"):
			sha := strings.TrimPrefix(hdr.Name, "text/")
			mb := br.blobs[sha]
			if mb == nil && br.skipUnknown && hex64.MatchString(sha) {
				continue
			}
			if mb == nil || mb.TextMethod == "" || hdr.Size > MaxTextBytes {
				return errf(ErrInvalid, "unexpected bundle entry %q", hdr.Name)
			}
			data, err := io.ReadAll(io.LimitReader(tr, hdr.Size))
			if err != nil {
				return err
			}
			br.texts[sha] = string(data)
		default:
			return errf(ErrInvalid, "unexpected bundle entry %q", hdr.Name)
		}
	}
	return nil
}

func newBundleReader(s *Service, m *Manifest) *bundleReader {
	br := &bundleReader{svc: s, m: m, blobs: map[string]*MBlob{}, staged: map[string]*blob.Staged{}, texts: map[string]string{}}
	for i := range m.Blobs {
		br.blobs[m.Blobs[i].SHA256] = &m.Blobs[i]
	}
	return br
}

func shaOf(h string) blob.SHA256 {
	var s blob.SHA256
	raw, _ := hex.DecodeString(h)
	copy(s[:], raw)
	return s
}

// ---------------------------------------------------------------- validation

var versionSources = map[string]bool{"web": true, "api": true, "webdav": true, "sftp": true, "s3": true, "rename": true, "copy": true, "restore": true}

// validateManifest checks a bundle as untrusted input. server allows the
// server-only parts (users, teams, keys).
func validateManifest(m *Manifest, server bool) error {
	bad := func(f string, a ...any) error { return errf(ErrInvalid, "invalid bundle: "+f, a...) }
	switch {
	case server && m.Kind != "server":
		return bad("this is a %s bundle, not a server backup", m.Kind)
	case !server && m.Kind != "space" && m.Kind != "collection":
		return bad("only space and collection bundles can be imported here (got %q)", m.Kind)
	case !server && (len(m.Users) > 0 || len(m.Teams) > 0 || len(m.AccessKeys) > 0 || len(m.SSHKeys) > 0 || len(m.Imports) > 0 || m.Backup != nil || m.Settings != nil):
		return bad("contains server data")
	case m.Kind == "space" && len(m.Spaces) != 1:
		return bad("a space bundle holds exactly one space")
	case m.Kind == "collection" && (len(m.Spaces) != 0 || len(m.Collections) != 1):
		return bad("a collection bundle holds one collection and no spaces")
	}
	blobs := map[string]bool{}
	for _, b := range m.Blobs {
		if !hex64.MatchString(b.SHA256) || !hex32.MatchString(b.MD5) || b.Size < 0 || blobs[b.SHA256] {
			return bad("blob %q", b.SHA256)
		}
		if b.TextMethod != "" && b.TextMethod != "text" && b.TextMethod != "pdftotext" && b.TextMethod != "ocr" {
			return bad("text method %q", b.TextMethod)
		}
		blobs[b.SHA256] = true
	}
	for _, d := range m.Derivatives {
		if !blobs[d.Source] || !blobs[d.Derived] || !blobs[d.Input] || (d.Kind != "searchable_pdf" && d.Kind != "image_only_pdf") {
			return bad("derivative %s/%s", d.Source, d.Kind)
		}
	}
	tags := map[string]bool{}
	for _, t := range m.Tags {
		if !looksLikeUUID(t.ID) {
			return bad("tag id")
		}
		if _, err := NormalizeTagName(t.Name); err != nil {
			return bad("tag %q", t.Name)
		}
		if _, err := normalizeColor(t.Color); err != nil {
			return bad("tag color %q", t.Color)
		}
		tags[t.ID] = true
	}
	docs := map[string]bool{}
	checkDocs := func(list []MDocument, folders map[string]bool) error {
		for _, d := range list {
			if !looksLikeUUID(d.ID) || docs[d.ID] || len(d.Versions) == 0 || d.CurrentVersion < 1 || d.CurrentVersion > len(d.Versions) {
				return bad("document %q", d.ID)
			}
			docs[d.ID] = true
			for i, v := range d.Versions {
				if v.Version != i+1 || !blobs[v.SHA256] || !versionSources[v.Source] ||
					(v.Source == "restore") != (v.RestoredFrom != nil) || (v.RestoredFrom != nil && (*v.RestoredFrom < 1 || *v.RestoredFrom >= v.Version)) {
					return bad("version %d of document %s", v.Version, d.ID)
				}
			}
			for _, l := range d.Links {
				if !looksLikeUUID(l.ID) || len(l.OriginalPath) > 4096 {
					return bad("place %q", l.ID)
				}
				if _, err := NormalizeName(l.Name); err != nil {
					return bad("file name %q", l.Name)
				}
				if l.FolderID != nil && !folders[*l.FolderID] && l.DeletedAt == nil {
					return bad("place %s points outside the space", l.ID)
				}
			}
			for _, t := range d.Tags {
				if !tags[t] {
					return bad("unknown tag %q", t)
				}
			}
		}
		return nil
	}
	for _, sp := range m.Spaces {
		if !looksLikeUUID(sp.ID) {
			return bad("space id")
		}
		folders := map[string]bool{}
		roots := 0
		for _, f := range sp.Folders {
			if !looksLikeUUID(f.ID) || folders[f.ID] {
				return bad("folder %q", f.ID)
			}
			folders[f.ID] = true
			if f.ParentID == nil {
				roots++
				if f.Name != "" {
					return bad("root folder with a name")
				}
			} else if _, err := NormalizeName(f.Name); err != nil {
				return bad("folder name %q", f.Name)
			}
		}
		if roots != 1 {
			return bad("space %s needs exactly one root folder", sp.ID)
		}
		parent := map[string]string{}
		for _, f := range sp.Folders {
			if f.ParentID != nil {
				if !folders[*f.ParentID] {
					return bad("folder %s has an unknown parent", f.ID)
				}
				parent[f.ID] = *f.ParentID
			}
		}
		for id := range parent { // no cycles
			seen := map[string]bool{}
			for cur := id; parent[cur] != ""; cur = parent[cur] {
				if seen[cur] {
					return bad("folder cycle at %s", id)
				}
				seen[cur] = true
			}
		}
		if err := checkDocs(sp.Documents, folders); err != nil {
			return err
		}
	}
	if err := checkDocs(m.Documents, map[string]bool{}); err != nil {
		return err
	}
	for _, c := range m.Collections {
		if !looksLikeUUID(c.ID) {
			return bad("collection id")
		}
		if _, err := normalizeCollectionName(c.Name); err != nil {
			return bad("collection name %q", c.Name)
		}
		if c.TagID != nil && !tags[*c.TagID] {
			return bad("collection tag")
		}
		for _, it := range c.Items {
			if !docs[it.DocumentID] && !server {
				return bad("collection item %s", it.DocumentID)
			}
		}
	}
	return nil
}

// ---------------------------------------------------------------- import

// ImportBundle imports a space or collection bundle. Every content goes
// through the normal blob ingest (verified SHA-256, stored once), every
// document through the same rules as uploads: content already present in
// the target space joins that document (histories merge). The metadata is
// applied in one transaction.
func (s *Service) ImportBundle(ctx context.Context, p *Principal, r io.Reader, opener Opener, opt ImportOptions, progress func(float64)) (*ImportResult, error) {
	if err := requireSession(p); err != nil {
		return nil, err
	}
	// blob files are written while reading and recorded at the end: keep
	// garbage collection away for the whole import
	release, err := s.holdBlobs(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	in, err := open(r, opener)
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(in)
	m, err := readManifest(tr)
	if err != nil {
		return nil, err
	}
	if err := validateManifest(m, false); err != nil {
		return nil, err
	}
	// check the target before reading gigabytes
	if opt.TargetFolderID != "" {
		f, err := s.GetFolder(ctx, p, opt.TargetFolderID)
		if err != nil {
			return nil, err
		}
		if err := s.requireCap(ctx, s.DB, p, f.SpaceID, CapWrite); err != nil {
			return nil, err
		}
	}
	br := newBundleReader(s, m)
	if err := br.readEntries(tr, progress); err != nil {
		return nil, err
	}
	for sha, mb := range br.blobs {
		if mb.Included && br.staged[sha] == nil {
			return nil, errf(ErrInvalid, "bundle is incomplete: content %s missing", sha)
		}
		if !mb.Included && !s.Blobs.Exists(shaOf(sha)) {
			return nil, errf(ErrInvalid, "bundle is incomplete: content %s is neither included nor on this server", sha)
		}
	}
	res := &ImportResult{BlobsNew: br.newBlobs, BlobsExisting: len(br.blobs) - br.newBlobs}
	err = s.tx(ctx, func(tx pgx.Tx) error {
		return s.applyImport(ctx, tx, p, br, opt, res)
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

func (s *Service) applyImport(ctx context.Context, tx pgx.Tx, p *Principal, br *bundleReader, opt ImportOptions, res *ImportResult) error {
	m := br.m
	if err := s.importBlobsTx(ctx, tx, br); err != nil {
		return err
	}
	// target
	var spaceID, root string
	if opt.TargetFolderID != "" {
		sp, err := lockFolder(ctx, tx, opt.TargetFolderID)
		if err != nil {
			return err
		}
		spaceID, root = sp, opt.TargetFolderID
	} else {
		name := strings.TrimSpace(opt.NewSpaceName)
		if name == "" && len(m.Spaces) == 1 {
			name = m.Spaces[0].Name
		}
		if name == "" && len(m.Collections) == 1 {
			name = m.Collections[0].Name
		}
		if name == "" {
			name = "Imported"
		}
		owner, team := p.UserID, ""
		if opt.TeamID != "" {
			role, err := s.teamRole(ctx, tx, p, opt.TeamID)
			if err != nil {
				return err
			}
			if roleRank(role) < roleRank("admin") {
				return errf(ErrForbidden, "only team owners and admins can create team spaces")
			}
			owner, team = "", opt.TeamID
		}
		slug := strings.ToLower(strings.TrimSpace(opt.NewSpaceSlug))
		if slug == "" {
			slug = s.uniqueSlug(ctx, tx, name)
		} else if err := ValidateSlug(slug); err != nil {
			return err
		}
		id, err := s.createSpaceTx(ctx, tx, slug, name, owner, team)
		if err != nil {
			return err
		}
		spaceID = id
		if err := tx.QueryRow(ctx, `SELECT id::text FROM entries WHERE space_id=$1 AND parent_id IS NULL AND kind='folder'`, id).Scan(&root); err != nil {
			return err
		}
	}
	res.SpaceID, res.FolderID = spaceID, root

	people := s.mapPeople(ctx, tx, m.People)
	tagMap, err := s.importTagsTx(ctx, tx, p, m.Tags)
	if err != nil {
		return err
	}

	// folders: bundle root -> target folder; same-named folders are reused
	folderMap := map[string]string{}
	var docs []MDocument
	if len(m.Spaces) == 1 {
		sp := m.Spaces[0]
		docs = sp.Documents
		byParent := map[string][]MFolder{}
		for _, f := range sp.Folders {
			if f.ParentID == nil {
				folderMap[f.ID] = root
			} else {
				byParent[*f.ParentID] = append(byParent[*f.ParentID], f)
			}
		}
		queue := []string{}
		for id := range folderMap {
			queue = append(queue, id)
		}
		for len(queue) > 0 {
			src := queue[0]
			queue = queue[1:]
			for _, f := range byParent[src] {
				id, err := s.importFolderTx(ctx, tx, p, folderMap[src], f.Name)
				if err != nil {
					return err
				}
				folderMap[f.ID] = id
				queue = append(queue, f.ID)
			}
		}
	} else {
		// collection bundle: documents go into a folder named after the collection
		id, err := s.importFolderTx(ctx, tx, p, root, m.Collections[0].Name)
		if err != nil {
			return err
		}
		res.FolderID = id
		root = id
		docs = m.Documents
	}

	docMap := map[string]string{}
	for _, d := range docs {
		if d.DeletedAt != nil {
			continue
		}
		var live []MLink
		for _, l := range d.Links {
			if l.DeletedAt == nil {
				live = append(live, l)
			}
		}
		if len(live) == 0 {
			continue
		}
		final, merged, err := s.importDocumentTx(ctx, tx, p, spaceID, d, people, tagMap)
		if err != nil {
			return err
		}
		docMap[d.ID] = final
		if merged {
			res.Merged++
		} else {
			res.Documents++
		}
		for _, l := range live {
			folder := root
			if l.FolderID != nil {
				folder = folderMap[*l.FolderID]
			}
			created, err := s.importLinkTx(ctx, tx, p, final, folder, l, people)
			if err != nil {
				return err
			}
			if created {
				res.Places++
			}
		}
	}
	for _, d := range docs {
		if d.ForkedFrom != nil && docMap[d.ID] != "" && docMap[*d.ForkedFrom] != "" {
			if _, err := tx.Exec(ctx, `UPDATE documents SET forked_from=$2 WHERE id=$1 AND forked_from IS NULL`, docMap[d.ID], docMap[*d.ForkedFrom]); err != nil {
				return err
			}
		}
	}
	for _, c := range m.Collections {
		n, err := s.importCollectionTx(ctx, tx, p, c, docMap, tagMap)
		if err != nil {
			return err
		}
		res.Collections += n
	}
	return nil
}

// importBlobsTx records blob rows and takes over processing results, text
// and derived variants that the target does not have yet.
func (s *Service) importBlobsTx(ctx context.Context, tx pgx.Tx, br *bundleReader) error {
	shas := make([]string, 0, len(br.blobs))
	for sha := range br.blobs {
		shas = append(shas, sha)
	}
	sort.Strings(shas)
	for _, sha := range shas {
		mb := br.blobs[sha]
		h := shaOf(sha)
		md5, _ := hex.DecodeString(mb.MD5)
		if st := br.staged[sha]; st != nil {
			md5 = st.MD5[:]
		}
		if _, err := tx.Exec(ctx, `INSERT INTO blobs(sha256, md5, size, mime) VALUES($1,$2,$3,$4)
			ON CONFLICT (sha256) DO NOTHING`, h[:], md5, mb.Size, mb.MIME); err != nil {
			return err
		}
	}
	for _, sha := range shas {
		mb := br.blobs[sha]
		h := shaOf(sha)
		if text, ok := br.texts[sha]; ok {
			if err := storeTextTx(ctx, tx, h, mb.TextMethod, text, false); err != nil {
				return err
			}
		}
		if pr := mb.Processing; pr != nil && (pr.Status == StatusDone || pr.Status == StatusUnsupported || pr.Status == StatusOCRPending) {
			if _, err := tx.Exec(ctx, `INSERT INTO blob_processing(sha256, status, kind, pages, error) VALUES($1,$2,$3,$4,$5)
				ON CONFLICT (sha256) DO NOTHING`, h[:], pr.Status, pr.Kind, pr.Pages, pr.Error); err != nil {
				return err
			}
		}
	}
	for _, d := range br.m.Derivatives {
		src, der, in := shaOf(d.Source), shaOf(d.Derived), shaOf(d.Input)
		if d.Meta == nil {
			d.Meta = map[string]any{}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO blob_derivatives(source_sha256, kind, derived_sha256, input_sha256, engine, meta, created_at)
			VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT (source_sha256, kind) DO NOTHING`, src[:], d.Kind, der[:], in[:], d.Engine, d.Meta, d.CreatedAt); err != nil {
			return err
		}
	}
	// everything else is processed like a fresh upload
	for _, sha := range shas {
		if err := ensureProcessingTx(ctx, tx, shaOf(sha)); err != nil {
			return err
		}
	}
	return nil
}

// storeTextTx stores extracted text inside a transaction (savepoint for the
// tsvector size fallback). keep=false leaves an existing text untouched.
func storeTextTx(ctx context.Context, tx pgx.Tx, sha blob.SHA256, method, text string, replace bool) error {
	text = sanitizeText(text)
	conflict := `DO NOTHING`
	if replace {
		conflict = `DO UPDATE SET method=EXCLUDED.method, content=EXCLUDED.content, tsv=EXCLUDED.tsv, created_at=now()`
	}
	for _, vec := range []string{`to_tsvector('simple', $3)`, `strip(to_tsvector('simple', $3))`} {
		sp, err := tx.Begin(ctx)
		if err != nil {
			return err
		}
		_, err = sp.Exec(ctx, `INSERT INTO blob_texts(sha256, method, content, tsv) VALUES($1, $2, $3, `+vec+`)
			ON CONFLICT (sha256) `+conflict, sha[:], method, text)
		if err == nil {
			return sp.Commit(ctx)
		}
		sp.Rollback(ctx)
		if !strings.Contains(err.Error(), "54000") {
			return err
		}
	}
	return errors.New("text could not be indexed")
}

// mapPeople maps bundle user ids to local users with the same username.
func (s *Service) mapPeople(ctx context.Context, tx pgx.Tx, people []MPerson) map[string]*string {
	out := map[string]*string{}
	for _, pp := range people {
		var id string
		if err := tx.QueryRow(ctx, `SELECT id::text FROM users WHERE username=$1`, strings.ToLower(pp.Username)).Scan(&id); err == nil {
			out[pp.ID] = &id
		}
	}
	return out
}

func mapped(people map[string]*string, id *string) *string {
	if id == nil {
		return nil
	}
	return people[*id]
}

func (s *Service) importTagsTx(ctx context.Context, tx pgx.Tx, p *Principal, tags []MTag) (map[string]string, error) {
	out := map[string]string{}
	for _, t := range tags {
		name, _ := NormalizeTagName(t.Name)
		color, _ := normalizeColor(t.Color)
		var id string
		if err := tx.QueryRow(ctx, `INSERT INTO tags(name, color, created_by) VALUES($1,$2,$3)
			ON CONFLICT (lower(name)) DO UPDATE SET name = tags.name RETURNING id::text`, name, color, p.userRef()).Scan(&id); err != nil {
			return nil, err
		}
		out[t.ID] = id
	}
	return out, nil
}

// importFolderTx returns a folder named name below parent, reusing an
// existing one; a file with that name makes it "name (imported)".
func (s *Service) importFolderTx(ctx context.Context, tx pgx.Tx, p *Principal, parent, name string) (string, error) {
	fold, link, err := child(ctx, tx, parent, name)
	if err != nil {
		return "", err
	}
	if fold != "" {
		return fold, nil
	}
	if link != "" {
		if name, err = uniqueName(ctx, tx, parent, name, "imported"); err != nil {
			return "", err
		}
	}
	return s.mkdirTx(ctx, tx, p, parent, name)
}

// importDocumentTx creates a document with its history; if the space holds
// the same current content already, the history is merged into that
// document instead. Returns the document the places belong to.
func (s *Service) importDocumentTx(ctx context.Context, tx pgx.Tx, p *Principal, spaceID string, d MDocument,
	people map[string]*string, tagMap map[string]string) (string, bool, error) {
	cur := shaOf(d.Versions[d.CurrentVersion-1].SHA256)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, spaceID+cur.Hex()); err != nil {
		return "", false, err
	}
	existing, err := findDocBySHA(ctx, tx, spaceID, cur)
	if err != nil {
		return "", false, err
	}
	var id string
	if err := tx.QueryRow(ctx, `INSERT INTO documents(space_id, current_version, created_at, updated_at, created_by)
		VALUES($1,$2,$3,$4,$5) RETURNING id::text`, spaceID, d.CurrentVersion, d.CreatedAt, d.UpdatedAt, mapped(people, d.CreatedBy)).Scan(&id); err != nil {
		return "", false, err
	}
	for _, v := range d.Versions {
		h := shaOf(v.SHA256)
		if v.Meta == nil {
			v.Meta = map[string]any{}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO document_versions(document_id, version, sha256, created_at, created_by, source, meta, restored_from)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, id, v.Version, h[:], v.CreatedAt, mapped(people, v.CreatedBy), v.Source, v.Meta, v.RestoredFrom); err != nil {
			return "", false, err
		}
	}
	for _, t := range d.Tags {
		if _, err := tx.Exec(ctx, `INSERT INTO document_tags(document_id, tag_id, added_by) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`,
			id, tagMap[t], p.userRef()); err != nil {
			return "", false, err
		}
	}
	if existing == "" {
		return id, false, nil
	}
	if err := mergeHistoryTx(ctx, tx, existing, id); err != nil {
		return "", false, err
	}
	if err := copyDocRelationsTx(ctx, tx, id, existing); err != nil {
		return "", false, err
	}
	_, err = tx.Exec(ctx, `DELETE FROM documents WHERE id=$1`, id)
	return existing, true, err
}

// importLinkTx places a document in a folder. A place that already shows
// this document under that name is kept (re-imports are idempotent); other
// name clashes get "(imported)".
func (s *Service) importLinkTx(ctx context.Context, tx pgx.Tx, p *Principal, docID, folder string, l MLink, people map[string]*string) (bool, error) {
	fold, other, err := child(ctx, tx, folder, l.Name)
	if err != nil {
		return false, err
	}
	name := l.Name
	if other != "" {
		var otherDoc string
		if err := tx.QueryRow(ctx, `SELECT document_id::text FROM entries WHERE id=$1`, other).Scan(&otherDoc); err != nil {
			return false, err
		}
		if otherDoc == docID {
			return false, nil
		}
	}
	if fold != "" || other != "" {
		if name, err = uniqueName(ctx, tx, folder, l.Name, "imported"); err != nil {
			return false, err
		}
	}
	orig := l.OriginalPath
	if orig == "" {
		orig = "/" + name
	}
	_, err = tx.Exec(ctx, `INSERT INTO entries(space_id, kind, document_id, parent_id, name, original_path, created_at, created_by)
		SELECT space_id, 'link', id, $2, $3, $4, $5, $6 FROM documents WHERE id=$1`,
		docID, folder, name, orig, l.CreatedAt, mapped(people, l.CreatedBy))
	return err == nil, err
}

// importCollectionTx recreates a collection as a personal collection of the
// importer (a taken name gets "(imported)").
func (s *Service) importCollectionTx(ctx context.Context, tx pgx.Tx, p *Principal, c MCollection, docMap, tagMap map[string]string) (int, error) {
	var items []string
	for _, it := range c.Items {
		if id := docMap[it.DocumentID]; id != "" {
			items = append(items, id)
		}
	}
	var tag *string
	if c.TagID != nil {
		if id := tagMap[*c.TagID]; id != "" {
			tag = &id
		}
	}
	if len(items) == 0 && tag == nil {
		return 0, nil
	}
	name := c.Name
	for i := 1; ; i++ {
		var taken bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM collections WHERE owner_user_id=$1 AND name=$2)`, p.UserID, name).Scan(&taken); err != nil {
			return 0, err
		}
		if !taken {
			break
		}
		if i == 1 {
			name = c.Name + " (imported)"
		} else {
			name = fmt.Sprintf("%s (imported %d)", c.Name, i)
		}
	}
	var id string
	if err := tx.QueryRow(ctx, `INSERT INTO collections(name, owner_user_id, tag_id, created_by) VALUES($1,$2,$3,$4) RETURNING id::text`,
		name, p.UserID, tag, p.userRef()).Scan(&id); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO collection_items(collection_id, document_id, added_by)
		SELECT $1, unnest($2::uuid[]), $3 ON CONFLICT DO NOTHING`, id, items, p.userRef()); err != nil {
		return 0, err
	}
	return 1, nil
}
