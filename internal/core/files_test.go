package core_test

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/tildoc/tildoc/internal/core"
	"github.com/tildoc/tildoc/internal/testutil"
)

type fixture struct {
	env   *testutil.Env
	svc   *core.Service
	ctx   context.Context
	alice *core.Principal
	bob   *core.Principal
	home  core.Space // alice's personal space
	bhome core.Space // bob's personal space
}

func newFixture(t *testing.T) *fixture {
	env := testutil.New(t)
	ctx := context.Background()
	svc := env.Svc
	ck(svc.SetupAdmin(ctx, "admin", "supersecret", ""))
	ap := session(t, svc, "admin", "supersecret")
	ck(svc.CreateUser(ctx, ap, "alice", "alicepass", "", false))
	ck(svc.CreateUser(ctx, ap, "bob", "bobpass12", "", false))
	f := &fixture{env: env, svc: svc, ctx: ctx, alice: session(t, svc, "alice", "alicepass"), bob: session(t, svc, "bob", "bobpass12")}
	f.home = ck(svc.ListSpaces(ctx, f.alice))[0]
	f.bhome = ck(svc.ListSpaces(ctx, f.bob))[0]
	return f
}

func (f *fixture) put(t *testing.T, p *core.Principal, space, path, content string) *core.PutResult {
	t.Helper()
	segs := ck(core.SplitPath(path))
	res, err := f.svc.PutFile(f.ctx, p, space, segs, strings.NewReader(content), core.PutOptions{Protocol: "api", CreateParents: true})
	if err != nil {
		t.Fatalf("put %s: %v", path, err)
	}
	return res
}

func (f *fixture) read(t *testing.T, p *core.Principal, space, path string) string {
	t.Helper()
	fi := ck(f.svc.FileByPath(f.ctx, p, space, ck(core.SplitPath(path))))
	r := ck(f.svc.OpenFile(f.ctx, p, fi))
	defer r.Close()
	b, _ := io.ReadAll(r)
	return string(b)
}

func (f *fixture) count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := f.env.Pool.QueryRow(f.ctx, sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestPutDedupVersionFork(t *testing.T) {
	f := newFixture(t)
	sp := f.home.ID

	r1 := f.put(t, f.alice, sp, "/Taxes/2025/invoice.pdf", "%PDF-1.4 invoice")
	if !r1.NewDocument || !r1.NewLink || !r1.NewBlob || r1.File.Version != 1 {
		t.Fatalf("first put: %+v", r1)
	}
	if r1.File.Path != "/Taxes/2025/invoice.pdf" || r1.File.OriginalPath != "/Taxes/2025/invoice.pdf" || r1.File.ImportID == nil {
		t.Fatalf("paths: %+v", r1.File)
	}
	if r1.File.MIME != "application/pdf" {
		t.Fatalf("mime %q", r1.File.MIME)
	}
	if n := f.count(t, `SELECT count(*) FROM jobs WHERE kind='process_blob'`); n != 1 {
		t.Fatalf("jobs: %d", n)
	}

	// same content elsewhere in the same space -> new link, same document
	r2 := f.put(t, f.alice, sp, "/Inbox/scan.pdf", "%PDF-1.4 invoice")
	if !r2.Deduplicated || r2.NewDocument || r2.NewBlob || r2.File.DocumentID != r1.File.DocumentID || r2.File.LinkCount != 2 {
		t.Fatalf("dedup: %+v", r2)
	}
	if *r2.File.ImportID != *r1.File.ImportID {
		t.Fatal("expected same import batch")
	}
	if n := f.count(t, `SELECT count(*) FROM jobs WHERE kind='process_blob'`); n != 1 {
		t.Fatalf("dedup must not re-process: %d jobs", n)
	}

	// same content in another user's space -> new document, blob reused
	r3 := f.put(t, f.bob, f.bhome.ID, "/invoice.pdf", "%PDF-1.4 invoice")
	if !r3.NewDocument || r3.NewBlob || r3.File.DocumentID == r1.File.DocumentID {
		t.Fatalf("cross space: %+v", r3)
	}
	if n := f.count(t, `SELECT count(*) FROM blobs`); n != 1 {
		t.Fatalf("blobs: %d", n)
	}

	// writing through one link of a multi-linked document forks it
	r4 := f.put(t, f.alice, sp, "/Inbox/scan.pdf", "%PDF-1.4 invoice v2")
	if !r4.Forked || !r4.NewVersion || r4.File.DocumentID == r1.File.DocumentID || r4.File.Version != 2 {
		t.Fatalf("fork: %+v", r4)
	}
	if got := f.read(t, f.alice, sp, "/Taxes/2025/invoice.pdf"); got != "%PDF-1.4 invoice" {
		t.Fatalf("other link changed: %q", got)
	}
	doc := ck(f.svc.GetDocument(f.ctx, f.alice, r4.File.DocumentID))
	if len(doc.Versions) != 2 || doc.ForkedFrom == nil || *doc.ForkedFrom != r1.File.DocumentID {
		t.Fatalf("fork history: %+v", doc)
	}

	// single-linked document: plain new version
	r5 := f.put(t, f.alice, sp, "/Inbox/scan.pdf", "%PDF-1.4 invoice v3")
	if r5.Forked || !r5.NewVersion || r5.File.DocumentID != r4.File.DocumentID || r5.File.Version != 3 {
		t.Fatalf("version: %+v", r5)
	}
	// identical content -> no-op
	r6 := f.put(t, f.alice, sp, "/Inbox/scan.pdf", "%PDF-1.4 invoice v3")
	if r6.NewVersion || r6.File.Version != 3 {
		t.Fatalf("no-op: %+v", r6)
	}
	// restore version 1 creates version 4
	d := ck(f.svc.RestoreVersion(f.ctx, f.alice, r5.File.DocumentID, 1))
	if d.Version != 4 || f.read(t, f.alice, sp, "/Inbox/scan.pdf") != "%PDF-1.4 invoice" {
		t.Fatalf("restore version: %d", d.Version)
	}
	// document-level version is visible in all links (no fork)
	ck(f.svc.AddLink(f.ctx, f.alice, d.ID, ck(f.svc.FolderByPath(f.ctx, f.alice, sp, nil)).ID, "alias.pdf"))
	d = ck(f.svc.AddDocumentVersion(f.ctx, f.alice, d.ID, strings.NewReader("shared v5"), "web"))
	if f.read(t, f.alice, sp, "/alias.pdf") != "shared v5" || f.read(t, f.alice, sp, "/Inbox/scan.pdf") != "shared v5" {
		t.Fatal("document version must apply to all links")
	}
}

func TestNameConflictsAndValidation(t *testing.T) {
	f := newFixture(t)
	sp := f.home.ID
	f.put(t, f.alice, sp, "/a/b.txt", "x")
	_, err := f.svc.PutFile(f.ctx, f.alice, sp, []string{"a"}, strings.NewReader("y"), core.PutOptions{})
	wantKind(t, err, core.ErrIsFolder)
	root := ck(f.svc.FolderByPath(f.ctx, f.alice, sp, nil))
	a := ck(f.svc.FolderByPath(f.ctx, f.alice, sp, []string{"a"}))
	_, err = f.svc.Mkdir(f.ctx, f.alice, a.ID, "b.txt")
	wantKind(t, err, core.ErrConflict)
	_, err = f.svc.Mkdir(f.ctx, f.alice, root.ID, "a")
	wantKind(t, err, core.ErrConflict)
	_, err = f.svc.Mkdir(f.ctx, f.alice, root.ID, "..")
	wantKind(t, err, core.ErrInvalid)
	_, err = f.svc.PutFile(f.ctx, f.alice, sp, []string{"missing", "x.txt"}, strings.NewReader("y"), core.PutOptions{})
	wantKind(t, err, core.ErrNotFound)
	// NFC normalization: decomposed "ü" equals composed
	f.put(t, f.alice, sp, "/Grüße.txt", "1")
	r := f.put(t, f.alice, sp, "/Grüße.txt", "2")
	if r.NewLink {
		t.Fatal("NFC variants must address the same file")
	}
}

func TestMoveRenameProvenance(t *testing.T) {
	f := newFixture(t)
	sp := f.home.ID
	r := f.put(t, f.alice, sp, "/import/contract.pdf", "contract")
	root := ck(f.svc.FolderByPath(f.ctx, f.alice, sp, nil))
	archive := ck(f.svc.Mkdir(f.ctx, f.alice, root.ID, "Archive"))

	// a deliberate rename in the UI keeps provenance
	m := ck(f.svc.MoveLink(f.ctx, f.alice, r.File.LinkID, "", "contract-v1.pdf", false))
	if m.OriginalPath != "/import/contract.pdf" {
		t.Fatalf("UI rename changed provenance: %q", m.OriginalPath)
	}
	// a fresh rename by a protocol client (temp file + rename save) updates provenance
	_, sec := ck2(f.svc.CreateAccessKey(f.ctx, f.alice, "dav", "", false))
	dav := ck(f.svc.AuthenticateAppPassword(f.ctx, "alice", sec))
	pr := f.put(t, dav, sp, "/import/scan.pdf.part", "scan")
	pm := ck(f.svc.MoveLink(f.ctx, dav, pr.File.LinkID, "", "scan.pdf", false))
	if pm.OriginalPath != "/import/scan.pdf" {
		t.Fatalf("protocol rename provenance: %q", pm.OriginalPath)
	}
	m = ck(f.svc.MoveLink(f.ctx, f.alice, r.File.LinkID, "", "contract-final.pdf", false))
	// age the link: later moves keep provenance
	f.env.Pool.Exec(f.ctx, `UPDATE links SET created_at = now() - interval '1 hour'`)
	m = ck(f.svc.MoveLink(f.ctx, f.alice, r.File.LinkID, archive.ID, "", false))
	if m.Path != "/Archive/contract-final.pdf" || m.OriginalPath != "/import/contract.pdf" {
		t.Fatalf("move: %+v", m)
	}
	// restore original structure
	res := ck(f.svc.RestoreOriginal(f.ctx, f.alice, []string{m.LinkID}))
	if res[0].Path != "/import/contract.pdf" {
		t.Fatalf("restore original: %q", res[0].Path)
	}

	// move conflicts
	f.put(t, f.alice, sp, "/Archive/x.pdf", "x")
	_, err := f.svc.MoveLink(f.ctx, f.alice, m.LinkID, archive.ID, "x.pdf", false)
	wantKind(t, err, core.ErrConflict)

	// folder move + cycle prevention
	sub := ck(f.svc.Mkdir(f.ctx, f.alice, archive.ID, "Sub"))
	_, err = f.svc.MoveFolder(f.ctx, f.alice, archive.ID, sub.ID, "")
	wantKind(t, err, core.ErrInvalid)
	mf := ck(f.svc.MoveFolder(f.ctx, f.alice, archive.ID, ck(f.svc.FolderByPath(f.ctx, f.alice, sp, []string{"import"})).ID, "Old"))
	if mf.Path != "/import/Old" {
		t.Fatalf("folder move: %q", mf.Path)
	}
	if f.read(t, f.alice, sp, "/import/Old/x.pdf") != "x" {
		t.Fatal("content after folder move")
	}
}

func TestRenameOverKeepsHistory(t *testing.T) {
	f := newFixture(t)
	sp := f.home.ID
	orig := f.put(t, f.alice, sp, "/notes.txt", "v1")
	f.env.Pool.Exec(f.ctx, `UPDATE documents SET created_at = now() - interval '1 hour'`)
	// KIO style save: write notes.txt.part, rename over notes.txt
	part := f.put(t, f.alice, sp, "/notes.txt.part", "v2")
	res := ck(f.svc.MoveLink(f.ctx, f.alice, part.File.LinkID, "", "notes.txt", true))
	if res.DocumentID != orig.File.DocumentID || res.Version != 2 {
		t.Fatalf("rename-over: %+v", res)
	}
	if f.read(t, f.alice, sp, "/notes.txt") != "v2" {
		t.Fatal("content")
	}
	// transient .part document vanished completely (not in trash)
	if n := f.count(t, `SELECT count(*) FROM documents`); n != 1 {
		t.Fatalf("documents: %d", n)
	}
	if tr := ck(f.svc.ListTrash(f.ctx, f.alice, "")); len(tr) != 0 {
		t.Fatalf("trash: %+v", tr)
	}
}

func TestDeleteTrashRestorePurge(t *testing.T) {
	f := newFixture(t)
	sp := f.home.ID
	a := f.put(t, f.alice, sp, "/docs/2024/a.txt", "AAA")
	f.put(t, f.alice, sp, "/docs/b.txt", "AAA") // second link, same document
	c := f.put(t, f.alice, sp, "/docs/2024/c.txt", "CCC")

	// deleting one of two links keeps the document alive
	if err := f.svc.DeleteLink(f.ctx, f.alice, a.File.LinkID); err != nil {
		t.Fatal(err)
	}
	if tr := ck(f.svc.ListTrash(f.ctx, f.alice, "")); len(tr) != 0 {
		t.Fatalf("trash should be empty: %+v", tr)
	}
	// delete folder recursively
	docs := ck(f.svc.FolderByPath(f.ctx, f.alice, sp, []string{"docs"}))
	if err := f.svc.DeleteFolder(f.ctx, f.alice, docs.ID); err != nil {
		t.Fatal(err)
	}
	tr := ck(f.svc.ListTrash(f.ctx, f.alice, ""))
	if len(tr) != 2 {
		t.Fatalf("trash: %+v", tr)
	}
	_, err := f.svc.FolderByPath(f.ctx, f.alice, sp, []string{"docs"})
	wantKind(t, err, core.ErrNotFound)

	// restore recreates the folder structure
	d := ck(f.svc.RestoreDocument(f.ctx, f.alice, c.File.DocumentID))
	if len(d.Links) != 1 || d.Links[0].Path != "/docs/2024/c.txt" {
		t.Fatalf("restore: %+v", d.Links)
	}
	// name clash on restore -> suffix
	f.put(t, f.alice, sp, "/docs/b.txt", "other")
	d = ck(f.svc.RestoreDocument(f.ctx, f.alice, a.File.DocumentID))
	if d.Links[0].Path != "/docs/b (restored).txt" {
		t.Fatalf("restore clash: %+v", d.Links)
	}

	// purge + GC removes the blob file
	if err := f.svc.DeleteLink(f.ctx, f.alice, d.Links[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.PurgeDocument(f.ctx, f.alice, d.ID); err != nil {
		t.Fatal(err)
	}
	if f.env.Blobs.Exists(d.SHA256) {
		t.Fatal("purge must delete the content at once")
	}
	// referenced blobs survive
	if f.count(t, `SELECT count(*) FROM blobs`) != 2 {
		t.Fatal("referenced blobs deleted")
	}
	// expired trash purge
	f.svc.DeleteLink(f.ctx, f.alice, ck(f.svc.FileByPath(f.ctx, f.alice, sp, []string{"docs", "b.txt"})).LinkID)
	f.env.Pool.Exec(f.ctx, `UPDATE documents SET deleted_at = now() - interval '400 days' WHERE deleted_at IS NOT NULL`)
	if n := ck(f.svc.PurgeExpired(f.ctx)); n != 1 {
		t.Fatalf("purge expired: %d", n)
	}
}

func TestCrossSpaceAndCopy(t *testing.T) {
	f := newFixture(t)
	work := ck(f.svc.CreateSpace(f.ctx, f.alice, "alice-work", "Work", ""))
	sp := f.home.ID
	r := f.put(t, f.alice, sp, "/report.txt", "r1")
	f.put(t, f.alice, sp, "/report.txt", "r2")
	wroot := ck(f.svc.FolderByPath(f.ctx, f.alice, work.ID, nil))

	// sole link: the document moves with its history
	m := ck(f.svc.MoveLink(f.ctx, f.alice, r.File.LinkID, wroot.ID, "", false))
	if m.SpaceID != work.ID || m.DocumentID != r.File.DocumentID || m.Version != 2 {
		t.Fatalf("cross move: %+v", m)
	}
	// copy within space: same document
	c := ck(f.svc.CopyLink(f.ctx, f.alice, m.LinkID, wroot.ID, "copy.txt", false))
	if c.DocumentID != m.DocumentID || c.LinkCount != 2 {
		t.Fatalf("copy: %+v", c)
	}
	// copy across spaces: new document with history, blob shared
	hroot := ck(f.svc.FolderByPath(f.ctx, f.alice, sp, nil))
	x := ck(f.svc.CopyLink(f.ctx, f.alice, m.LinkID, hroot.ID, "", false))
	if x.SpaceID != sp || x.DocumentID == m.DocumentID || x.Version != 2 {
		t.Fatalf("cross copy: %+v", x)
	}
	// folder tree copy and cross-space folder move
	f.put(t, f.alice, sp, "/tree/a/1.txt", "one")
	f.put(t, f.alice, sp, "/tree/b.txt", "two")
	tree := ck(f.svc.FolderByPath(f.ctx, f.alice, sp, []string{"tree"}))
	cp := ck(f.svc.CopyFolder(f.ctx, f.alice, tree.ID, hroot.ID, "tree-copy"))
	if f.read(t, f.alice, sp, "/tree-copy/a/1.txt") != "one" || cp.Path != "/tree-copy" {
		t.Fatal("copy folder")
	}
	mv := ck(f.svc.MoveFolder(f.ctx, f.alice, tree.ID, wroot.ID, ""))
	if mv.SpaceID != work.ID || f.read(t, f.alice, work.ID, "/tree/a/1.txt") != "one" {
		t.Fatalf("cross folder move: %+v", mv)
	}
	// tree-copy still readable in home (forked docs)
	if f.read(t, f.alice, sp, "/tree-copy/b.txt") != "two" {
		t.Fatal("copy lost after move")
	}
}

func TestPermissions(t *testing.T) {
	f := newFixture(t)
	team := ck(f.svc.CreateTeam(f.ctx, f.alice, "Team"))
	ts := ck(f.svc.CreateSpace(f.ctx, f.alice, "team-docs", "", team.ID))
	r := f.put(t, f.alice, ts.ID, "/a.txt", "a")

	// outsider sees nothing
	_, err := f.svc.FileByPath(f.ctx, f.bob, ts.ID, []string{"a.txt"})
	wantKind(t, err, core.ErrNotFound)
	_, err = f.svc.GetDocument(f.ctx, f.bob, r.File.DocumentID)
	wantKind(t, err, core.ErrNotFound)

	bobUser := ck(f.svc.UserByName(f.ctx, "bob"))
	noErr(t, f.svc.SetMember(f.ctx, f.alice, team.ID, bobUser.ID, "viewer"))
	if f.read(t, f.bob, ts.ID, "/a.txt") != "a" {
		t.Fatal("viewer read")
	}
	_, err = f.svc.PutFile(f.ctx, f.bob, ts.ID, []string{"b.txt"}, strings.NewReader("b"), core.PutOptions{})
	wantKind(t, err, core.ErrForbidden)

	noErr(t, f.svc.SetMember(f.ctx, f.alice, team.ID, bobUser.ID, "member"))
	f.put(t, f.bob, ts.ID, "/b.txt", "b")
	wantKind(t, f.svc.DeleteLink(f.ctx, f.bob, r.File.LinkID), core.ErrForbidden)
	// member may not move documents out of the team space
	_, err = f.svc.MoveLink(f.ctx, f.bob, r.File.LinkID, ck(f.svc.FolderByPath(f.ctx, f.bob, f.bhome.ID, nil)).ID, "", false)
	wantKind(t, err, core.ErrForbidden)
	// but may copy out
	ck(f.svc.CopyLink(f.ctx, f.bob, r.File.LinkID, ck(f.svc.FolderByPath(f.ctx, f.bob, f.bhome.ID, nil)).ID, "", false))

	noErr(t, f.svc.SetMember(f.ctx, f.alice, team.ID, bobUser.ID, "admin"))
	noErr(t, f.svc.DeleteLink(f.ctx, f.bob, r.File.LinkID))

	// read-only access key
	_, sec := ck2(f.svc.CreateAccessKey(f.ctx, f.alice, "ro", "", true))
	kp := ck(f.svc.AuthenticateAppPassword(f.ctx, "alice", sec))
	_, err = f.svc.PutFile(f.ctx, kp, f.home.ID, []string{"x.txt"}, strings.NewReader("x"), core.PutOptions{})
	wantKind(t, err, core.ErrForbidden)
}

func TestReviveOnDeleteAndRecreate(t *testing.T) {
	f := newFixture(t)
	sp := f.home.ID
	_, sec := ck2(f.svc.CreateAccessKey(f.ctx, f.alice, "sftp", "", false))
	cl := ck(f.svc.AuthenticateAppPassword(f.ctx, "alice", sec))
	orig := f.put(t, cl, sp, "/doc.txt", "v1")
	f.env.Pool.Exec(f.ctx, `UPDATE documents SET created_at = now() - interval '1 hour'`)

	// client deletes the file, then uploads a temp file and renames it into place
	noErr(t, f.svc.DeleteLink(f.ctx, cl, orig.File.LinkID))
	part := f.put(t, cl, sp, "/doc.txt.part", "v2")
	res := ck(f.svc.MoveLink(f.ctx, cl, part.File.LinkID, "", "doc.txt", false))
	if res.DocumentID != orig.File.DocumentID || res.Version != 2 || res.LinkID != orig.File.LinkID {
		t.Fatalf("revive via rename: %+v", res)
	}
	// delete + direct upload
	noErr(t, f.svc.DeleteLink(f.ctx, cl, res.LinkID))
	r := f.put(t, cl, sp, "/doc.txt", "v3")
	if r.File.DocumentID != orig.File.DocumentID || r.File.Version != 3 {
		t.Fatalf("revive via upload: %+v", r.File)
	}
	if tr := ck(f.svc.ListTrash(f.ctx, f.alice, "")); len(tr) != 0 {
		t.Fatalf("trash: %+v", tr)
	}
	// UI deletes are final (no revive for session principals)
	noErr(t, f.svc.DeleteLink(f.ctx, f.alice, r.File.LinkID))
	r2 := f.put(t, f.alice, sp, "/doc.txt", "v4")
	if r2.File.DocumentID == orig.File.DocumentID {
		t.Fatal("session upload must not revive")
	}
}
