package core_test

import (
	"strings"
	"testing"

	"github.com/tildoc/tildoc/internal/core"
)

// The schema itself must refuse inconsistent data, independent of the Go code.
func TestSchemaInvariants(t *testing.T) {
	f := newFixture(t)
	work := ck(f.svc.CreateSpace(f.ctx, f.alice, "alice-work", "Work", ""))
	a := f.put(t, f.alice, f.home.ID, "/dir/a.txt", "alpha")
	w := f.put(t, f.alice, work.ID, "/w.txt", "omega")
	dir := ck(f.svc.FolderByPath(f.ctx, f.alice, f.home.ID, []string{"dir"}))
	wroot := ck(f.svc.FolderByPath(f.ctx, f.alice, work.ID, nil))

	refuse := func(name, sql string, args ...any) {
		t.Helper()
		if _, err := f.env.Pool.Exec(f.ctx, sql, args...); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}

	// namespace: space membership, folder parents, one name per folder
	refuse("link into a folder of another space",
		`UPDATE entries SET parent_id=$2 WHERE id=$1`, a.File.LinkID, wroot.ID)
	refuse("link to a document of another space",
		`UPDATE entries SET document_id=$2 WHERE id=$1`, a.File.LinkID, w.File.DocumentID)
	refuse("folder and file with the same name",
		`INSERT INTO entries(space_id, kind, parent_id, name) VALUES($1, 'folder', $2, 'a.txt')`, f.home.ID, dir.ID)
	refuse("link as parent",
		`INSERT INTO entries(space_id, kind, parent_id, name) VALUES($1, 'folder', $2, 'sub')`, f.home.ID, a.File.LinkID)
	refuse("second root folder",
		`INSERT INTO entries(space_id, kind, parent_id, name) VALUES($1, 'folder', NULL, '')`, f.home.ID)
	refuse("live link without folder",
		`UPDATE entries SET parent_id=NULL WHERE id=$1`, a.File.LinkID)
	refuse("deleting a folder that still holds a live file",
		`DELETE FROM entries WHERE id=$1`, dir.ID)
	refuse("folder with a document",
		`UPDATE entries SET document_id=$2 WHERE id=$1`, dir.ID, a.File.DocumentID)
	for _, bad := range []string{"a/b", "..", ".", "x\x01y", "é", strings.Repeat("x", 256)} {
		refuse("invalid name "+bad, `UPDATE entries SET name=$2 WHERE id=$1`, dir.ID, bad)
	}

	// documents: head must exist, trash state must match the links
	refuse("current version without a version row",
		`UPDATE documents SET current_version=7 WHERE id=$1`, a.File.DocumentID)
	refuse("trashed document with a live link",
		`UPDATE documents SET deleted_at=now() WHERE id=$1`, a.File.DocumentID)
	refuse("live document without a live link",
		`UPDATE entries SET deleted_at=now(), deleted_path='/dir/a.txt' WHERE id=$1`, a.File.LinkID)

	// closed value sets and formats
	refuse("unknown viewer", `UPDATE users SET pdf_viewer='acrobat'`)
	refuse("username with capitals", `UPDATE users SET username='Alice' WHERE username='alice'`)
	refuse("invalid slug", `UPDATE spaces SET slug='Bad--Slug' WHERE id=$1`, work.ID)
	refuse("reserved slug", `UPDATE spaces SET slug='dav' WHERE id=$1`, work.ID)
	refuse("unknown protocol", `UPDATE imports SET protocol='ftp'`)
	refuse("unknown version source", `UPDATE document_versions SET source='magic'`)
	refuse("unknown job kind", `INSERT INTO jobs(kind) VALUES('bogus')`)
	refuse("running job without lock", `INSERT INTO jobs(kind, status) VALUES('gc_blobs', 'running')`)
	refuse("tag name with outer spaces", `INSERT INTO tags(name) VALUES(' tax ')`)
	if _, err := f.env.Pool.Exec(f.ctx, `INSERT INTO tags(name) VALUES('Steuer')`); err != nil {
		t.Fatal(err)
	}
	refuse("tag names are unique ignoring case", `INSERT INTO tags(name) VALUES('steuer')`)

	// the Go code keeps everything consistent through normal operations
	ck(f.svc.MoveLink(f.ctx, f.alice, a.File.LinkID, wroot.ID, "", false))
	if err := f.svc.DeleteFolder(f.ctx, f.alice, dir.ID); err != nil {
		t.Fatal(err)
	}
}

// Moving a document into a space that already holds the same content keeps
// one document per content: the link joins the existing document, whose
// history absorbs the moved document's versions in chronological order.
func TestMoveIntoSpaceWithSameContentMerges(t *testing.T) {
	f := newFixture(t)
	work := ck(f.svc.CreateSpace(f.ctx, f.alice, "alice-work", "Work", ""))
	wroot := ck(f.svc.FolderByPath(f.ctx, f.alice, work.ID, nil))

	f.put(t, f.alice, work.ID, "/a.txt", "draft A")
	a := f.put(t, f.alice, work.ID, "/a.txt", "final")
	f.put(t, f.alice, f.home.ID, "/b.txt", "draft B")
	b := f.put(t, f.alice, f.home.ID, "/b.txt", "final")
	files := blobFiles(t, f)

	m := ck(f.svc.MoveLink(f.ctx, f.alice, b.File.LinkID, wroot.ID, "", false))
	if m.DocumentID != a.File.DocumentID || m.LinkID != b.File.LinkID || m.LinkCount != 2 || m.Version != 4 {
		t.Fatalf("merged move: %+v", m)
	}
	d := ck(f.svc.GetDocument(f.ctx, f.alice, a.File.DocumentID))
	var got []string
	for i := len(d.Versions) - 1; i >= 0; i-- {
		v := d.Versions[i]
		rd, _, err := f.svc.OpenDocument(f.ctx, f.alice, d.ID, v.Version, "", false)
		if err != nil {
			t.Fatal(err)
		}
		buf := make([]byte, 64)
		n, _ := rd.Read(buf)
		rd.Close()
		got = append(got, string(buf[:n]))
	}
	if strings.Join(got, "|") != "draft A|final|draft B|final" {
		t.Fatalf("merged history: %v", got)
	}
	if merged := d.Versions[0].Meta["merged_from"]; merged != b.File.DocumentID {
		t.Fatalf("merged version meta: %+v", d.Versions[0].Meta)
	}
	if _, err := f.svc.GetDocument(f.ctx, f.alice, b.File.DocumentID); err == nil {
		t.Fatal("moved document must be gone after the merge")
	}
	if n := f.count(t, `SELECT count(*) FROM documents WHERE space_id=$1`, work.ID); n != 1 {
		t.Fatalf("documents in work: %d", n)
	}
	sameFiles(t, "merge", files, blobFiles(t, f))

	// a document with several places: the moved place joins the existing
	// document, the source keeps its other place and its own history
	c := f.put(t, f.alice, f.home.ID, "/c.txt", "final")
	hroot := ck(f.svc.FolderByPath(f.ctx, f.alice, f.home.ID, nil))
	ck(f.svc.AddLink(f.ctx, f.alice, c.File.DocumentID, hroot.ID, "c2.txt"))
	mc := ck(f.svc.MoveLink(f.ctx, f.alice, c.File.LinkID, wroot.ID, "", false))
	if mc.DocumentID != a.File.DocumentID || mc.LinkCount != 3 {
		t.Fatalf("multi-place merge: %+v", mc)
	}
	src := ck(f.svc.GetDocument(f.ctx, f.alice, c.File.DocumentID))
	if len(src.Links) != 1 || src.Links[0].Name != "c2.txt" || len(src.Versions) != 1 {
		t.Fatalf("source after split merge: %+v", src)
	}
	if v := ck(f.svc.GetDocument(f.ctx, f.alice, a.File.DocumentID)); len(v.Versions) != 5 {
		t.Fatalf("target history: %d versions", len(v.Versions))
	}

	// a fresh upload of the same bytes is still deduplicated afterwards
	if r := f.put(t, f.alice, work.ID, "/again.txt", "final"); !r.Deduplicated || r.File.DocumentID != a.File.DocumentID {
		t.Fatalf("dedup after merge: %+v", r)
	}
	_ = core.ErrNotFound
}
