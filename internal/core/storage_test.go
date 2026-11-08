package core_test

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/tildoc/tildoc/internal/core"
)

// blobFiles returns path -> (inode, mtime) of every stored blob on disk.
func blobFiles(t *testing.T, f *fixture) map[string][2]int64 {
	t.Helper()
	out := map[string][2]int64{}
	root := filepath.Join(f.env.DataDir, "store", "blobs")
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		st, err := os.Stat(p)
		if err != nil {
			return err
		}
		out[p] = [2]int64{int64(st.Sys().(*syscall.Stat_t).Ino), st.ModTime().UnixNano()}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func sameFiles(t *testing.T, what string, a, b map[string][2]int64) {
	t.Helper()
	if len(a) != len(b) {
		t.Fatalf("%s: %d blob files before, %d after", what, len(a), len(b))
	}
	for p, v := range a {
		if b[p] != v {
			t.Fatalf("%s: blob file %s was rewritten or removed", what, p)
		}
	}
}

func (f *fixture) hits(t *testing.T, p *core.Principal, q core.SearchQuery) []core.SearchHit {
	t.Helper()
	q.Limit = 50
	r, err := f.svc.Search(f.ctx, p, q)
	if err != nil {
		return nil
	}
	return r.Hits
}

func (f *fixture) index(t *testing.T, r *core.PutResult, text string) {
	t.Helper()
	if err := f.svc.StoreText(f.ctx, r.File.SHA256, "text", text); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) readDoc(p *core.Principal, docID string) (string, error) {
	rd, _, err := f.svc.OpenDocument(f.ctx, p, docID, 0, "")
	if err != nil {
		return "", err
	}
	defer rd.Close()
	b, err := io.ReadAll(rd)
	return string(b), err
}

// Moving between spaces only remaps references: the document keeps its
// identity and history, the stored bytes are never copied or rewritten, and
// access follows the space the document now lives in.
func TestCrossSpaceMoveRemapsReferenceOnly(t *testing.T) {
	f := newFixture(t)
	bobUser := ck(f.svc.UserByName(f.ctx, "bob"))
	team := ck(f.svc.CreateTeam(f.ctx, f.alice, "Steuer"))
	if err := f.svc.SetMember(f.ctx, f.alice, team.ID, bobUser.ID, "member"); err != nil {
		t.Fatal(err)
	}
	ts := ck(f.svc.CreateSpace(f.ctx, f.alice, "steuer", "Steuer", team.ID))
	tdir := ck(f.svc.MkdirAll(f.ctx, f.alice, ts.ID, []string{"2025"}))

	f.put(t, f.alice, f.home.ID, "/Rechnungen/strom.txt", "Stromrechnung Januar")
	r := f.put(t, f.alice, f.home.ID, "/Rechnungen/strom.txt", "Stromrechnung Januar korrigiert")
	f.index(t, r, "Stromrechnung Januar korrigiert")
	if len(f.hits(t, f.bob, core.SearchQuery{Q: "Stromrechnung"})) != 0 {
		t.Fatal("bob must not find alice's private document")
	}

	files := blobFiles(t, f)
	blobs := f.count(t, `SELECT count(*) FROM blobs`)
	jobs := f.count(t, `SELECT count(*) FROM jobs WHERE kind='process_blob'`)

	// sole link: document moves with identity and full history
	m := ck(f.svc.MoveLink(f.ctx, f.alice, r.File.LinkID, tdir.ID, "", false))
	if m.SpaceID != ts.ID || m.DocumentID != r.File.DocumentID || m.LinkID != r.File.LinkID || m.Version != 2 {
		t.Fatalf("moved: %+v", m)
	}
	if d := ck(f.svc.GetDocument(f.ctx, f.alice, m.DocumentID)); len(d.Versions) != 2 || d.SpaceID != ts.ID {
		t.Fatalf("history after move: %+v", d.Versions)
	}
	sameFiles(t, "move into team space", files, blobFiles(t, f))
	if f.count(t, `SELECT count(*) FROM blobs`) != blobs || f.count(t, `SELECT count(*) FROM jobs WHERE kind='process_blob'`) != jobs {
		t.Fatal("a move must neither store nor reprocess content")
	}

	// the team member now sees it, including the full-text index
	if h := f.hits(t, f.bob, core.SearchQuery{Q: "Stromrechnung"}); len(h) != 1 || h[0].LinkID != m.LinkID || h[0].Snippet == "" {
		t.Fatalf("bob search after move: %+v", h)
	}
	if s, err := f.readDoc(f.bob, m.DocumentID); err != nil || s != "Stromrechnung Januar korrigiert" {
		t.Fatalf("bob read: %q %v", s, err)
	}

	// moving it back out of the team space revokes bob's access everywhere
	hroot := ck(f.svc.FolderByPath(f.ctx, f.alice, f.home.ID, nil))
	ck(f.svc.MoveLink(f.ctx, f.alice, m.LinkID, hroot.ID, "", false))
	if len(f.hits(t, f.bob, core.SearchQuery{Q: "Stromrechnung"})) != 0 {
		t.Fatal("bob still finds the document after it left the team space")
	}
	if _, err := f.svc.GetDocument(f.ctx, f.bob, m.DocumentID); err == nil {
		t.Fatal("bob can still open the document")
	}
	if _, err := f.readDoc(f.bob, m.DocumentID); err == nil {
		t.Fatal("bob can still read the content")
	}
	if _, _, err := f.svc.DocumentText(f.ctx, f.bob, m.DocumentID); err == nil {
		t.Fatal("bob can still read the extracted text")
	}
	sameFiles(t, "move back", files, blobFiles(t, f))

	// a document placed in two folders: moving one placement splits it
	// (copy-on-write); both stay readable, the bytes are still stored once
	two := ck(f.svc.AddLink(f.ctx, f.alice, m.DocumentID, hroot.ID, "strom-kopie.txt"))
	mv := ck(f.svc.MoveLink(f.ctx, f.alice, two.LinkID, tdir.ID, "", false))
	if mv.DocumentID == m.DocumentID || mv.SpaceID != ts.ID || mv.Version != 2 {
		t.Fatalf("multi-link move: %+v", mv)
	}
	if orig := ck(f.svc.GetDocument(f.ctx, f.alice, m.DocumentID)); len(orig.Links) != 1 || orig.SpaceID != f.home.ID {
		t.Fatalf("original after split: %+v", orig.Links)
	}
	sameFiles(t, "multi-link move", files, blobFiles(t, f))

	// moving a whole folder tree across spaces: same documents, same bytes
	f.put(t, f.alice, f.home.ID, "/Belege/a/1.txt", "Beleg eins")
	b2 := f.put(t, f.alice, f.home.ID, "/Belege/2.txt", "Beleg zwei")
	files = blobFiles(t, f)
	tree := ck(f.svc.FolderByPath(f.ctx, f.alice, f.home.ID, []string{"Belege"}))
	ck(f.svc.MoveFolder(f.ctx, f.alice, tree.ID, tdir.ID, ""))
	moved := ck(f.svc.FileByPath(f.ctx, f.alice, ts.ID, []string{"2025", "Belege", "2.txt"}))
	if moved.DocumentID != b2.File.DocumentID || moved.LinkID != b2.File.LinkID {
		t.Fatalf("folder move changed identity: %+v", moved)
	}
	sameFiles(t, "folder move", files, blobFiles(t, f))
}

// Content is stored once per server, but every user only ever sees the
// documents, names, text and index entries of spaces they can access.
func TestStoredOnceVisibleOnlyWithAccess(t *testing.T) {
	f := newFixture(t)
	admin := session(t, f.svc, "admin", "supersecret")
	const content = "Steuerbescheid 2025 vertraulich Hamburg"

	ra := f.put(t, f.alice, f.home.ID, "/privat/bescheid.txt", content)
	f.index(t, ra, content)
	only := f.put(t, f.alice, f.home.ID, "/privat/nur-alice.txt", "Kontoauszug Zebrastreifen")
	f.index(t, only, "Kontoauszug Zebrastreifen")

	// bob uploads the identical bytes into his own space
	rb := f.put(t, f.bob, f.bhome.ID, "/meins.txt", content)
	if !rb.NewDocument || rb.Deduplicated || rb.File.DocumentID == ra.File.DocumentID {
		t.Fatalf("bob's upload must look like a fresh document: %+v", rb)
	}
	if n := f.count(t, `SELECT count(*) FROM blobs WHERE sha256=$1`, ra.File.SHA256[:]); n != 1 {
		t.Fatalf("content stored %d times", n)
	}
	if n := f.count(t, `SELECT count(*) FROM blob_texts WHERE sha256=$1`, ra.File.SHA256[:]); n != 1 {
		t.Fatalf("text index stored %d times", n)
	}
	if n := len(blobFiles(t, f)); n != 2 {
		t.Fatalf("expected 2 blob files on disk (shared + alice-only), got %d", n)
	}

	// search: shared text finds only the caller's own placement
	if h := f.hits(t, f.bob, core.SearchQuery{Q: "vertraulich"}); len(h) != 1 || h[0].LinkID != rb.File.LinkID || h[0].SpaceID != f.bhome.ID {
		t.Fatalf("bob shared-text search: %+v", h)
	}
	if h := f.hits(t, f.alice, core.SearchQuery{Q: "vertraulich"}); len(h) != 1 || h[0].LinkID != ra.File.LinkID {
		t.Fatalf("alice shared-text search: %+v", h)
	}
	// text, names and filters of other spaces stay invisible
	for _, q := range []core.SearchQuery{
		{Q: "Zebrastreifen"},
		{Q: "nur-alice"},
		{Q: "bescheid.txt"},
		{Q: "", SpaceID: f.home.ID},
		{Q: "Zebrastreifen", SpaceID: f.home.ID},
		{Q: "", FolderID: ck(f.svc.FolderByPath(f.ctx, f.alice, f.home.ID, nil)).ID},
	} {
		if h := f.hits(t, f.bob, q); len(h) != 0 {
			t.Fatalf("bob sees alice's data via %+v: %+v", q, h)
		}
		if h := f.hits(t, admin, q); len(h) != 0 {
			t.Fatalf("server admin sees alice's data via %+v: %+v", q, h)
		}
	}

	// direct access by id is refused for every read path
	if _, err := f.svc.GetDocument(f.ctx, f.bob, ra.File.DocumentID); err == nil {
		t.Fatal("GetDocument")
	}
	if _, err := f.readDoc(f.bob, ra.File.DocumentID); err == nil {
		t.Fatal("OpenDocument")
	}
	if _, _, err := f.svc.DocumentText(f.ctx, f.bob, ra.File.DocumentID); err == nil {
		t.Fatal("DocumentText")
	}
	if _, err := f.svc.GetFile(f.ctx, f.bob, ra.File.LinkID); err == nil {
		t.Fatal("GetFile")
	}
	if _, _, _, err := f.svc.ListFolder(f.ctx, f.bob, ra.File.FolderID); err == nil {
		t.Fatal("ListFolder")
	}
	if _, err := f.svc.FileByPath(f.ctx, f.bob, f.home.ID, []string{"privat", "bescheid.txt"}); err == nil {
		t.Fatal("FileByPath")
	}
	if _, err := f.svc.AddLink(f.ctx, f.bob, ra.File.DocumentID, ck(f.svc.FolderByPath(f.ctx, f.bob, f.bhome.ID, nil)).ID, "stolen.txt"); err == nil {
		t.Fatal("AddLink to a foreign document")
	}

	// trash is per space as well
	if err := f.svc.DeleteLink(f.ctx, f.alice, only.File.LinkID); err != nil {
		t.Fatal(err)
	}
	for _, sp := range []string{"", f.home.ID} {
		if items, _ := f.svc.ListTrash(f.ctx, f.bob, sp); len(items) != 0 {
			t.Fatalf("bob sees alice's trash (%q): %+v", sp, items)
		}
	}

	// purging alice's copy keeps the shared bytes for bob; purging bob's
	// copy too lets garbage collection remove them
	if err := f.svc.DeleteLink(f.ctx, f.alice, ra.File.LinkID); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.PurgeDocument(f.ctx, f.alice, ra.File.DocumentID); err != nil {
		t.Fatal(err)
	}
	ck(f.svc.GCBlobs(f.ctx, -time.Hour))
	if s, err := f.readDoc(f.bob, rb.File.DocumentID); err != nil || s != content {
		t.Fatalf("bob lost the shared content: %q %v", s, err)
	}
	if err := f.svc.DeleteLink(f.ctx, f.bob, rb.File.LinkID); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.PurgeDocument(f.ctx, f.bob, rb.File.DocumentID); err != nil {
		t.Fatal(err)
	}
	ck(f.svc.GCBlobs(f.ctx, -time.Hour))
	if f.env.Blobs.Exists(ra.File.SHA256) || f.count(t, `SELECT count(*) FROM blob_texts WHERE sha256=$1`, ra.File.SHA256[:]) != 0 {
		t.Fatal("unreferenced content and its index must be removed")
	}
}
