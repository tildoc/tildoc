package api_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tildoc/tildoc/internal/api"
	"github.com/tildoc/tildoc/internal/blob"
	"github.com/tildoc/tildoc/internal/config"
	"github.com/tildoc/tildoc/internal/wopi/wopitest"
)

type officeEnv struct {
	t    *testing.T
	ts   *httptest.Server
	srv  *api.Server
	stub *wopitest.Stub
}

func newOffice(t *testing.T) *officeEnv {
	t.Helper()
	stub, err := wopitest.New()
	if err != nil {
		t.Fatal(err)
	}
	cs := httptest.NewServer(stub.Handler())
	t.Cleanup(cs.Close)
	ts, srv := liveServerWith(t, "", func(c *config.Config) {
		c.OfficeURL, c.OfficeInternalURL = cs.URL, cs.URL
	})
	srv.Cfg.WOPIURL = ts.URL
	return &officeEnv{t, ts, srv, stub}
}

func (e *officeEnv) open(c *client, docID, lock string) *wopitest.Session {
	e.t.Helper()
	var o struct {
		URL         string `json:"url"`
		AccessToken string `json:"access_token"`
		TTL         int64  `json:"access_token_ttl"`
	}
	c.ok("POST", "/api/documents/"+docID+"/office", map[string]any{}, &o)
	if !strings.Contains(o.URL, "/browser/stub/cool.html?WOPISrc=") || strings.Contains(o.URL, "ignored.example") || o.TTL == 0 {
		e.t.Fatalf("office url %+v", o)
	}
	return &wopitest.Session{Stub: e.stub, WOPISrc: e.ts.URL + "/wopi/files/" + docID, Token: o.AccessToken, Lock: lock}
}

type officeDoc struct {
	Version  int
	SHA256   string
	Versions []struct {
		Version int
		Source  string
	}
	OfficeSession *struct {
		SavedAt *string `json:"saved_at"`
	} `json:"office_session"`
}

func upDocx(t *testing.T, c *client, sp space, name string) string {
	t.Helper()
	var up upload
	c.ok("PUT", "/api/folders/"+sp.RootFolderID+"/files?path="+name, wopitest.Doc(name), &up) // the name: identical content would be deduplicated
	return up.File.DocumentID
}

func wantStatus(t *testing.T, got int, err error, want int, what string) {
	t.Helper()
	if err != nil || got != want {
		t.Fatalf("%s: status %d err %v, want %d", what, got, err, want)
	}
}

func sessionOnly(t *testing.T, c *client, id string, version int, what string) officeDoc {
	t.Helper()
	var d officeDoc
	c.ok("GET", "/api/documents/"+id, nil, &d)
	if d.Version != version {
		t.Fatalf("%s: version %d, want %d (%+v)", what, d.Version, version, d.Versions)
	}
	return d
}

func TestOfficeOneVersionPerSession(t *testing.T) {
	e := newOffice(t)
	alice, sp := aliceClient(t, e.ts.URL)
	id := upDocx(t, alice, sp, "report.docx")

	var exts struct{ Extensions, Editable, Create []string }
	alice.ok("GET", "/api/office", nil, &exts)
	if strings.Join(exts.Extensions, ",") != "docx,odt,pdf,txt" || strings.Join(exts.Create, ",") != "docx,pdf" {
		t.Fatalf("office info %+v", exts)
	}

	s := e.open(alice, id, "lock-1")
	var info map[string]any
	st, err := s.Info(&info)
	wantStatus(t, st, err, 200, "CheckFileInfo")
	if info["BaseFileName"] != "report.docx" || info["UserCanWrite"] != true || info["SupportsLocks"] != true || info["UserCanNotWriteRelative"] != true {
		t.Fatalf("info %v", info)
	}
	st, body, _ := s.Get()
	if st != 200 || string(body) != string(wopitest.Doc("report.docx")) {
		t.Fatalf("GetFile %d %q", st, body)
	}
	st, _, err = s.Call("LOCK", nil)
	wantStatus(t, st, err, 200, "LOCK")
	// another lock is refused and told the current one
	other := e.open(alice, id, "lock-2")
	st, cur, err := other.Call("LOCK", nil)
	wantStatus(t, st, err, 409, "second LOCK")
	if cur != "lock-1" {
		t.Fatalf("conflict names %q", cur)
	}
	st, cur, _ = s.Call("GET_LOCK", nil)
	if st != 200 || cur != "lock-1" {
		t.Fatalf("GET_LOCK %d %q", st, cur)
	}

	// autosaves and Ctrl+S: working copy only
	st, _, err = s.Put(wopitest.Doc("one"), false)
	wantStatus(t, st, err, 200, "autosave")
	first := sha(wopitest.Doc("one"))
	if !e.srv.Svc.Blobs.Exists(first) {
		t.Fatal("working copy not stored")
	}
	st, _, err = s.Put(wopitest.Doc("two"), false)
	wantStatus(t, st, err, 200, "autosave 2")
	d := sessionOnly(t, alice, id, 1, "during the session")
	if d.OfficeSession == nil || d.OfficeSession.SavedAt == nil {
		t.Fatalf("no session in the document: %+v", d)
	}
	if e.srv.Svc.Blobs.Exists(first) {
		t.Fatal("intermediate save kept")
	}
	// someone joining reads the state of the session, the normal channel doesn't
	st, body, _ = other.Get()
	if st != 200 || string(body) != string(wopitest.Doc("two")) {
		t.Fatalf("joining editor reads %q", body)
	}
	var content string
	alice.ok("GET", "/api/documents/"+id+"/content", nil, &content)
	if content != string(wopitest.Doc("report.docx")) {
		t.Fatalf("content during the session: %q", content)
	}
	st, _, _ = other.Put(wopitest.Doc("x"), false)
	wantStatus(t, st, nil, 409, "save with the wrong lock")

	// exit save: one version
	st, _, err = s.Put(wopitest.Doc("final"), true)
	wantStatus(t, st, err, 200, "exit save")
	d = sessionOnly(t, alice, id, 2, "after the session")
	if d.Versions[0].Source != "office" || d.OfficeSession != nil {
		t.Fatalf("after exit %+v", d)
	}
	// the UNLOCK that follows finds nothing to do
	st, _, err = s.Call("UNLOCK", nil)
	wantStatus(t, st, err, 200, "UNLOCK after exit save")
	sessionOnly(t, alice, id, 2, "after UNLOCK")
}

func sha(b []byte) blob.SHA256 { return blob.SHA256(sha256.Sum256(b)) }

func TestOfficeSessionEnd(t *testing.T) {
	e := newOffice(t)
	alice, sp := aliceClient(t, e.ts.URL)
	ctx := context.Background()

	// UNLOCK without exit save (the last autosave held everything)
	id := upDocx(t, alice, sp, "a.docx")
	s := e.open(alice, id, "l")
	s.Call("LOCK", nil)
	s.Put(wopitest.Doc("autosaved"), false)
	st, _, err := s.Call("UNLOCK", nil)
	wantStatus(t, st, err, 200, "UNLOCK")
	if d := sessionOnly(t, alice, id, 2, "UNLOCK promotes"); d.Versions[0].Source != "office" {
		t.Fatalf("%+v", d)
	}

	// ONLYOFFICE marks the exit save with the older header prefix
	id = upDocx(t, alice, sp, "oo.docx")
	s = e.open(alice, id, "l")
	s.HeaderPrefix = "X-LOOL-WOPI-"
	s.Call("LOCK", nil)
	s.Put(wopitest.Doc("autosave"), false)
	sessionOnly(t, alice, id, 1, "ONLYOFFICE autosave")
	st, _, err = s.Put(wopitest.Doc("closing"), true)
	wantStatus(t, st, err, 200, "ONLYOFFICE exit save")
	sessionOnly(t, alice, id, 2, "ONLYOFFICE exit save")

	// nothing changed: no version
	id = upDocx(t, alice, sp, "b.docx")
	s = e.open(alice, id, "l")
	s.Call("LOCK", nil)
	s.Put(wopitest.Doc("b.docx"), false)
	s.Put(wopitest.Doc("b.docx"), true)
	sessionOnly(t, alice, id, 1, "unchanged")
	s = e.open(alice, id, "l")
	s.Call("LOCK", nil)
	s.Call("UNLOCK", nil)
	sessionOnly(t, alice, id, 1, "no save at all")

	// crash: the lock runs out, housekeeping promotes
	id = upDocx(t, alice, sp, "c.docx")
	s = e.open(alice, id, "l")
	s.Call("LOCK", nil)
	s.Put(wopitest.Doc("before the crash"), false)
	if n, err := e.srv.Svc.ExpireOfficeSessions(ctx); err != nil || n != 0 {
		t.Fatalf("live session expired: %d %v", n, err)
	}
	if _, err := e.srv.Svc.DB.Exec(ctx, `UPDATE office_sessions SET expires_at = now() - interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	if n, err := e.srv.Svc.ExpireOfficeSessions(ctx); err != nil || n != 1 {
		t.Fatalf("expire: %d %v", n, err)
	}
	sessionOnly(t, alice, id, 2, "expiry promotes")

	// a save after the lock expired and was promoted starts over instead of failing
	st, _, err = s.Put(wopitest.Doc("late save"), false)
	wantStatus(t, st, err, 200, "late save")
	s.Call("UNLOCK", nil)
	sessionOnly(t, alice, id, 3, "late save promoted")

	// a new lock over an expired session promotes that session first
	id = upDocx(t, alice, sp, "d.docx")
	s = e.open(alice, id, "old")
	s.Call("LOCK", nil)
	s.Put(wopitest.Doc("abandoned"), false)
	e.srv.Svc.DB.Exec(ctx, `UPDATE office_sessions SET expires_at = now() - interval '1 second'`)
	n := e.open(alice, id, "new")
	st, _, err = n.Call("LOCK", nil)
	wantStatus(t, st, err, 200, "LOCK over expired")
	sessionOnly(t, alice, id, 2, "takeover promotes")

	// no lock at all: refused
	id = upDocx(t, alice, sp, "e.docx")
	s = e.open(alice, id, "")
	st, _, err = s.Put(wopitest.Doc("x"), false)
	wantStatus(t, st, err, 409, "save without a lock")
}

func TestOfficeSecurity(t *testing.T) {
	e := newOffice(t)
	alice, sp := aliceClient(t, e.ts.URL)
	ctx := context.Background()
	id := upDocx(t, alice, sp, "secret.docx")
	id2 := upDocx(t, alice, sp, "other.docx")
	s := e.open(alice, id, "l")
	if st, _ := s.Info(nil); st != 200 {
		t.Fatalf("baseline %d", st)
	}

	// proof signature
	bad := *s
	bad.BadProof = true
	if st, _ := bad.Info(nil); st != 401 {
		t.Fatalf("bad proof %d", st)
	}
	bad = *s
	bad.NoProof = true
	if st, _ := bad.Info(nil); st != 401 {
		t.Fatalf("no proof %d", st)
	}
	// a token opens its one document
	other := *s
	other.WOPISrc = e.ts.URL + "/wopi/files/" + id2
	if st, _ := other.Info(nil); st != 401 {
		t.Fatalf("token on another document %d", st)
	}
	bad = *s
	bad.Token = "guess"
	if st, _ := bad.Info(nil); st != 401 {
		t.Fatalf("wrong token %d", st)
	}

	// a reader: read-only, no lock, no save
	admin := newClient(t, e.ts.URL)
	admin.ok("POST", "/api/auth/login", map[string]string{"username": "admin", "password": "supersecret"}, nil)
	admin.ok("POST", "/api/users", map[string]any{"username": "bob", "password": "bobspassword"}, nil)
	var grant struct{ ID string }
	alice.ok("POST", "/api/spaces/"+sp.ID+"/grants", map[string]any{"user": "bob", "role": "viewer"}, &grant)
	bob := newClient(t, e.ts.URL)
	bob.ok("POST", "/api/auth/login", map[string]string{"username": "bob", "password": "bobspassword"}, nil)
	r := e.open(bob, id, "b")
	var info map[string]any
	st, err := r.Info(&info)
	wantStatus(t, st, err, 200, "reader info")
	if info["UserCanWrite"] != false {
		t.Fatalf("reader may write: %v", info)
	}
	st, _, _ = r.Call("LOCK", nil)
	wantStatus(t, st, nil, 403, "reader LOCK")
	s.Call("LOCK", nil)
	r.Lock = "l"
	st, _, _ = r.Put(wopitest.Doc("from bob"), false)
	wantStatus(t, st, nil, 403, "reader save")

	// revoking applies at once
	expectGone := func(what string) {
		t.Helper()
		if st, _ := r.Info(nil); st != 404 && st != 403 {
			t.Fatalf("%s: %d", what, st)
		}
	}
	alice.ok("DELETE", "/api/spaces/"+sp.ID+"/grants/"+grant.ID, nil, nil)
	expectGone("revoked")

	// no access: no token either
	expect(t, bob.do("POST", "/api/documents/"+id+"/office", map[string]any{}, nil), 404, "stranger")
	// logout ends the token
	bob.ok("POST", "/api/auth/logout", map[string]any{}, nil)
	if st, _ := r.Info(nil); st != 401 {
		t.Fatalf("after logout %d", st)
	}

	// text and PDFs are not Collabora's
	var up upload
	alice.ok("PUT", "/api/folders/"+sp.RootFolderID+"/files?path=note.txt", "plain", &up)
	expect(t, alice.do("POST", "/api/documents/"+up.File.DocumentID+"/office", map[string]any{}, nil), 400, "text")

	// trash ends access; purging takes the working copy along
	s.Put(wopitest.Doc("draft only in the working copy"), false)
	work := sha(wopitest.Doc("draft only in the working copy"))
	var f struct{ Links []struct{ ID string } }
	alice.ok("GET", "/api/documents/"+id, nil, &f)
	expect(t, alice.do("DELETE", "/api/files/"+f.Links[0].ID, nil, nil), 204, "trash")
	if st, _ := s.Info(nil); st != 404 {
		t.Fatalf("trashed %d", st)
	}
	st, _, _ = s.Put(wopitest.Doc("after trash"), false)
	wantStatus(t, st, nil, 404, "save after trash")
	if !e.srv.Svc.Blobs.Exists(work) {
		t.Fatal("working copy gone while the session exists")
	}
	// GC must leave a live working copy alone
	if _, ok, err := e.srv.Svc.GCBlobs(ctx, nil); err != nil || !ok {
		t.Fatalf("gc %v %v", ok, err)
	}
	if !e.srv.Svc.Blobs.Exists(work) {
		t.Fatal("GC removed the working copy")
	}
	expect(t, alice.do("DELETE", "/api/trash/"+id, nil, nil), 204, "purge")
	if e.srv.Svc.Blobs.Exists(work) {
		t.Fatal("purge left the working copy: " + hex.EncodeToString(work[:]))
	}
}

func TestOfficeDisabled(t *testing.T) {
	ts, _ := liveServer(t, "")
	alice, sp := aliceClient(t, ts.URL)
	id := upDocx(t, alice, sp, "report.docx")
	var exts struct{ Extensions []string }
	alice.ok("GET", "/api/office", nil, &exts)
	if len(exts.Extensions) != 0 {
		t.Fatalf("extensions %v", exts.Extensions)
	}
	expect(t, alice.do("POST", "/api/documents/"+id+"/office", map[string]any{}, nil), 404, "open")
	resp, err := http.Get(ts.URL + "/wopi/files/" + id + "?access_token=x")
	if err != nil || resp.StatusCode != 404 {
		t.Fatalf("wopi %v %v", resp, err)
	}
}

// The page may only frame and post to this deployment's Collabora.
func TestOfficeFramePolicy(t *testing.T) {
	ui := t.TempDir()
	if err := os.WriteFile(filepath.Join(ui, "index.html"), []byte("<!doctype html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	ts, _ := liveServerWith(t, ui, func(c *config.Config) {
		c.OfficeURL, c.OfficeInternalURL = "https://office.example", "https://office.example"
	})
	resp, err := http.Get(ts.URL + "/view/x")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if got := resp.Header.Get("Content-Security-Policy"); got != "frame-src 'self' https://office.example; form-action 'self' https://office.example" {
		t.Fatalf("policy %q", got)
	}
}

func TestNewFile(t *testing.T) {
	e := newOffice(t)
	alice, sp := aliceClient(t, e.ts.URL)
	type info struct {
		DocumentID string `json:"document_id"`
		LinkID     string `json:"link_id"`
		Name, MIME string
		Size       int64
		Version    int
	}
	folder := "/api/folders/" + sp.RootFolderID + "/new-file"
	var a, b, doc info
	alice.ok("POST", folder, map[string]string{"name": "a.docx"}, &a)
	alice.ok("POST", folder, map[string]string{"name": "b.docx"}, &b)
	if a.DocumentID == b.DocumentID || a.Size != 0 || a.Version != 1 {
		t.Fatalf("new files share a document or aren't empty: %+v %+v", a, b)
	}
	if !strings.Contains(a.MIME, "wordprocessingml") {
		t.Fatalf("mime %q", a.MIME)
	}
	expect(t, alice.do("POST", folder, map[string]string{"name": "a.docx"}, nil), 409, "same name")
	expect(t, alice.do("POST", folder, map[string]string{"name": "../x"}, nil), 400, "bad name")

	// the office server fills it; the first save makes version 2
	s := e.open(alice, a.DocumentID, "l")
	s.Call("LOCK", nil)
	st, body, _ := s.Get()
	if st != 200 || len(body) != 0 {
		t.Fatalf("empty file: %d %q", st, body)
	}
	s.Put(wopitest.Doc("from the template"), true)
	sessionOnly(t, alice, a.DocumentID, 2, "first save")
	sessionOnly(t, alice, b.DocumentID, 1, "the other new file")

	// a text file with any ending can be edited at once
	alice.ok("POST", folder, map[string]string{"name": "notes.whatever"}, &doc)
	const c = "33333333-3333-4333-8333-333333333333"
	alice.ok("POST", "/api/documents/"+doc.DocumentID+"/edit-lease", map[string]any{"client_id": c}, nil)
	alice.ok("PUT", "/api/documents/"+doc.DocumentID+"/text?client="+c, "hello\n", nil)
	sessionOnly(t, alice, doc.DocumentID, 2, "text saved")

	// a PDF can open in the office server, a reader cannot create
	var up upload
	alice.ok("PUT", "/api/folders/"+sp.RootFolderID+"/files?path=doc.pdf", "%PDF-1.7 x", &up)
	e.open(alice, up.File.DocumentID, "p")
	admin := newClient(t, e.ts.URL)
	admin.ok("POST", "/api/auth/login", map[string]string{"username": "admin", "password": "supersecret"}, nil)
	admin.ok("POST", "/api/users", map[string]any{"username": "rita", "password": "ritaspassword"}, nil)
	alice.ok("POST", "/api/spaces/"+sp.ID+"/grants", map[string]any{"user": "rita", "role": "viewer"}, nil)
	rita := newClient(t, e.ts.URL)
	rita.ok("POST", "/api/auth/login", map[string]string{"username": "rita", "password": "ritaspassword"}, nil)
	expect(t, rita.do("POST", folder, map[string]string{"name": "r.docx"}, nil), 403, "reader")
}
