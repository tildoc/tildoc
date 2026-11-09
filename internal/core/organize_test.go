package core_test

import (
	"errors"
	"io"
	"testing"

	"github.com/tildoc/tildoc/internal/core"
	"github.com/tildoc/tildoc/internal/vfs"
)

func tagNamesOf(tags []core.TagRef) []string {
	out := []string{}
	for _, t := range tags {
		out = append(out, t.Name)
	}
	return out
}

func TestTags(t *testing.T) {
	f := newFixture(t)
	admin := session(t, f.svc, "admin", "supersecret")
	a := f.put(t, f.alice, f.home.ID, "/bescheid.txt", "Steuerbescheid")
	b := f.put(t, f.bob, f.bhome.ID, "/rechnung.txt", "Rechnung")

	// one vocabulary: same name (any case) is the same tag; users may create
	tax := ck(f.svc.CreateTag(f.ctx, f.alice, "  Steuer   2025 ", "#1F4E79"))
	if tax.Name != "Steuer 2025" || tax.Color != "#1f4e79" {
		t.Fatalf("normalized tag: %+v", tax)
	}
	if again := ck(f.svc.CreateTag(f.ctx, f.bob, "steuer 2025", "")); again.ID != tax.ID {
		t.Fatal("case-insensitive duplicate created a second tag")
	}
	if _, err := f.svc.CreateTag(f.ctx, f.alice, "", ""); core.KindOf(err) != core.ErrInvalid {
		t.Fatalf("empty tag: %v", err)
	}
	ro := *f.alice
	ro.ReadOnly = true
	if _, err := f.svc.CreateTag(f.ctx, &ro, "nope", ""); err == nil {
		t.Fatal("read-only key created a tag")
	}

	// attaching needs write access to the document's space
	if err := f.svc.TagDocument(f.ctx, f.alice, a.File.DocumentID, tax.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.TagDocument(f.ctx, f.bob, a.File.DocumentID, tax.ID); err == nil {
		t.Fatal("bob tagged alice's document")
	}
	if err := f.svc.TagDocument(f.ctx, f.bob, b.File.DocumentID, tax.ID); err != nil {
		t.Fatal(err)
	}

	// counts and search only cover readable spaces
	for _, c := range []struct {
		p    *core.Principal
		want int
	}{{f.alice, 1}, {f.bob, 1}, {admin, 0}} {
		tags := ck(f.svc.ListTags(f.ctx, c.p))
		if len(tags) != 1 || tags[0].Count != c.want {
			t.Fatalf("tag counts for %s: %+v", c.p.Username, tags)
		}
		if h := f.hits(t, c.p, core.SearchQuery{TagIDs: []string{tax.ID}}); len(h) != c.want {
			t.Fatalf("tag search for %s: %d hits", c.p.Username, len(h))
		}
	}
	if fi := ck(f.svc.GetFile(f.ctx, f.alice, a.File.LinkID)); len(fi.Tags) != 1 || fi.Tags[0].Name != "Steuer 2025" {
		t.Fatalf("file tags: %+v", fi.Tags)
	}

	// splitting a document (copy-on-write) keeps its tags on both halves
	hroot := ck(f.svc.FolderByPath(f.ctx, f.alice, f.home.ID, nil))
	ck(f.svc.AddLink(f.ctx, f.alice, a.File.DocumentID, hroot.ID, "kopie.txt"))
	fork := f.put(t, f.alice, f.home.ID, "/kopie.txt", "Steuerbescheid geaendert")
	if !fork.Forked || len(fork.File.Tags) != 1 {
		t.Fatalf("fork tags: %+v", fork.File)
	}

	// vocabulary changes are for administrators
	newName := "Tax 2025"
	if _, err := f.svc.UpdateTag(f.ctx, f.alice, tax.ID, &newName, nil); core.KindOf(err) != core.ErrForbidden {
		t.Fatalf("user renamed tag: %v", err)
	}
	if err := f.svc.DeleteTag(f.ctx, f.bob, tax.ID); core.KindOf(err) != core.ErrForbidden {
		t.Fatalf("user deleted tag: %v", err)
	}
	other := ck(f.svc.CreateTag(f.ctx, f.alice, "Steuern", ""))
	if err := f.svc.TagDocument(f.ctx, f.alice, a.File.DocumentID, other.ID); err != nil {
		t.Fatal(err)
	}
	merged := ck(f.svc.MergeTags(f.ctx, admin, other.ID, tax.ID))
	if merged.ID != tax.ID {
		t.Fatal("merge returned wrong tag")
	}
	d := ck(f.svc.GetDocument(f.ctx, f.alice, a.File.DocumentID))
	if names := tagNamesOf(d.Tags); len(names) != 1 || names[0] != "Steuer 2025" {
		t.Fatalf("after merge: %v", names)
	}

	// replacing by names creates missing tags (S3 tagging)
	if err := f.svc.SetDocumentTagNames(f.ctx, f.alice, a.File.DocumentID, []string{"Belege", "steuer 2025"}); err != nil {
		t.Fatal(err)
	}
	d = ck(f.svc.GetDocument(f.ctx, f.alice, a.File.DocumentID))
	if names := tagNamesOf(d.Tags); len(names) != 2 || names[0] != "Belege" || names[1] != "Steuer 2025" {
		t.Fatalf("set by names: %v", names)
	}
	if err := f.svc.DeleteTag(f.ctx, admin, tax.ID); err != nil {
		t.Fatal(err)
	}
	if d = ck(f.svc.GetDocument(f.ctx, f.alice, a.File.DocumentID)); len(d.Tags) != 1 {
		t.Fatalf("after delete: %+v", d.Tags)
	}
}

func TestCollections(t *testing.T) {
	f := newFixture(t)
	carol := ck(f.svc.CreateUser(f.ctx, session(t, f.svc, "admin", "supersecret"), "carol", "carolpass", "", false))
	cp := session(t, f.svc, "carol", "carolpass")
	team := ck(f.svc.CreateTeam(f.ctx, f.alice, "Buchhaltung"))
	if err := f.svc.SetMember(f.ctx, f.alice, team.ID, ck(f.svc.UserByName(f.ctx, "bob")).ID, "member"); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.SetMember(f.ctx, f.alice, team.ID, carol.ID, "member"); err != nil {
		t.Fatal(err)
	}
	ts := ck(f.svc.CreateSpace(f.ctx, f.alice, "belege", "Belege", team.ID))

	private := f.put(t, f.alice, f.home.ID, "/Privat/strom.pdf", "%PDF private")
	shared := f.put(t, f.alice, ts.ID, "/2025/miete.pdf", "%PDF shared")
	bobs := f.put(t, f.bob, f.bhome.ID, "/strom.pdf", "%PDF bob")

	// personal collection across spaces
	mine := ck(f.svc.CreateCollection(f.ctx, f.alice, "Steuer 2025", "", nil))
	if err := f.svc.AddToCollection(f.ctx, f.alice, mine.ID, []string{private.File.DocumentID, shared.File.DocumentID}); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.AddToCollection(f.ctx, f.alice, mine.ID, []string{bobs.File.DocumentID}); err == nil {
		t.Fatal("added a document alice cannot read")
	}
	if items := ck(f.svc.CollectionItems(f.ctx, f.alice, mine.ID)); len(items) != 2 || !items[0].Manual {
		t.Fatalf("personal items: %+v", items)
	}
	if _, err := f.svc.GetCollection(f.ctx, f.bob, mine.ID); core.KindOf(err) != core.ErrNotFound {
		t.Fatalf("bob sees alice's personal collection: %v", err)
	}

	// team collection: members edit, viewers view, only owners/admins create
	if _, err := f.svc.CreateCollection(f.ctx, f.bob, "Team", team.ID, nil); core.KindOf(err) != core.ErrForbidden {
		t.Fatalf("member created team collection: %v", err)
	}
	tc := ck(f.svc.CreateCollection(f.ctx, f.alice, "Steuer 2025", team.ID, nil))
	// a collection never grants access: alice's private document stays hidden
	if err := f.svc.AddToCollection(f.ctx, f.alice, tc.ID, []string{private.File.DocumentID, shared.File.DocumentID}); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.AddToCollection(f.ctx, f.bob, tc.ID, []string{bobs.File.DocumentID}); err != nil {
		t.Fatal(err)
	}
	if items := ck(f.svc.CollectionItems(f.ctx, f.alice, tc.ID)); len(items) != 2 {
		t.Fatalf("alice sees %d team items (her private doc and the shared one, not bob's)", len(items))
	}
	bobView := ck(f.svc.CollectionItems(f.ctx, f.bob, tc.ID))
	if len(bobView) != 2 {
		t.Fatalf("bob sees %d items", len(bobView))
	}
	for _, it := range bobView {
		if it.DocumentID == private.File.DocumentID {
			t.Fatal("collection leaked alice's private document to bob")
		}
	}
	if c := ck(f.svc.GetCollection(f.ctx, cp, tc.ID)); c.Count != 1 {
		t.Fatalf("viewer view: %+v", c)
	}
	if h := f.hits(t, f.bob, core.SearchQuery{CollectionID: tc.ID}); len(h) != 2 {
		t.Fatalf("bob collection search: %d", len(h))
	}
	if _, err := f.svc.Search(f.ctx, f.bob, core.SearchQuery{CollectionID: mine.ID}); err == nil {
		t.Fatal("search in a foreign collection")
	}

	// tag rule: tagged documents are part of the collection automatically
	tax := ck(f.svc.CreateTag(f.ctx, f.alice, "Steuer 2025", ""))
	ruleTag := tax.ID
	ck(f.svc.UpdateCollection(f.ctx, f.alice, mine.ID, core.CollectionUpdate{TagID: &ruleTag}))
	extra := f.put(t, f.alice, f.home.ID, "/spende.pdf", "%PDF spende")
	if err := f.svc.TagDocument(f.ctx, f.alice, extra.File.DocumentID, tax.ID); err != nil {
		t.Fatal(err)
	}
	items := ck(f.svc.CollectionItems(f.ctx, f.alice, mine.ID))
	if len(items) != 3 {
		t.Fatalf("with tag rule: %d items", len(items))
	}
	if err := f.svc.RemoveFromCollection(f.ctx, f.alice, mine.ID, []string{extra.File.DocumentID}); err != nil {
		t.Fatal(err)
	}
	if items = ck(f.svc.CollectionItems(f.ctx, f.alice, mine.ID)); len(items) != 3 {
		t.Fatal("documents included through the tag must stay")
	}
	if d := ck(f.svc.GetDocument(f.ctx, f.alice, extra.File.DocumentID)); len(d.Collections) != 1 || d.Collections[0].ID != mine.ID {
		t.Fatalf("document collections: %+v", d.Collections)
	}

	// membership follows the document: moves keep it, trash hides it
	broot := ck(f.svc.FolderByPath(f.ctx, f.alice, ts.ID, nil))
	ck(f.svc.MoveLink(f.ctx, f.alice, private.File.LinkID, broot.ID, "", false))
	if items = ck(f.svc.CollectionItems(f.ctx, f.bob, tc.ID)); len(items) != 3 {
		t.Fatalf("after moving into the team space bob sees %d items", len(items))
	}
	if err := f.svc.DeleteLink(f.ctx, f.alice, shared.File.LinkID); err != nil {
		t.Fatal(err)
	}
	if items = ck(f.svc.CollectionItems(f.ctx, f.bob, tc.ID)); len(items) != 2 {
		t.Fatalf("trashed document still listed: %d", len(items))
	}
	ck(f.svc.RestoreDocument(f.ctx, f.alice, shared.File.DocumentID))
	if items = ck(f.svc.CollectionItems(f.ctx, f.bob, tc.ID)); len(items) != 3 {
		t.Fatalf("restored document missing: %d", len(items))
	}

	// read-only folders in WebDAV/SFTP
	fs := vfs.New(f.svc)
	root := ck(fs.List(f.ctx, f.bob, "/"))
	var hasDir bool
	for _, n := range root {
		hasDir = hasDir || n.Name == vfs.CollectionsDir
	}
	if !hasDir {
		t.Fatal("/@collections missing for bob")
	}
	dirs := ck(fs.List(f.ctx, f.alice, "/@collections"))
	if len(dirs) != 2 || dirs[0].Name != "Steuer 2025" || dirs[1].Name != "Steuer 2025 (Buchhaltung)" {
		t.Fatalf("collection dirs: %+v", dirs)
	}
	files := ck(fs.List(f.ctx, f.bob, "/@collections/Steuer 2025"))
	names := map[string]bool{}
	for _, n := range files {
		names[n.Name] = true
	}
	if len(files) != 3 || !names["strom.pdf"] || !names["strom (2).pdf"] || !names["miete.pdf"] {
		t.Fatalf("collection files: %v", names)
	}
	_, rd, err := fs.Open(f.ctx, f.bob, "/@collections/Steuer 2025/miete.pdf")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(rd)
	rd.Close()
	if string(body) != "%PDF shared" {
		t.Fatalf("collection read: %q", body)
	}
	for name, err := range map[string]error{
		"put": func() error {
			_, err := fs.Put(f.ctx, f.bob, "/@collections/Steuer 2025/x.pdf", nil, core.PutOptions{})
			return err
		}(),
		"mkdir":  fs.Mkdir(f.ctx, f.bob, "/@collections/Steuer 2025/sub"),
		"remove": fs.Remove(f.ctx, f.bob, "/@collections/Steuer 2025/miete.pdf"),
		"rename": fs.Rename(f.ctx, f.bob, "/@collections/Steuer 2025/miete.pdf", "/bob/miete.pdf", false),
	} {
		if !errors.Is(err, core.ErrForbidden) {
			t.Errorf("%s inside /@collections: %v", name, err)
		}
	}
	if err := fs.Copy(f.ctx, f.bob, "/@collections/Steuer 2025/miete.pdf", "/bob/miete.pdf", false); err != nil {
		t.Fatalf("copy out of a collection: %v", err)
	}
	if err := f.svc.DeleteCollection(f.ctx, f.bob, tc.ID); core.KindOf(err) != core.ErrForbidden {
		t.Fatalf("member deleted team collection: %v", err)
	}
	if err := f.svc.DeleteCollection(f.ctx, f.alice, tc.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.GetDocument(f.ctx, f.bob, shared.File.DocumentID); err != nil {
		t.Fatal("deleting a collection must not touch its documents")
	}
}
