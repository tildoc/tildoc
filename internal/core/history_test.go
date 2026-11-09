package core_test

import (
	"strings"
	"testing"

	"github.com/tildoc/tildoc/internal/blob"
	"github.com/tildoc/tildoc/internal/core"
)

func stageText(t *testing.T, f *fixture, s string) *blob.Staged {
	t.Helper()
	return ck(f.env.Blobs.Stage(strings.NewReader(s), 0))
}

func versionByNumber(t *testing.T, d *core.Document, n int) core.VersionInfo {
	t.Helper()
	for _, v := range d.Versions {
		if v.Version == n {
			return v
		}
	}
	t.Fatalf("no version %d", n)
	return core.VersionInfo{}
}

// The history graph data: variants per version, restores (stored) and
// continuations from variants or older versions (derived from hashes).
func TestHistoryVersionsAndVariants(t *testing.T) {
	f := newFixture(t)
	r := f.put(t, f.alice, f.home.ID, "/scan.pdf", "%PDF scan v1")
	doc := r.File.DocumentID

	// v1: original -> image only -> tildoc OCR
	io := stageText(t, f, "%PDF image only of v1")
	noErr(t, f.svc.StoreDerivative(f.ctx, r.File.SHA256, core.Derivation{Kind: "image_only_pdf", Input: r.File.SHA256, Engine: "gs"}, io))
	ocr := stageText(t, f, "%PDF tildoc ocr of v1")
	noErr(t, f.svc.StoreDerivative(f.ctx, r.File.SHA256, core.Derivation{Kind: "searchable_pdf", Input: io.SHA256, Engine: "ocrmypdf"}, ocr))

	ck(f.svc.AddDocumentVersion(f.ctx, f.alice, doc, strings.NewReader("%PDF v2 from WebDAV"), "webdav"))
	ck(f.svc.AddDocumentVersion(f.ctx, f.alice, doc, strings.NewReader("%PDF tildoc ocr of v1"), "web")) // v3: the OCR PDF again
	ck(f.svc.RestoreVersion(f.ctx, f.alice, doc, 2))                                                     // v4
	d := ck(f.svc.AddDocumentVersion(f.ctx, f.alice, doc, strings.NewReader("%PDF scan v1"), "web"))     // v5: v1 again

	if d.Version != 5 || len(d.Versions) != 5 || d.Versions[0].Version != 5 {
		t.Fatalf("versions (newest first): %+v", d.Versions)
	}
	v1 := versionByNumber(t, d, 1)
	if len(v1.Variants) != 2 || v1.Variants[1].Input != io.SHA256 {
		t.Fatalf("v1 variants: %+v", v1.Variants)
	}
	if len(versionByNumber(t, d, 2).Variants) != 0 {
		t.Fatal("v2 has no variants")
	}
	if c := versionByNumber(t, d, 3).ContinuedFrom; c == nil || c.Version != 1 || c.Variant != "searchable_pdf" {
		t.Fatalf("v3 continued from: %+v", c)
	}
	if v4 := versionByNumber(t, d, 4); v4.RestoredFrom == nil || *v4.RestoredFrom != 2 || v4.ContinuedFrom != nil || v4.Source != "restore" {
		t.Fatalf("v4: %+v", v4)
	}
	if c := versionByNumber(t, d, 5).ContinuedFrom; c == nil || c.Version != 1 || c.Variant != "" {
		t.Fatalf("v5 continued from: %+v", c)
	}
	if versionByNumber(t, d, 2).ContinuedFrom != nil {
		t.Fatal("v2 continues from nothing")
	}

	// the database keeps restores consistent
	_, err := f.env.Pool.Exec(f.ctx, `UPDATE document_versions SET restored_from=NULL WHERE document_id=$1 AND version=4`, doc)
	if err == nil {
		t.Fatal("restore without source version accepted")
	}
	_, err = f.env.Pool.Exec(f.ctx, `UPDATE document_versions SET restored_from=4 WHERE document_id=$1 AND version=4`, doc)
	if err == nil {
		t.Fatal("restore from itself accepted")
	}
	_, err = f.env.Pool.Exec(f.ctx, `UPDATE document_versions SET restored_from=1 WHERE document_id=$1 AND version=2`, doc)
	if err == nil {
		t.Fatal("restored_from on a non-restore version accepted")
	}
}

// Merging histories renumbers versions; restores keep pointing at the
// version whose content they restored.
func TestHistoryMergeKeepsRestores(t *testing.T) {
	f := newFixture(t)
	team := ck(f.svc.CreateTeam(f.ctx, f.alice, "T"))
	ts := ck(f.svc.CreateSpace(f.ctx, f.alice, "team", "", team.ID))

	x := f.put(t, f.alice, f.home.ID, "/x.txt", "alpha")
	ck(f.svc.AddDocumentVersion(f.ctx, f.alice, x.File.DocumentID, strings.NewReader("beta"), "web"))
	ck(f.svc.RestoreVersion(f.ctx, f.alice, x.File.DocumentID, 1)) // v3 = alpha, restored from 1
	y := f.put(t, f.alice, ts.ID, "/y.txt", "alpha")               // same content, later

	// move x into the team space: merges x's history into y's document
	root := ck(f.svc.FolderByPath(f.ctx, f.alice, ts.ID, nil))
	moved := ck(f.svc.MoveLink(f.ctx, f.alice, x.File.LinkID, root.ID, "", false))
	if moved.DocumentID != y.File.DocumentID {
		t.Fatalf("expected a merge into %s, got %s", y.File.DocumentID, moved.DocumentID)
	}
	d := ck(f.svc.GetDocument(f.ctx, f.alice, y.File.DocumentID))
	if len(d.Versions) != 4 {
		t.Fatalf("merged versions: %+v", d.Versions)
	}
	v3 := versionByNumber(t, d, 3)
	if v3.Source != "restore" || v3.RestoredFrom == nil || *v3.RestoredFrom != 1 {
		t.Fatalf("restore after merge: %+v", v3)
	}
}

// Identical content in several spaces is one blob but one document per
// space; "places" show the other spaces' places too, only where readable.
func TestSameContentPlaces(t *testing.T) {
	f := newFixture(t)
	team := ck(f.svc.CreateTeam(f.ctx, f.alice, "T"))
	ts := ck(f.svc.CreateSpace(f.ctx, f.alice, "shared", "", team.ID))
	bob := ck(f.svc.UserByName(f.ctx, "bob"))
	noErr(t, f.svc.SetMember(f.ctx, f.alice, team.ID, bob.ID, "viewer"))

	mine := f.put(t, f.alice, f.home.ID, "/Privat/rechnung.pdf", "%PDF same invoice")
	shared := f.put(t, f.alice, ts.ID, "/Rechnungen/rechnung.pdf", "%PDF same invoice")
	f.put(t, f.alice, f.home.ID, "/other.pdf", "%PDF something else")
	if mine.File.DocumentID == shared.File.DocumentID || mine.File.SHA256 != shared.File.SHA256 {
		t.Fatal("expected two documents with one content")
	}

	d := ck(f.svc.GetDocument(f.ctx, f.alice, mine.File.DocumentID))
	if len(d.SameContent) != 1 || d.SameContent[0].SpaceID != ts.ID || d.SameContent[0].Path != "/Rechnungen/rechnung.pdf" ||
		d.SameContent[0].DocumentID != shared.File.DocumentID {
		t.Fatalf("alice, personal document: %+v", d.SameContent)
	}
	d = ck(f.svc.GetDocument(f.ctx, f.alice, shared.File.DocumentID))
	if len(d.SameContent) != 1 || d.SameContent[0].SpaceID != f.home.ID {
		t.Fatalf("alice, team document: %+v", d.SameContent)
	}
	// bob reads the team space only: alice's personal place stays hidden
	d = ck(f.svc.GetDocument(f.ctx, f.bob, shared.File.DocumentID))
	if len(d.SameContent) != 0 {
		t.Fatalf("bob must not see places in alice's personal space: %+v", d.SameContent)
	}
	// trashed places are not listed
	noErr(t, f.svc.DeleteLink(f.ctx, f.alice, shared.File.LinkID))
	if d = ck(f.svc.GetDocument(f.ctx, f.alice, mine.File.DocumentID)); len(d.SameContent) != 0 {
		t.Fatalf("trashed place listed: %+v", d.SameContent)
	}
}
