package core_test

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/tildoc/tildoc/internal/blob"
	"github.com/tildoc/tildoc/internal/core"
	"github.com/tildoc/tildoc/internal/testutil"
)

func zipEntries(t *testing.T, data []byte) map[string]string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(rc)
		rc.Close()
		out[f.Name] = string(b)
	}
	return out
}

func TestZipExport(t *testing.T) {
	f := newFixture(t)
	f.put(t, f.alice, f.home.ID, "/Rechnungen/2025/strom.txt", "Strom")
	f.put(t, f.alice, f.home.ID, "/Rechnungen/Übersicht März.txt", "Übersicht")
	gone := f.put(t, f.alice, f.home.ID, "/weg.txt", "weg")
	ck(f.svc.MkdirAll(f.ctx, f.alice, f.home.ID, []string{"Leer"}))
	if err := f.svc.DeleteLink(f.ctx, f.alice, gone.File.LinkID); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := f.svc.ExportZip(f.ctx, f.alice, core.ExportScope{Kind: "space", ID: f.home.ID}, &buf, nil); err != nil {
		t.Fatal(err)
	}
	got := zipEntries(t, buf.Bytes())
	root := f.home.Name + "/"
	for name, want := range map[string]string{
		root + "Rechnungen/2025/strom.txt": "Strom", root + "Rechnungen/Übersicht März.txt": "Übersicht", root + "Leer/": "",
	} {
		if c, ok := got[name]; !ok || c != want {
			t.Errorf("zip entry %q: %q %v (have %v)", name, c, ok, slices.Sorted(func(yield func(string) bool) {
				for k := range got {
					if !yield(k) {
						return
					}
				}
			}))
		}
	}
	if _, ok := got[root+"weg.txt"]; ok {
		t.Fatal("trash must not be exported")
	}
	// folder export and permissions
	dir := ck(f.svc.FolderByPath(f.ctx, f.alice, f.home.ID, []string{"Rechnungen"}))
	buf.Reset()
	if err := f.svc.ExportZip(f.ctx, f.alice, core.ExportScope{Kind: "folder", ID: dir.ID}, &buf, nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := zipEntries(t, buf.Bytes())["Rechnungen/2025/strom.txt"]; !ok {
		t.Fatal("folder export")
	}
	if err := f.svc.ExportZip(f.ctx, f.bob, core.ExportScope{Kind: "space", ID: f.home.ID}, io.Discard, nil); core.KindOf(err) != core.ErrNotFound {
		t.Fatalf("bob exported alice's space: %v", err)
	}
	ck(f.svc.SetGrant(f.ctx, f.alice, f.home.ID, "bob", "", "viewer"))
	if err := f.svc.ExportZip(f.ctx, f.bob, core.ExportScope{Kind: "space", ID: f.home.ID}, io.Discard, nil); err != nil {
		t.Fatalf("viewer export: %v", err)
	}
	// collection: flat, shown names
	b := f.put(t, f.bob, f.bhome.ID, "/strom.txt", "Bob Strom")
	coll := ck(f.svc.CreateCollection(f.ctx, f.bob, "Steuer", "", nil))
	strom := ck(f.svc.FileByPath(f.ctx, f.alice, f.home.ID, []string{"Rechnungen", "2025", "strom.txt"}))
	if err := f.svc.AddToCollection(f.ctx, f.bob, coll.ID, []string{strom.DocumentID, b.File.DocumentID}); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	if err := f.svc.ExportZip(f.ctx, f.bob, core.ExportScope{Kind: "collection", ID: coll.ID}, &buf, nil); err != nil {
		t.Fatal(err)
	}
	c := zipEntries(t, buf.Bytes())
	if len(c) != 3 || c["Steuer/strom.txt"] == "" || c["Steuer/strom (2).txt"] == "" {
		t.Fatalf("collection zip: %v", c)
	}
}

// setupDerived gives a blob extracted text and a derived variant.
func setupDerived(t *testing.T, f *fixture, r *core.PutResult, text string) {
	t.Helper()
	f.index(t, r, text)
	st := ck(f.env.Blobs.Stage(strings.NewReader("%PDF searchable "+text), 0))
	if err := f.svc.StoreDerivative(f.ctx, r.File.SHA256, core.Derivation{Kind: "searchable_pdf", Input: r.File.SHA256, Engine: "test"}, st); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.SetProcessing(f.ctx, r.File.SHA256, core.StatusDone, "pdf-scanned", nil, ""); err != nil {
		t.Fatal(err)
	}
}

func secondInstance(t *testing.T) (*testutil.Env, *core.Principal) {
	t.Helper()
	env := testutil.New(t)
	ck(env.Svc.SetupAdmin(t.Context(), "root", "rootpass1", ""))
	ck(env.Svc.CreateUser(t.Context(), session(t, env.Svc, "root", "rootpass1"), "carol", "carolpass", "", false))
	return env, session(t, env.Svc, "carol", "carolpass")
}

func TestBundleRoundTrip(t *testing.T) {
	f := newFixture(t)
	f.put(t, f.alice, f.home.ID, "/Akten/vertrag.pdf", "%PDF vertrag v1")
	v2 := f.put(t, f.alice, f.home.ID, "/Akten/vertrag.pdf", "%PDF vertrag v2")
	setupDerived(t, f, v2, "Vertrag Zebrastreifen")
	hroot := ck(f.svc.FolderByPath(f.ctx, f.alice, f.home.ID, nil))
	ck(f.svc.AddLink(f.ctx, f.alice, v2.File.DocumentID, hroot.ID, "vertrag-kopie.pdf"))
	f.put(t, f.alice, f.home.ID, "/Akten/Unterordner/notiz.txt", "Notiz")
	ck(f.svc.MkdirAll(f.ctx, f.alice, f.home.ID, []string{"Leer"}))
	tag := ck(f.svc.CreateTag(f.ctx, f.alice, "Verträge", "#1f4e79"))
	if err := f.svc.TagDocument(f.ctx, f.alice, v2.File.DocumentID, tag.ID); err != nil {
		t.Fatal(err)
	}
	coll := ck(f.svc.CreateCollection(f.ctx, f.alice, "Wichtig", "", nil))
	if err := f.svc.AddToCollection(f.ctx, f.alice, coll.ID, []string{v2.File.DocumentID}); err != nil {
		t.Fatal(err)
	}
	trashed := f.put(t, f.alice, f.home.ID, "/alt.txt", "alt")
	if err := f.svc.DeleteLink(f.ctx, f.alice, trashed.File.LinkID); err != nil {
		t.Fatal(err)
	}
	var bundle bytes.Buffer
	if err := f.svc.ExportBundle(f.ctx, f.alice, core.ExportScope{Kind: "space", ID: f.home.ID}, &bundle, nil); err != nil {
		t.Fatal(err)
	}

	// another server, another user: remapped into a new space
	env2, carol := secondInstance(t)
	res := ck(env2.Svc.ImportBundle(f.ctx, carol, bytes.NewReader(bundle.Bytes()), nil, core.ImportOptions{NewSpaceName: "Archiv Alice"}, nil))
	if res.Documents != 2 || res.Places != 3 || res.Collections != 1 || res.BlobsExisting != 0 {
		t.Fatalf("import result: %+v", res)
	}
	sp := ck(env2.Svc.GetSpace(f.ctx, carol, res.SpaceID))
	if sp.Name != "Archiv Alice" || sp.Role != "owner" || sp.ID == f.home.ID {
		t.Fatalf("new space: %+v", sp)
	}
	vf := ck(env2.Svc.FileByPath(f.ctx, carol, sp.ID, []string{"Akten", "vertrag.pdf"}))
	d := ck(env2.Svc.GetDocument(f.ctx, carol, vf.DocumentID))
	if len(d.Versions) != 2 || len(d.Links) != 2 || !d.HasSearchable || !d.HasText || len(d.Tags) != 1 || d.Tags[0].Name != "Verträge" {
		t.Fatalf("imported document: versions %d links %d searchable %v text %v tags %+v", len(d.Versions), len(d.Links), d.HasSearchable, d.HasText, d.Tags)
	}
	if d.Status != "done" {
		t.Fatalf("processing state not taken over: %s", d.Status)
	}
	if ck(env2.Svc.FolderByPath(f.ctx, carol, sp.ID, []string{"Leer"})) == nil {
		t.Fatal("empty folder missing")
	}
	if _, err := env2.Svc.FileByPath(f.ctx, carol, sp.ID, []string{"alt.txt"}); err == nil {
		t.Fatal("trash was imported")
	}
	if h := ck(env2.Svc.Search(f.ctx, carol, core.SearchQuery{Q: "Zebrastreifen"})); h.Total != 2 { // two places
		t.Fatalf("imported text not searchable: %d", h.Total)
	}
	cols := ck(env2.Svc.ListCollections(f.ctx, carol))
	if len(cols) != 1 || cols[0].Name != "Wichtig" || cols[0].Count != 1 {
		t.Fatalf("collections: %+v", cols)
	}
	var jobs int
	env2.Pool.QueryRow(f.ctx, `SELECT count(*) FROM jobs WHERE kind='process_blob' AND payload->>'sha256' = $1`, v2.File.SHA256.Hex()).Scan(&jobs)
	if jobs != 0 {
		t.Fatal("imported results must not be processed again")
	}

	// importing again into the same space is idempotent
	again := ck(env2.Svc.ImportBundle(f.ctx, carol, bytes.NewReader(bundle.Bytes()), nil, core.ImportOptions{TargetFolderID: sp.RootFolderID}, nil))
	if again.Places != 0 || again.Merged != 2 || again.Documents != 0 || again.BlobsNew != 0 {
		t.Fatalf("re-import: %+v", again)
	}
	if d2 := ck(env2.Svc.GetDocument(f.ctx, carol, vf.DocumentID)); len(d2.Versions) != 2 {
		t.Fatalf("re-import duplicated history: %d versions", len(d2.Versions))
	}

	// same server: contents are not stored again
	before := len(blobFiles(t, f))
	local := ck(f.svc.ImportBundle(f.ctx, f.bob, bytes.NewReader(bundle.Bytes()), nil, core.ImportOptions{NewSpaceName: "Kopie"}, nil))
	if local.BlobsNew != 0 || len(blobFiles(t, f)) != before {
		t.Fatalf("same-server import stored content again: %+v", local)
	}

	// collection bundle: one folder named after the collection
	var cb bytes.Buffer
	if err := f.svc.ExportBundle(f.ctx, f.alice, core.ExportScope{Kind: "collection", ID: coll.ID}, &cb, nil); err != nil {
		t.Fatal(err)
	}
	cr := ck(env2.Svc.ImportBundle(f.ctx, carol, &cb, nil, core.ImportOptions{TargetFolderID: sp.RootFolderID}, nil))
	if cr.Collections != 1 || cr.Merged != 1 {
		t.Fatalf("collection import: %+v", cr)
	}
	if _, err := env2.Svc.FileByPath(f.ctx, carol, sp.ID, []string{"Wichtig", "vertrag.pdf"}); err != nil {
		t.Fatalf("collection folder: %v", err)
	}
}

// tarOf builds a tar archive from name/content pairs.
func tarOf(t *testing.T, entries ...[2]string) []byte {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, e := range entries {
		tw.WriteHeader(&tar.Header{Name: e[0], Mode: 0o600, Size: int64(len(e[1]))})
		tw.Write([]byte(e[1]))
	}
	tw.Close()
	return buf.Bytes()
}

func TestHostileBundles(t *testing.T) {
	f := newFixture(t)
	r := f.put(t, f.alice, f.home.ID, "/a.txt", "inhalt")
	var good bytes.Buffer
	if err := f.svc.ExportBundle(f.ctx, f.alice, core.ExportScope{Kind: "space", ID: f.home.ID}, &good, nil); err != nil {
		t.Fatal(err)
	}
	// split the good bundle to tamper with it
	tr := tar.NewReader(bytes.NewReader(good.Bytes()))
	tr.Next()
	manifest, _ := io.ReadAll(tr)
	var m map[string]any
	json.Unmarshal(manifest, &m)
	tamper := func(fn func(m map[string]any)) string {
		var c map[string]any
		json.Unmarshal(manifest, &c)
		fn(c)
		b, _ := json.Marshal(c)
		return string(b)
	}
	sha := r.File.SHA256.Hex()
	docs := func(c map[string]any) []any {
		return c["spaces"].([]any)[0].(map[string]any)["documents"].([]any)
	}
	cases := map[string][]byte{
		"not a tar":          []byte("hello"),
		"manifest not first": tarOf(t, [2]string{"blobs/" + sha, "inhalt"}, [2]string{"manifest.json", string(manifest)}),
		"hash mismatch":      tarOf(t, [2]string{"manifest.json", string(manifest)}, [2]string{"blobs/" + sha, "fälscht"}),
		"missing blob":       tarOf(t, [2]string{"manifest.json", string(manifest)}),
		"extra entry":        tarOf(t, [2]string{"manifest.json", string(manifest)}, [2]string{"blobs/" + sha, "inhalt"}, [2]string{"../../etc/passwd", "x"}),
		"future version":     tarOf(t, [2]string{"manifest.json", tamper(func(c map[string]any) { c["version"] = 99 })}),
		"unknown field":      tarOf(t, [2]string{"manifest.json", tamper(func(c map[string]any) { c["evil"] = true })}),
		"server data":        tarOf(t, [2]string{"manifest.json", tamper(func(c map[string]any) { c["users"] = []any{map[string]any{"id": r.File.DocumentID, "username": "x"}} })}),
		"traversal name": tarOf(t, [2]string{"manifest.json", tamper(func(c map[string]any) {
			docs(c)[0].(map[string]any)["links"].([]any)[0].(map[string]any)["name"] = "../../x"
		})}, [2]string{"blobs/" + sha, "inhalt"}),
		"slash in folder": tarOf(t, [2]string{"manifest.json", tamper(func(c map[string]any) {
			fs := c["spaces"].([]any)[0].(map[string]any)["folders"].([]any)
			fs = append(fs, map[string]any{"id": "01234567-89ab-7def-8123-456789abcdef", "parent_id": fs[0].(map[string]any)["id"], "name": "a/b",
				"created_at": "2026-01-01T00:00:00Z", "created_by": nil})
			c["spaces"].([]any)[0].(map[string]any)["folders"] = fs
		})}, [2]string{"blobs/" + sha, "inhalt"}),
	}
	spaces := len(ck(f.svc.ListSpaces(f.ctx, f.bob)))
	for name, data := range cases {
		if _, err := f.svc.ImportBundle(f.ctx, f.bob, bytes.NewReader(data), nil, core.ImportOptions{NewSpaceName: "X"}, nil); err == nil {
			t.Errorf("%s: accepted", name)
		} else if core.KindOf(err) != core.ErrInvalid {
			t.Errorf("%s: %v", name, err)
		}
	}
	if n := len(ck(f.svc.ListSpaces(f.ctx, f.bob))); n != spaces {
		t.Fatalf("a failed import left a space behind (%d -> %d)", spaces, n)
	}
	// the untampered bundle works
	if _, err := f.svc.ImportBundle(f.ctx, f.bob, bytes.NewReader(good.Bytes()), nil, core.ImportOptions{}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestServerBackupRestore(t *testing.T) {
	f := newFixture(t)
	admin := session(t, f.svc, "admin", "supersecret")
	f.env.Cfg.BackupDir = t.TempDir()
	team := ck(f.svc.CreateTeam(f.ctx, f.alice, "Kanzlei"))
	ts := ck(f.svc.CreateSpace(f.ctx, f.alice, "kanzlei", "Kanzlei", team.ID))
	a := f.put(t, f.alice, ts.ID, "/akte.pdf", "%PDF akte")
	setupDerived(t, f, a, "Akte Hamburg")
	ck(f.svc.SetGrant(f.ctx, f.alice, f.home.ID, "bob", "", "viewer"))
	tag := ck(f.svc.CreateTag(f.ctx, f.alice, "Mandat", ""))
	if err := f.svc.TagDocument(f.ctx, f.alice, a.File.DocumentID, tag.ID); err != nil {
		t.Fatal(err)
	}
	coll := ck(f.svc.CreateCollection(f.ctx, f.alice, "Akten", team.ID, &tag.ID))
	old := f.put(t, f.alice, f.home.ID, "/papierkorb.txt", "weg")
	if err := f.svc.DeleteLink(f.ctx, f.alice, old.File.LinkID); err != nil {
		t.Fatal(err)
	}
	key, secret := ck2(f.svc.CreateAccessKey(f.ctx, f.alice, "laptop", "", false))

	if _, err := f.svc.StartBackup(f.ctx, f.alice, "full"); core.KindOf(err) != core.ErrForbidden {
		t.Fatalf("non-admin started a backup: %v", err)
	}
	if _, err := f.svc.BackupNow(f.ctx, "differential"); core.KindOf(err) != core.ErrInvalid {
		t.Fatalf("differential without full: %v", err)
	}
	full := ck(f.svc.BackupNow(f.ctx, "full"))
	if full.Status != "done" || full.BlobCount < 3 {
		t.Fatalf("full backup: %+v", full)
	}
	late := f.put(t, f.alice, f.home.ID, "/spaeter.txt", "später dazu")
	diff := ck(f.svc.BackupNow(f.ctx, "differential"))
	if diff.Status != "done" || diff.BlobCount != 1 || *diff.BaseID != full.ID {
		t.Fatalf("differential: %+v", diff)
	}
	files := []string{filepath.Join(f.env.Cfg.BackupDir, full.File), filepath.Join(f.env.Cfg.BackupDir, diff.File)}
	sum := ck(core.VerifyBackup(files))
	if sum.Users != 3 || sum.Spaces != 4 || sum.Mode != "differential" {
		t.Fatalf("verify: %+v", sum)
	}
	if _, err := core.VerifyBackup(files[1:]); err == nil {
		t.Fatal("a differential alone must not verify")
	}
	if list := ck(f.svc.ListBackups(f.ctx, admin)); len(list) != 2 {
		t.Fatalf("list: %+v", list)
	}
	if err := f.svc.DeleteBackup(f.ctx, admin, full.ID); core.KindOf(err) != core.ErrConflict {
		t.Fatalf("deleted a base backup: %v", err)
	}

	// restore into an empty instance with the same master key
	env2 := testutil.New(t)
	env2.Cfg.MasterKey = f.env.Cfg.MasterKey
	if _, err := f.svc.RestoreServer(f.ctx, files); core.KindOf(err) != core.ErrConflict {
		t.Fatalf("restore into a used instance: %v", err)
	}
	ck(env2.Svc.RestoreServer(f.ctx, files))
	alice := session(t, env2.Svc, "alice", "alicepass")
	bob := session(t, env2.Svc, "bob", "bobpass12")
	if sp := ck(env2.Svc.GetSpace(f.ctx, alice, ts.ID)); sp.ID != ts.ID || sp.TeamName != "Kanzlei" {
		t.Fatalf("space ids: %+v", sp)
	}
	if fi := ck(env2.Svc.FileByPath(f.ctx, alice, f.home.ID, []string{"spaeter.txt"})); fi.LinkID != late.File.LinkID {
		t.Fatal("differential content missing or ids changed")
	}
	if tr := ck(env2.Svc.ListTrash(f.ctx, alice, f.home.ID)); len(tr) != 1 {
		t.Fatalf("trash not restored: %+v", tr)
	}
	if ck(env2.Svc.GetSpace(f.ctx, bob, f.home.ID)).Role != "viewer" {
		t.Fatal("grant not restored")
	}
	if items := ck(env2.Svc.CollectionItems(f.ctx, alice, coll.ID)); len(items) != 1 {
		t.Fatalf("collection: %+v", items)
	}
	if h := ck(env2.Svc.Search(f.ctx, alice, core.SearchQuery{Q: "Hamburg"})); h.Total != 1 {
		t.Fatal("text not restored")
	}
	if d := ck(env2.Svc.GetDocument(f.ctx, alice, a.File.DocumentID)); !d.HasSearchable || len(d.Tags) != 1 {
		t.Fatalf("document: %+v", d)
	}
	if sec, _, err := env2.Svc.AccessKeySecret(f.ctx, key.ID); err != nil || sec != secret {
		t.Fatalf("access key: %v", err)
	}

	// retention: keep one full backup -> the old full and its differential go
	keep := 1
	ck(f.svc.UpdateSettings(f.ctx, admin, core.SettingsUpdate{BackupKeep: &keep}))
	ck(f.svc.BackupNow(f.ctx, "full"))
	if list := ck(f.svc.ListBackups(f.ctx, admin)); len(list) != 1 {
		t.Fatalf("retention: %+v", list)
	}
	for _, fl := range files {
		if _, err := os.Stat(fl); !os.IsNotExist(err) {
			t.Fatalf("%s still exists", fl)
		}
	}
}

func TestImportTransfers(t *testing.T) {
	f := newFixture(t)
	f.put(t, f.alice, f.home.ID, "/x/brief.txt", "Brief")
	var zbuf bytes.Buffer
	if err := f.svc.ExportZip(f.ctx, f.alice, core.ExportScope{Kind: "space", ID: f.home.ID}, &zbuf, nil); err != nil {
		t.Fatal(err)
	}
	broot := ck(f.svc.FolderByPath(f.ctx, f.bob, f.bhome.ID, nil))
	tr := ck(f.svc.CreateTransfer(f.ctx, f.bob, "import_zip", bytes.NewReader(zbuf.Bytes()), core.ImportOptions{TargetFolderID: broot.ID}))
	if err := f.svc.RunTransfer(f.ctx, tr.ID); err != nil {
		t.Fatal(err)
	}
	done := ck(f.svc.GetTransfer(f.ctx, f.bob, tr.ID))
	var zr core.ZipImportResult
	json.Unmarshal(done.Result, &zr)
	if done.Status != "done" || zr.Files != 1 {
		t.Fatalf("zip transfer: %+v %s", done, done.Result)
	}
	if r := ck(f.svc.FileByPath(f.ctx, f.bob, f.bhome.ID, []string{f.home.Name, "x", "brief.txt"})); r.LinkCount != 1 {
		t.Fatal("zip content missing")
	}
	// a broken file fails the transfer with a readable error
	bad := ck(f.svc.CreateTransfer(f.ctx, f.bob, "import_bundle", strings.NewReader("garbage"), core.ImportOptions{}))
	if err := f.svc.RunTransfer(f.ctx, bad.ID); err != nil {
		t.Fatal(err)
	}
	if b := ck(f.svc.GetTransfer(f.ctx, f.bob, bad.ID)); b.Status != "failed" || !strings.Contains(b.Error, "not a tildoc bundle") {
		t.Fatalf("failed transfer: %+v", b)
	}
	if _, err := f.svc.GetTransfer(f.ctx, f.alice, tr.ID); err == nil {
		t.Fatal("alice sees bob's transfer")
	}
	if entries, _ := os.ReadDir(filepath.Join(f.env.DataDir, "transfers")); len(entries) != 0 {
		t.Fatalf("staged files left behind: %d", len(entries))
	}
	_ = blob.SHA256{}
}
