package core_test

import (
	"strings"
	"testing"

	"github.com/tildoc/tildoc/internal/core"
)

func TestWritePolicies(t *testing.T) {
	f := newFixture(t)
	doc := f.put(t, f.alice, f.home.ID, "/a.txt", "one")
	docID, linkID := doc.File.DocumentID, doc.File.LinkID
	root := ck(f.svc.FolderByPath(f.ctx, f.alice, f.home.ID, nil))
	tag := ck(f.svc.CreateTag(f.ctx, f.alice, "Beleg", ""))
	set := func(policy string) {
		t.Helper()
		ck(f.svc.UpdateSpaceSettings(f.ctx, f.alice, f.home.ID, core.SpaceSettings{WritePolicy: policy}))
	}
	put := func(name, content string) error {
		_, err := f.svc.PutFile(f.ctx, f.alice, f.home.ID, []string{name}, strings.NewReader(content), core.PutOptions{})
		return err
	}
	// what exists can't change, whatever the policy but open
	fixed := func() {
		t.Helper()
		wantKind(t, put("a.txt", "two"), core.ErrForbidden)
		_, err := f.svc.AddDocumentVersion(f.ctx, f.alice, docID, strings.NewReader("two"), "web")
		wantKind(t, err, core.ErrForbidden)
		_, err = f.svc.MoveLink(f.ctx, f.alice, linkID, root.ID, "b.txt", false)
		wantKind(t, err, core.ErrForbidden)
		wantKind(t, f.svc.DeleteLink(f.ctx, f.alice, linkID), core.ErrForbidden)
	}

	set(core.WriteAppendOnly)
	fixed()
	noErr(t, put("a.txt", "one")) // the same content again changes nothing
	noErr(t, put("new.txt", "new"))
	ck(f.svc.Mkdir(f.ctx, f.alice, root.ID, "sub"))
	noErr(t, f.svc.TagDocument(f.ctx, f.alice, docID, tag.ID))

	set(core.WriteReadOnly)
	fixed()
	wantKind(t, put("new2.txt", "x"), core.ErrForbidden)
	noErr(t, f.svc.UntagDocument(f.ctx, f.alice, docID, tag.ID)) // about the data, not the tags

	set(core.WriteFrozen)
	fixed()
	wantKind(t, put("new2.txt", "x"), core.ErrForbidden)
	wantKind(t, f.svc.TagDocument(f.ctx, f.alice, docID, tag.ID), core.ErrForbidden)
	_, err := f.svc.UpdateSpaceSettings(f.ctx, f.alice, f.home.ID, core.SpaceSettings{WritePolicy: core.WriteFrozen, NoOCR: true})
	wantKind(t, err, core.ErrForbidden)
	if ck(f.svc.OCRAllowed(f.ctx, doc.File.SHA256)) {
		t.Fatal("OCR runs in a frozen space")
	}
	ck(f.svc.GetDocument(f.ctx, f.alice, docID)) // reading goes on

	// omitted: stays as it is, a partial update can't open a space
	ck(f.svc.UpdateSpaceSettings(f.ctx, f.alice, f.home.ID, core.SpaceSettings{WritePolicy: core.WriteAppendOnly}))
	ck(f.svc.UpdateSpaceSettings(f.ctx, f.alice, f.home.ID, core.SpaceSettings{NoOCR: true}))
	if p := spaceFor(t, f, f.alice, f.home.ID).Settings.WritePolicy; p != core.WriteAppendOnly {
		t.Fatalf("policy became %q", p)
	}
	_, err = f.svc.UpdateSpaceSettings(f.ctx, f.alice, f.home.ID, core.SpaceSettings{WritePolicy: "nonsense"})
	wantKind(t, err, core.ErrInvalid)

	// the database refuses it too
	for _, q := range []string{
		`UPDATE entries SET name='x.txt' WHERE id=$1`,
		`DELETE FROM entries WHERE id=$1`,
	} {
		if _, err := f.svc.DB.Exec(f.ctx, q, linkID); err == nil {
			t.Fatalf("%s went through", q)
		}
	}
	if _, err := f.svc.DB.Exec(f.ctx, `DELETE FROM document_versions WHERE document_id=$1`, docID); err == nil {
		t.Fatal("deleted a version in an append-only space")
	}

	set(core.WriteOpen)
	noErr(t, put("a.txt", "two"))
	noErr(t, f.svc.DeleteLink(f.ctx, f.alice, linkID))
}

func TestAddOnlyKey(t *testing.T) {
	f := newFixture(t)
	f.put(t, f.alice, f.home.ID, "/a.txt", "one")
	_, token := ck2(f.svc.CreateAPIKey(f.ctx, f.alice, "x", []string{"view", "add"}, []string{f.home.ID}, nil))
	key := ck(f.svc.APIKeyPrincipal(f.ctx, token))
	put := func(name, content string) error {
		_, err := f.svc.PutFile(f.ctx, key, f.home.ID, []string{name}, strings.NewReader(content), core.PutOptions{})
		return err
	}
	noErr(t, put("b.txt", "new"))
	noErr(t, put("a.txt", "one"))
	wantKind(t, put("a.txt", "changed"), core.ErrForbidden)
}

func TestExternalReferences(t *testing.T) {
	f := newFixture(t)
	put := func(ref, path, content string, opt core.PutOptions) (*core.PutResult, error) {
		segs := ck(core.SplitPath(path))
		return f.svc.PutExternal(f.ctx, f.alice, f.home.ID, "billing", ref, segs, strings.NewReader(content), opt)
	}
	first := ck(put("42", "/Invoices/42.pdf", "one", core.PutOptions{}))
	if !first.NewDocument {
		t.Fatalf("%+v", first)
	}
	again := ck(put("42", "/elsewhere.pdf", "one", core.PutOptions{}))
	if again.NewVersion || again.NewDocument || again.File.DocumentID != first.File.DocumentID {
		t.Fatalf("retry changed something: %+v", again)
	}
	second := ck(put("42", "", "two", core.PutOptions{}))
	if !second.NewVersion || second.File.Path != first.File.Path {
		t.Fatalf("%+v", second)
	}
	e := ck(f.svc.GetExternal(f.ctx, f.alice, f.home.ID, "billing", "42"))
	if e.DocumentID != first.File.DocumentID {
		t.Fatalf("%+v", e)
	}
	// conditions
	_, err := put("43", "/43.pdf", "x", core.PutOptions{SHA256: strings.Repeat("0", 64)})
	wantKind(t, err, core.ErrChecksum)
	_, err = put("42", "", "three", core.PutOptions{IfMatch: `"00000000000000000000000000000000"`})
	wantKind(t, err, core.ErrPrecondition)
	ck(put("42", "", "three", core.PutOptions{IfMatch: second.File.ETag()}))
	_, err = put("44", first.File.Path, "x", core.PutOptions{CreateOnly: true})
	wantKind(t, err, core.ErrPrecondition)
	// invalid names, other people
	_, err = f.svc.GetExternal(f.ctx, f.alice, f.home.ID, "Bad Source", "1")
	wantKind(t, err, core.ErrInvalid)
	_, err = f.svc.GetExternal(f.ctx, f.bob, f.home.ID, "billing", "42")
	wantKind(t, err, core.ErrNotFound)
	// pages
	for _, r := range []string{"a", "b", "c"} {
		ck(put(r, "/"+r+".pdf", r, core.PutOptions{}))
	}
	seen := 0
	cursor := ""
	for {
		items, next := ck2(f.svc.ListExternal(f.ctx, f.alice, f.home.ID, "billing", cursor, 2))
		seen += len(items)
		if next == "" {
			break
		}
		cursor = next
	}
	if seen != 4 {
		t.Fatalf("listed %d references", seen)
	}
	noErr(t, f.svc.DeleteExternal(f.ctx, f.alice, f.home.ID, "billing", "a"))
	_, err = f.svc.GetExternal(f.ctx, f.alice, f.home.ID, "billing", "a")
	wantKind(t, err, core.ErrNotFound)
	// append-only: a retry works, new content under the same reference doesn't
	ck(f.svc.UpdateSpaceSettings(f.ctx, f.alice, f.home.ID, core.SpaceSettings{WritePolicy: core.WriteAppendOnly}))
	ck(put("b", "", "b", core.PutOptions{}))
	_, err = put("b", "", "changed", core.PutOptions{})
	wantKind(t, err, core.ErrForbidden)
}

// A key limited to one space must not be able to mint wider credentials.
func TestAPIKeyCannotMintCredentials(t *testing.T) {
	f := newFixture(t)
	_, token := ck2(f.svc.CreateAPIKey(f.ctx, f.alice, "x", []string{"view"}, []string{f.home.ID}, nil))
	key := ck(f.svc.APIKeyPrincipal(f.ctx, token))
	_, _, err := f.svc.CreateAccessKey(f.ctx, key, "x", "", false)
	wantKind(t, err, core.ErrForbidden)
	_, err = f.svc.AddSSHKey(f.ctx, key, "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGJvb2dhbG9vZ2Fib29nYWxvb2dhYm9vZ2Fsb29nYWJvb2c x", "x")
	wantKind(t, err, core.ErrForbidden)
}
