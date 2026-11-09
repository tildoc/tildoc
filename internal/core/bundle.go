package core

import (
	"archive/tar"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/tildoc/tildoc/internal/blob"
)

// Bundle format: a tar stream with manifest.json first, then
// blobs/<sha256-hex> (each content once) and text/<sha256-hex> (extracted
// text). See docs/bundle-format.md.
const (
	BundleFormat  = "tildoc-bundle"
	BundleVersion = 3
	manifestName  = "manifest.json"
	maxManifest   = 1 << 30
)

// Manifest describes a bundle. Every object carries its UUID.
type Manifest struct {
	Format      string        `json:"format"`
	Version     int           `json:"version"`
	Kind        string        `json:"kind"` // space | collection | server
	CreatedAt   time.Time     `json:"created_at"`
	Instance    string        `json:"instance"`
	Backup      *MBackup      `json:"backup,omitempty"`
	Settings    *MSettings    `json:"settings,omitempty"`
	People      []MPerson     `json:"people"`
	Users       []MUser       `json:"users,omitempty"`
	Identities  []MIdentity   `json:"identities,omitempty"`
	Teams       []MTeam       `json:"teams,omitempty"`
	Roles       []MRole       `json:"roles,omitempty"`
	Grants      []MGrant      `json:"grants,omitempty"`
	ShareLinks  []MShareLink  `json:"share_links,omitempty"`
	APIKeys     []MAPIKey     `json:"api_keys,omitempty"`
	Tags        []MTag        `json:"tags"`
	Spaces      []MSpace      `json:"spaces"`
	Documents   []MDocument   `json:"documents,omitempty"` // collection bundles: documents outside a folder structure
	Collections []MCollection `json:"collections"`
	Imports     []MImport     `json:"imports,omitempty"`
	AccessKeys  []MAccessKey  `json:"access_keys,omitempty"`
	SSHKeys     []MSSHKey     `json:"ssh_keys,omitempty"`
	Blobs       []MBlob       `json:"blobs"`
	Derivatives []MDerivative `json:"derivatives"`
}

// MBackup identifies a server backup.
type MBackup struct {
	ID     string  `json:"id"`
	Mode   string  `json:"mode"`
	BaseID *string `json:"base_id"`
}

// MSettings are the instance settings (server backups).
type MSettings struct {
	InstanceID    string `json:"instance_id"`
	OCRPreference string `json:"ocr_preference"`
	BackupKeep    int    `json:"backup_keep"`
	PublicLinks   bool   `json:"public_links"`
}

// MPerson is a user referenced by a bundle (author of versions etc.).
type MPerson struct {
	ID       string `json:"id"`
	Username string `json:"username"`
}

// MUser is a full user (server backups only).
type MUser struct {
	ID          string    `json:"id"`
	Username    string    `json:"username"`
	DisplayName string    `json:"display_name"`
	Disabled    bool      `json:"disabled"`
	PDFViewer   string    `json:"pdf_viewer"`
	CreatedAt   time.Time `json:"created_at"`
}

// MTeam is a team with members (server backups only).
type MTeam struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	Members   []MMember `json:"members"`
}

// MMember is a team membership.
type MMember struct {
	UserID    string    `json:"user_id"`
	IsManager bool      `json:"is_manager"`
	CreatedAt time.Time `json:"created_at"`
}

// MIdentity is how a user logs in (server backups only).
type MIdentity struct {
	ID           string     `json:"id"`
	UserID       string     `json:"user_id"`
	Provider     string     `json:"provider"`
	Subject      string     `json:"subject"`
	PasswordHash *string    `json:"password_hash"`
	CreatedAt    time.Time  `json:"created_at"`
	LastLoginAt  *time.Time `json:"last_login_at"`
}

// MRole is a role (server backups only).
type MRole struct {
	Slug        string   `json:"slug"`
	Name        string   `json:"name"`
	Scope       string   `json:"scope"`
	BuiltIn     bool     `json:"built_in"`
	Permissions []string `json:"permissions"`
}

// MShareLink is a share link (server backups only; tokens stay hashed).
type MShareLink struct {
	ID           string     `json:"id"`
	TokenHash    []byte     `json:"token_hash"`
	SpaceID      *string    `json:"space_id"`
	DocumentID   *string    `json:"document_id"`
	FolderID     *string    `json:"folder_id"`
	Kind         string     `json:"kind"`
	Preset       string     `json:"preset"`
	Label        string     `json:"label"`
	PasswordHash *string    `json:"password_hash"`
	ExpiresAt    *time.Time `json:"expires_at"`
	CreatedBy    string     `json:"created_by"`
	CreatedAt    time.Time  `json:"created_at"`
	RevokedAt    *time.Time `json:"revoked_at"`
}

// MAPIKey is an API key (server backups only; the secret stays hashed).
type MAPIKey struct {
	ID          string     `json:"id"`
	UserID      string     `json:"user_id"`
	SecretHash  []byte     `json:"secret_hash"`
	Label       string     `json:"label"`
	ExpiresAt   *time.Time `json:"expires_at"`
	CreatedAt   time.Time  `json:"created_at"`
	LastUsedAt  *time.Time `json:"last_used_at"`
	RevokedAt   *time.Time `json:"revoked_at"`
	Permissions []string   `json:"permissions"`
	SpaceIDs    []string   `json:"space_ids"`
}

// MTag is a tag.
type MTag struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Color     string    `json:"color"`
	CreatedBy *string   `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
}

// MSpace is a space with its folder tree and documents.
type MSpace struct {
	ID          string      `json:"id"`
	Slug        string      `json:"slug"`
	Name        string      `json:"name"`
	OwnerUserID *string     `json:"owner_user_id"`
	OwnerTeamID *string     `json:"owner_team_id"`
	CreatedAt   time.Time   `json:"created_at"`
	Folders     []MFolder   `json:"folders"`
	Documents   []MDocument `json:"documents"`
}

// MFolder is a folder; the root has no parent and an empty name.
type MFolder struct {
	ID        string    `json:"id"`
	ParentID  *string   `json:"parent_id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	CreatedBy *string   `json:"created_by"`
}

// MGrant is one matrix row (server backups only).
type MGrant struct {
	ID          string    `json:"id"`
	SpaceID     *string   `json:"space_id"`
	DocumentID  *string   `json:"document_id"`
	UserID      *string   `json:"user_id"`
	TeamID      *string   `json:"team_id"`
	Role        *string   `json:"role"` // slug
	Permissions []string  `json:"permissions"`
	CreatedBy   *string   `json:"created_by"`
	CreatedAt   time.Time `json:"created_at"`
}

// MDocument is a document with its full history and places.
type MDocument struct {
	ID             string     `json:"id"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	CreatedBy      *string    `json:"created_by"`
	ForkedFrom     *string    `json:"forked_from"`
	DeletedAt      *time.Time `json:"deleted_at"`
	CurrentVersion int        `json:"current_version"`
	Versions       []MVersion `json:"versions"`
	Links          []MLink    `json:"links"`
	Tags           []string   `json:"tags"`
}

// MVersion is one version.
type MVersion struct {
	Version   int            `json:"version"`
	SHA256    string         `json:"sha256"`
	CreatedAt time.Time      `json:"created_at"`
	CreatedBy *string        `json:"created_by"`
	Source    string         `json:"source"`
	Meta      map[string]any `json:"meta"`
	// RestoredFrom is set exactly for source "restore": the earlier version
	// whose content was restored.
	RestoredFrom *int `json:"restored_from,omitempty"`
}

// MLink is a place of a document.
type MLink struct {
	ID           string     `json:"id"`
	FolderID     *string    `json:"folder_id"`
	Name         string     `json:"name"`
	OriginalPath string     `json:"original_path"`
	ImportID     *string    `json:"import_id"`
	CreatedAt    time.Time  `json:"created_at"`
	CreatedBy    *string    `json:"created_by"`
	UpdatedAt    time.Time  `json:"updated_at"`
	DeletedAt    *time.Time `json:"deleted_at"`
	DeletedPath  *string    `json:"deleted_path"`
}

// MCollection is a collection.
type MCollection struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	OwnerUserID *string   `json:"owner_user_id"`
	OwnerTeamID *string   `json:"owner_team_id"`
	TagID       *string   `json:"tag_id"`
	CreatedBy   *string   `json:"created_by"`
	CreatedAt   time.Time `json:"created_at"`
	Items       []MItem   `json:"items"`
}

// MItem is a manually added collection member.
type MItem struct {
	DocumentID string    `json:"document_id"`
	AddedBy    *string   `json:"added_by"`
	AddedAt    time.Time `json:"added_at"`
}

// MImport is an upload batch (server backups only).
type MImport struct {
	ID             string    `json:"id"`
	UserID         *string   `json:"user_id"`
	SpaceID        string    `json:"space_id"`
	Protocol       string    `json:"protocol"`
	ClientRoot     string    `json:"client_root"`
	CreatedAt      time.Time `json:"created_at"`
	LastActivityAt time.Time `json:"last_activity_at"`
}

// MAccessKey is an access key; the secret stays encrypted with the master key.
type MAccessKey struct {
	ID         string     `json:"id"`
	UserID     string     `json:"user_id"`
	SecretEnc  []byte     `json:"secret_enc"`
	Label      string     `json:"label"`
	SpaceID    *string    `json:"space_id"`
	ReadOnly   bool       `json:"read_only"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
}

// MSSHKey is a registered SSH key.
type MSSHKey struct {
	ID          string     `json:"id"`
	UserID      string     `json:"user_id"`
	Fingerprint string     `json:"fingerprint"`
	PublicKey   string     `json:"public_key"`
	Label       string     `json:"label"`
	CreatedAt   time.Time  `json:"created_at"`
	LastUsedAt  *time.Time `json:"last_used_at"`
}

// MBlob is a stored content. Included tells whether its bytes are in this
// archive (a differential backup omits those of its base).
type MBlob struct {
	SHA256     string       `json:"sha256"`
	MD5        string       `json:"md5"`
	Size       int64        `json:"size"`
	MIME       string       `json:"mime"`
	CreatedAt  time.Time    `json:"created_at"`
	Included   bool         `json:"included"`
	Processing *MProcessing `json:"processing"`
	TextMethod string       `json:"text_method,omitempty"` // text/<sha> present
}

// MProcessing is the processing state of a blob.
type MProcessing struct {
	Status string `json:"status"`
	Kind   string `json:"kind"`
	Pages  *int   `json:"pages"`
	Error  string `json:"error"`
}

// MDerivative is a derived variant (OCR PDF, image-only PDF).
type MDerivative struct {
	Source    string         `json:"source"`
	Kind      string         `json:"kind"`
	Derived   string         `json:"derived"`
	Input     string         `json:"input"`
	Engine    string         `json:"engine"`
	Meta      map[string]any `json:"meta"`
	CreatedAt time.Time      `json:"created_at"`
}

// ---------------------------------------------------------------- snapshot readers

type manifestBuilder struct {
	tx     pgx.Tx
	ctx    context.Context
	m      *Manifest
	people map[string]bool
	blobs  map[string]bool
	tags   map[string]bool
}

func newManifestBuilder(ctx context.Context, tx pgx.Tx, kind string) (*manifestBuilder, error) {
	m := &Manifest{Format: BundleFormat, Version: BundleVersion, Kind: kind, CreatedAt: time.Now().UTC(),
		People: []MPerson{}, Tags: []MTag{}, Spaces: []MSpace{}, Collections: []MCollection{}, Blobs: []MBlob{}, Derivatives: []MDerivative{}}
	if err := tx.QueryRow(ctx, `SELECT instance_id::text FROM instance_settings`).Scan(&m.Instance); err != nil {
		return nil, err
	}
	return &manifestBuilder{tx: tx, ctx: ctx, m: m, people: map[string]bool{}, blobs: map[string]bool{}, tags: map[string]bool{}}, nil
}

func (b *manifestBuilder) person(id *string) {
	if id != nil {
		b.people[*id] = true
	}
}

// documents loads documents matching where (with $1 as its argument).
func (b *manifestBuilder) documents(where string, arg any, withTrash bool) ([]MDocument, error) {
	rows, err := b.tx.Query(b.ctx, `SELECT id::text, created_at, updated_at, created_by::text, forked_from::text, deleted_at, current_version
		FROM documents d WHERE `+where+` AND (d.deleted_at IS NULL OR $2) ORDER BY d.created_at, d.id`, arg, withTrash)
	if err != nil {
		return nil, err
	}
	var docs []MDocument
	idx := map[string]int{}
	for rows.Next() {
		var d MDocument
		if err := rows.Scan(&d.ID, &d.CreatedAt, &d.UpdatedAt, &d.CreatedBy, &d.ForkedFrom, &d.DeletedAt, &d.CurrentVersion); err != nil {
			rows.Close()
			return nil, err
		}
		b.person(d.CreatedBy)
		d.Versions, d.Links, d.Tags = []MVersion{}, []MLink{}, []string{}
		idx[d.ID] = len(docs)
		docs = append(docs, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil || len(docs) == 0 {
		return docs, err
	}
	ids := make([]string, len(docs))
	for i, d := range docs {
		ids[i] = d.ID
	}
	vrows, err := b.tx.Query(b.ctx, `SELECT document_id::text, version, sha256, created_at, created_by::text, source, meta, restored_from
		FROM document_versions WHERE document_id = ANY($1::uuid[]) ORDER BY document_id, version`, ids)
	if err != nil {
		return nil, err
	}
	for vrows.Next() {
		var docID string
		var v MVersion
		var sha []byte
		if err := vrows.Scan(&docID, &v.Version, &sha, &v.CreatedAt, &v.CreatedBy, &v.Source, &v.Meta, &v.RestoredFrom); err != nil {
			vrows.Close()
			return nil, err
		}
		v.SHA256 = hex.EncodeToString(sha)
		b.blobs[v.SHA256] = true
		b.person(v.CreatedBy)
		d := &docs[idx[docID]]
		d.Versions = append(d.Versions, v)
	}
	vrows.Close()
	lrows, err := b.tx.Query(b.ctx, `SELECT id::text, document_id::text, parent_id::text, name, original_path, import_id::text,
		created_at, created_by::text, updated_at, deleted_at, deleted_path
		FROM entries WHERE document_id = ANY($1::uuid[]) AND kind = 'link' AND (deleted_at IS NULL OR $2) ORDER BY created_at, id`, ids, withTrash)
	if err != nil {
		return nil, err
	}
	for lrows.Next() {
		var l MLink
		var docID string
		if err := lrows.Scan(&l.ID, &docID, &l.FolderID, &l.Name, &l.OriginalPath, &l.ImportID, &l.CreatedAt, &l.CreatedBy,
			&l.UpdatedAt, &l.DeletedAt, &l.DeletedPath); err != nil {
			lrows.Close()
			return nil, err
		}
		b.person(l.CreatedBy)
		d := &docs[idx[docID]]
		d.Links = append(d.Links, l)
	}
	lrows.Close()
	trows, err := b.tx.Query(b.ctx, `SELECT document_id::text, tag_id::text FROM document_tags WHERE document_id = ANY($1::uuid[]) ORDER BY tag_id`, ids)
	if err != nil {
		return nil, err
	}
	for trows.Next() {
		var docID, tag string
		if err := trows.Scan(&docID, &tag); err != nil {
			trows.Close()
			return nil, err
		}
		b.tags[tag] = true
		d := &docs[idx[docID]]
		d.Tags = append(d.Tags, tag)
	}
	trows.Close()
	return docs, trows.Err()
}

// space loads a space with folders and documents.
func (b *manifestBuilder) space(id string, withTrash bool) (*MSpace, error) {
	sp := &MSpace{}
	err := b.tx.QueryRow(b.ctx, `SELECT id::text, slug, name, owner_user_id::text, owner_team_id::text, created_at FROM spaces WHERE id=$1`, id).
		Scan(&sp.ID, &sp.Slug, &sp.Name, &sp.OwnerUserID, &sp.OwnerTeamID, &sp.CreatedAt)
	if err != nil {
		return nil, err
	}
	rows, err := b.tx.Query(b.ctx, `SELECT id::text, parent_id::text, name, created_at, created_by::text
		FROM entries WHERE space_id=$1 AND kind='folder' ORDER BY created_at, id`, id)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var f MFolder
		if err := rows.Scan(&f.ID, &f.ParentID, &f.Name, &f.CreatedAt, &f.CreatedBy); err != nil {
			rows.Close()
			return nil, err
		}
		b.person(f.CreatedBy)
		sp.Folders = append(sp.Folders, f)
	}
	rows.Close()
	if sp.Documents, err = b.documents(`d.space_id = $1`, id, withTrash); err != nil {
		return nil, err
	}
	if sp.Documents == nil {
		sp.Documents = []MDocument{}
	}
	return sp, nil
}

// collections loads collections (by where with $1) keeping only items in docs.
func (b *manifestBuilder) collections(where string, arg any, docs map[string]bool) error {
	rows, err := b.tx.Query(b.ctx, `SELECT id::text, name, owner_user_id::text, owner_team_id::text, tag_id::text, created_by::text, created_at
		FROM collections c WHERE `+where+` ORDER BY created_at, id`, arg)
	if err != nil {
		return err
	}
	var cols []MCollection
	for rows.Next() {
		var c MCollection
		if err := rows.Scan(&c.ID, &c.Name, &c.OwnerUserID, &c.OwnerTeamID, &c.TagID, &c.CreatedBy, &c.CreatedAt); err != nil {
			rows.Close()
			return err
		}
		b.person(c.CreatedBy)
		c.Items = []MItem{}
		cols = append(cols, c)
	}
	rows.Close()
	for i := range cols {
		irows, err := b.tx.Query(b.ctx, `SELECT document_id::text, added_by::text, added_at FROM collection_items
			WHERE collection_id=$1 ORDER BY added_at, document_id`, cols[i].ID)
		if err != nil {
			return err
		}
		for irows.Next() {
			var it MItem
			if err := irows.Scan(&it.DocumentID, &it.AddedBy, &it.AddedAt); err != nil {
				irows.Close()
				return err
			}
			if docs == nil || docs[it.DocumentID] {
				b.person(it.AddedBy)
				cols[i].Items = append(cols[i].Items, it)
			}
		}
		irows.Close()
		if cols[i].TagID != nil {
			b.tags[*cols[i].TagID] = true
		}
		if docs == nil || len(cols[i].Items) > 0 {
			b.m.Collections = append(b.m.Collections, cols[i])
		}
	}
	return nil
}

// finish adds tags, people, derivatives and blob records.
func (b *manifestBuilder) finish(exclude map[string]bool) error {
	tagIDs := keys(b.tags)
	rows, err := b.tx.Query(b.ctx, `SELECT id::text, name, color, created_by::text, created_at FROM tags WHERE id = ANY($1::uuid[]) ORDER BY lower(name)`, tagIDs)
	if err != nil {
		return err
	}
	for rows.Next() {
		var t MTag
		if err := rows.Scan(&t.ID, &t.Name, &t.Color, &t.CreatedBy, &t.CreatedAt); err != nil {
			rows.Close()
			return err
		}
		b.person(t.CreatedBy)
		b.m.Tags = append(b.m.Tags, t)
	}
	rows.Close()

	sources := hexList(keys(b.blobs))
	drows, err := b.tx.Query(b.ctx, `SELECT source_sha256, kind, derived_sha256, input_sha256, engine, meta, created_at
		FROM blob_derivatives WHERE source_sha256 = ANY($1) ORDER BY source_sha256, kind`, sources)
	if err != nil {
		return err
	}
	for drows.Next() {
		var d MDerivative
		var src, der, in []byte
		if err := drows.Scan(&src, &d.Kind, &der, &in, &d.Engine, &d.Meta, &d.CreatedAt); err != nil {
			drows.Close()
			return err
		}
		d.Source, d.Derived, d.Input = hex.EncodeToString(src), hex.EncodeToString(der), hex.EncodeToString(in)
		b.blobs[d.Derived], b.blobs[d.Input] = true, true
		b.m.Derivatives = append(b.m.Derivatives, d)
	}
	drows.Close()

	brows, err := b.tx.Query(b.ctx, `SELECT b.sha256, b.md5, b.size, b.mime, b.created_at,
		bp.status, coalesce(bp.kind,''), bp.pages, coalesce(bp.error,''), coalesce(t.method,'')
		FROM blobs b LEFT JOIN blob_processing bp ON bp.sha256 = b.sha256 LEFT JOIN blob_texts t ON t.sha256 = b.sha256
		WHERE b.sha256 = ANY($1) ORDER BY b.sha256`, hexList(keys(b.blobs)))
	if err != nil {
		return err
	}
	for brows.Next() {
		var mb MBlob
		var sha, md5 []byte
		var status *string
		var pr MProcessing
		if err := brows.Scan(&sha, &md5, &mb.Size, &mb.MIME, &mb.CreatedAt, &status, &pr.Kind, &pr.Pages, &pr.Error, &mb.TextMethod); err != nil {
			brows.Close()
			return err
		}
		mb.SHA256, mb.MD5 = hex.EncodeToString(sha), hex.EncodeToString(md5)
		mb.Included = !exclude[mb.SHA256]
		if status != nil {
			pr.Status = *status
			mb.Processing = &pr
		}
		b.m.Blobs = append(b.m.Blobs, mb)
	}
	brows.Close()

	if len(b.m.Users) == 0 {
		prows, err := b.tx.Query(b.ctx, `SELECT id::text, username FROM users WHERE id = ANY($1::uuid[]) ORDER BY username`, keys(b.people))
		if err != nil {
			return err
		}
		for prows.Next() {
			var pp MPerson
			if err := prows.Scan(&pp.ID, &pp.Username); err != nil {
				prows.Close()
				return err
			}
			b.m.People = append(b.m.People, pp)
		}
		prows.Close()
	}
	return nil
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func hexList(in []string) [][]byte {
	out := make([][]byte, 0, len(in))
	for _, h := range in {
		if b, err := hex.DecodeString(h); err == nil {
			out = append(out, b)
		}
	}
	return out
}

// ---------------------------------------------------------------- exports

// spaceManifest builds the bundle manifest of a space (no trash).
func (s *Service) spaceManifest(ctx context.Context, p *Principal, spaceID string) (*Manifest, error) {
	var m *Manifest
	err := s.snapshot(ctx, func(tx pgx.Tx) error {
		b, err := newManifestBuilder(ctx, tx, "space")
		if err != nil {
			return err
		}
		sp, err := b.space(spaceID, false)
		if err != nil {
			return err
		}
		b.m.Spaces = append(b.m.Spaces, *sp)
		docs := map[string]bool{}
		for _, d := range sp.Documents {
			docs[d.ID] = true
		}
		if err := b.collections(`c.owner_user_id = $1 OR c.owner_team_id IN (SELECT team_id FROM team_members WHERE user_id = $1)`, p.UserID, docs); err != nil {
			return err
		}
		if err := b.finish(nil); err != nil {
			return err
		}
		m = b.m
		return nil
	})
	return m, err
}

// collectionManifest builds the bundle of a collection: its visible
// documents, each with one place named as shown in the collection.
func (s *Service) collectionManifest(ctx context.Context, p *Principal, collectionID string) (*Manifest, error) {
	items, err := s.CollectionItems(ctx, p, collectionID)
	if err != nil {
		return nil, err
	}
	names := CollectionFileNames(items)
	var ids []string
	nameOf := map[string]string{}
	for i, it := range items {
		if s.requireCap(ctx, s.DB, p, it.SpaceID, CapExport) == nil {
			ids = append(ids, it.DocumentID)
			nameOf[it.DocumentID] = names[i]
		}
	}
	var m *Manifest
	err = s.snapshot(ctx, func(tx pgx.Tx) error {
		b, err := newManifestBuilder(ctx, tx, "collection")
		if err != nil {
			return err
		}
		docs, err := b.documents(`d.id = ANY($1::uuid[])`, ids, false)
		if err != nil {
			return err
		}
		in := map[string]bool{}
		for i := range docs {
			d := &docs[i]
			in[d.ID] = true
			if len(d.Links) == 0 {
				continue
			}
			first := d.Links[0]
			first.FolderID, first.Name = nil, nameOf[d.ID]
			d.Links = []MLink{first}
		}
		b.m.Documents = docs
		if err := b.collections(`c.id = $1`, collectionID, in); err != nil {
			return err
		}
		if err := b.finish(nil); err != nil {
			return err
		}
		m = b.m
		return nil
	})
	return m, err
}

// ExportBundle writes a space or collection bundle.
func (s *Service) ExportBundle(ctx context.Context, p *Principal, scope ExportScope, w io.Writer, sealer Sealer) error {
	if _, _, err := s.exportRoot(ctx, p, scope); err != nil {
		return err
	}
	var m *Manifest
	var err error
	switch scope.Kind {
	case "space":
		sp, err2 := s.GetSpace(ctx, p, scope.ID)
		if err2 != nil {
			return err2
		}
		m, err = s.spaceManifest(ctx, p, sp.ID)
	case "collection":
		m, err = s.collectionManifest(ctx, p, scope.ID)
	default:
		return errf(ErrInvalid, "bundles are exported per space or collection")
	}
	if err != nil {
		return err
	}
	_, err = s.writeBundle(ctx, m, w, sealer)
	return err
}

// writeBundle streams a manifest and its included blobs and texts. Returns
// the number of blobs written.
func (s *Service) writeBundle(ctx context.Context, m *Manifest, w io.Writer, sealer Sealer) (int, error) {
	release, err := s.holdBlobs(ctx)
	if err != nil {
		return 0, err
	}
	defer release()
	out, err := seal(w, sealer)
	if err != nil {
		return 0, err
	}
	tw := tar.NewWriter(out)
	data, err := json.Marshal(m)
	if err != nil {
		return 0, err
	}
	if err := tw.WriteHeader(&tar.Header{Name: manifestName, Mode: 0o600, Size: int64(len(data)), ModTime: m.CreatedAt, Format: tar.FormatPAX}); err != nil {
		return 0, err
	}
	if _, err := tw.Write(data); err != nil {
		return 0, err
	}
	n := 0
	for _, mb := range m.Blobs {
		if !mb.Included {
			continue
		}
		var sha blob.SHA256
		raw, _ := hex.DecodeString(mb.SHA256)
		copy(sha[:], raw)
		f, err := s.Blobs.Open(sha)
		if err != nil {
			return n, fmt.Errorf("blob %s: %w", mb.SHA256, err)
		}
		err = tw.WriteHeader(&tar.Header{Name: "blobs/" + mb.SHA256, Mode: 0o600, Size: mb.Size, ModTime: mb.CreatedAt, Format: tar.FormatPAX})
		if err == nil {
			_, err = io.Copy(tw, f)
		}
		f.Close()
		if err != nil {
			return n, err
		}
		n++
		if mb.TextMethod != "" {
			var text string
			if err := s.DB.QueryRow(ctx, `SELECT content FROM blob_texts WHERE sha256=$1`, sha[:]).Scan(&text); err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return n, err
			}
			if err := tw.WriteHeader(&tar.Header{Name: "text/" + mb.SHA256, Mode: 0o600, Size: int64(len(text)), ModTime: mb.CreatedAt, Format: tar.FormatPAX}); err != nil {
				return n, err
			}
			if _, err := io.WriteString(tw, text); err != nil {
				return n, err
			}
		}
	}
	if err := tw.Close(); err != nil {
		return n, err
	}
	return n, out.Close()
}
