package api_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/tildoc/tildoc/internal/api"
	"github.com/tildoc/tildoc/internal/testutil"
)

type client struct {
	t    *testing.T
	base string
	http *http.Client
	csrf bool
}

func newServer(t *testing.T) (*httptest.Server, *testutil.Env) {
	env := testutil.New(t)
	srv := api.New(env.Svc, env.Cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, env
}

func newClient(t *testing.T, base string) *client {
	jar, _ := cookiejar.New(nil)
	return &client{t: t, base: base, http: &http.Client{Jar: jar}, csrf: true}
}

func (c *client) do(method, path string, body any, out any) int {
	c.t.Helper()
	var rd io.Reader
	ctype := ""
	switch b := body.(type) {
	case nil:
	case []byte:
		rd = bytes.NewReader(b)
		ctype = "application/octet-stream"
	case string:
		rd = strings.NewReader(b)
		ctype = "application/octet-stream"
	default:
		j, _ := json.Marshal(b)
		rd = bytes.NewReader(j)
		ctype = "application/json"
	}
	req, _ := http.NewRequest(method, c.base+path, rd)
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	if c.csrf {
		req.Header.Set(api.CSRFHeader, "1")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if out != nil {
		if s, ok := out.(*string); ok {
			*s = string(data)
		} else if len(data) > 0 && resp.StatusCode < 300 {
			if err := json.Unmarshal(data, out); err != nil {
				c.t.Fatalf("%s %s: decode %q: %v", method, path, data, err)
			}
		}
	}
	if resp.StatusCode >= 500 {
		c.t.Fatalf("%s %s: %d %s", method, path, resp.StatusCode, data)
	}
	return resp.StatusCode
}

func (c *client) ok(method, path string, body any, out any) {
	c.t.Helper()
	if code := c.do(method, path, body, out); code >= 300 {
		var s string
		c.do(method, path, body, &s)
		c.t.Fatalf("%s %s: status %d (%s)", method, path, code, s)
	}
}

func expect(t *testing.T, got, want int, what string) {
	t.Helper()
	if got != want {
		t.Fatalf("%s: status %d, want %d", what, got, want)
	}
}

type space struct {
	ID, Slug     string
	RootFolderID string `json:"root_folder_id"`
}

type fileInfo struct {
	LinkID       string `json:"link_id"`
	DocumentID   string `json:"document_id"`
	FolderID     string `json:"folder_id"`
	Name, Path   string
	Size         int64
	Version      int
	LinkCount    int    `json:"link_count"`
	OriginalPath string `json:"original_path"`
	ImportID     string `json:"import_id"`
}

type upload struct {
	File         fileInfo
	NewLink      bool `json:"new_link"`
	Deduplicated bool
	NewVersion   bool `json:"new_version"`
	Forked       bool
}

func TestAPIFlow(t *testing.T) {
	ts, _ := newServer(t)
	admin := newClient(t, ts.URL)

	var st map[string]bool
	admin.ok("GET", "/api/v1/setup", nil, &st)
	if !st["needs_setup"] {
		t.Fatal("expected needs_setup")
	}
	admin.ok("POST", "/api/v1/setup", map[string]string{"username": "admin", "password": "supersecret"}, nil)
	expect(t, admin.do("POST", "/api/v1/setup", map[string]string{"username": "x", "password": "supersecret"}, nil), 403, "second setup")
	admin.ok("POST", "/api/v1/users", map[string]any{"username": "alice", "password": "alicepass"}, nil)

	anon := newClient(t, ts.URL)
	expect(t, anon.do("GET", "/api/v1/me", nil, nil), 401, "anonymous")
	expect(t, anon.do("POST", "/api/v1/auth/login", map[string]string{"username": "alice", "password": "nope"}, nil), 401, "bad login")

	alice := newClient(t, ts.URL)
	alice.ok("POST", "/api/v1/auth/login", map[string]string{"username": "alice", "password": "alicepass"}, nil)

	// CSRF header required for cookie authenticated writes
	alice.csrf = false
	expect(t, alice.do("POST", "/api/v1/teams", map[string]string{"name": "T"}, nil), 403, "csrf")
	alice.csrf = true

	var spaces []space
	alice.ok("GET", "/api/v1/spaces", nil, &spaces)
	if len(spaces) != 1 {
		t.Fatalf("spaces %+v", spaces)
	}
	root := spaces[0].RootFolderID

	// upload with relative paths (folder drop)
	var imp struct{ ID string }
	alice.ok("POST", "/api/v1/imports", map[string]string{"space_id": spaces[0].ID, "client_root": "Scans"}, &imp)
	var u1, u2, u3 upload
	alice.ok("PUT", "/api/v1/folders/"+root+"/files?path="+url.QueryEscape("Scans/2024/a.txt")+"&import="+imp.ID, "alpha bravo charlie", &u1)
	alice.ok("PUT", "/api/v1/folders/"+root+"/files?path="+url.QueryEscape("Scans/b.txt")+"&import="+imp.ID, "alpha bravo charlie", &u2)
	if !u1.NewLink || !u2.Deduplicated || u2.File.DocumentID != u1.File.DocumentID || u1.File.ImportID != imp.ID {
		t.Fatalf("uploads: %+v %+v", u1, u2)
	}
	alice.ok("PUT", "/api/v1/folders/"+root+"/files?path=Scans/b.txt", "changed", &u3)
	if !u3.Forked || !u3.NewVersion {
		t.Fatalf("fork via upload: %+v", u3)
	}

	// listing + breadcrumbs
	var listing struct {
		Folders     []struct{ ID, Name string }
		Files       []fileInfo
		Breadcrumbs []struct{ Name string }
	}
	alice.ok("GET", "/api/v1/folders/"+u1.File.FolderID, nil, &listing)
	if len(listing.Files) != 1 || len(listing.Breadcrumbs) != 3 {
		t.Fatalf("listing: %+v", listing)
	}

	// download: attachment + sandbox CSP; inline text rendered as text/plain
	resp, _ := alice.http.Get(ts.URL + "/api/v1/files/" + u1.File.LinkID + "/content")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "alpha bravo charlie" || !strings.HasPrefix(resp.Header.Get("Content-Disposition"), "attachment") ||
		!strings.Contains(resp.Header.Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("download: %q %v", body, resp.Header)
	}
	req, _ := http.NewRequest("GET", ts.URL+"/api/v1/files/"+u1.File.LinkID+"/content", nil)
	req.Header.Set("Range", "bytes=6-10")
	resp, _ = alice.http.Do(req)
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 206 || string(body) != "bravo" {
		t.Fatalf("range: %d %q", resp.StatusCode, body)
	}

	// document details show all links
	var doc struct {
		Links    []struct{ Path string }
		Versions []struct{ Version int }
	}
	alice.ok("GET", "/api/v1/documents/"+u1.File.DocumentID, nil, &doc)
	if len(doc.Links) != 1 || doc.Links[0].Path != "/Scans/2024/a.txt" {
		t.Fatalf("doc links: %+v", doc.Links)
	}

	// move + restore original structure (bulk by import)
	var f fileInfo
	alice.ok("PATCH", "/api/v1/files/"+u1.File.LinkID, map[string]string{"folder_id": root, "name": "renamed.txt"}, &f)
	if f.Path != "/renamed.txt" {
		t.Fatalf("move: %+v", f)
	}
	var restored []fileInfo
	alice.ok("POST", "/api/v1/bulk/restore-original", map[string]string{"import_id": imp.ID}, &restored)
	found := false
	for _, r := range restored {
		if r.LinkID == u1.File.LinkID && r.Path == "/Scans/2024/a.txt" {
			found = true
		}
	}
	if !found {
		t.Fatalf("restore original: %+v", restored)
	}

	// search by name and content (no text extracted yet -> name match)
	var sr struct {
		Total int
		Hits  []fileInfo
	}
	alice.ok("GET", "/api/v1/search?q=a.txt", nil, &sr)
	if sr.Total < 1 {
		t.Fatalf("search: %+v", sr)
	}

	// trash
	expect(t, alice.do("DELETE", "/api/v1/files/"+u1.File.LinkID, nil, nil), 204, "delete")
	var trash []struct {
		DocumentID string `json:"document_id"`
	}
	alice.ok("GET", "/api/v1/trash", nil, &trash)
	if len(trash) != 1 {
		t.Fatalf("trash: %+v", trash)
	}
	alice.ok("POST", "/api/v1/trash/"+trash[0].DocumentID+"/restore", nil, nil)

	// access keys: secret shown once
	var key struct {
		Key    struct{ ID string }
		Secret string
	}
	alice.ok("POST", "/api/v1/me/access-keys", map[string]any{"label": "laptop"}, &key)
	if len(key.Secret) != 40 {
		t.Fatalf("secret: %+v", key)
	}
	var keys []map[string]any
	alice.ok("GET", "/api/v1/me/access-keys", nil, &keys)
	if len(keys) != 1 || keys[0]["secret"] != nil {
		t.Fatalf("keys leak secret: %+v", keys)
	}

	// teams via API
	var team struct{ ID string }
	alice.ok("POST", "/api/v1/teams", map[string]string{"name": "Finance"}, &team)
	alice.ok("PUT", "/api/v1/teams/"+team.ID+"/members/admin", map[string]string{"role": "member"}, nil)
	var ts2 space
	alice.ok("POST", "/api/v1/spaces", map[string]string{"slug": "finance", "name": "Finance", "team_id": team.ID}, &ts2)
	alice.ok("POST", "/api/v1/spaces/"+ts2.ID+"/grants", map[string]string{"team_id": team.ID, "role": "viewer"}, nil)
	expect(t, admin.do("PUT", "/api/v1/folders/"+ts2.RootFolderID+"/files?path=x.txt", "x", nil), 403, "viewer upload")

	// admin endpoints
	expect(t, alice.do("GET", "/api/v1/admin/stats", nil, nil), 403, "non-admin stats")
	var stats map[string]any
	admin.ok("GET", "/api/v1/admin/stats", nil, &stats)
	if stats["documents"].(float64) < 2 {
		t.Fatalf("stats: %+v", stats)
	}

	// unknown endpoint JSON 404
	expect(t, alice.do("GET", "/api/v1/nope", nil, nil), 404, "unknown")
	// logout invalidates
	alice.ok("POST", "/api/v1/auth/logout", nil, nil)
	expect(t, alice.do("GET", "/api/v1/me", nil, nil), 401, "after logout")
}

func TestLoginRateLimit(t *testing.T) {
	ts, _ := newServer(t)
	c := newClient(t, ts.URL)
	c.ok("POST", "/api/v1/setup", map[string]string{"username": "admin", "password": "supersecret"}, nil)
	anon := newClient(t, ts.URL)
	for i := 0; i < 10; i++ {
		anon.do("POST", "/api/v1/auth/login", map[string]string{"username": "admin", "password": fmt.Sprint("wrong", i)}, nil)
	}
	expect(t, anon.do("POST", "/api/v1/auth/login", map[string]string{"username": "admin", "password": "supersecret"}, nil), 403, "rate limited")
}

func TestPrefsAndPDFSandbox(t *testing.T) {
	ts, _ := newServer(t)
	c := newClient(t, ts.URL)
	c.ok("POST", "/api/v1/setup", map[string]string{"username": "admin", "password": "supersecret"}, nil)
	var spaces []space
	c.ok("GET", "/api/v1/spaces", nil, &spaces)
	var up upload
	c.ok("PUT", "/api/v1/folders/"+spaces[0].RootFolderID+"/files?path=a.pdf", "%PDF-1.4\n%%EOF\n", &up)

	csp := func(q string) string {
		t.Helper()
		resp, err := c.http.Get(ts.URL + "/api/v1/files/" + up.File.LinkID + "/content" + q)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.Header.Get("Content-Security-Policy")
	}
	if !strings.Contains(csp("?inline=1"), "sandbox") {
		t.Fatal("inline PDF must be sandboxed")
	}
	if !strings.Contains(csp("?inline=1&viewer=native"), "sandbox") {
		t.Fatal("native viewer without preference must stay sandboxed")
	}

	var me struct {
		Username string
		Prefs    struct {
			PDFViewer string `json:"pdf_viewer"`
		}
	}
	c.ok("GET", "/api/v1/me", nil, &me)
	if me.Username != "admin" || me.Prefs.PDFViewer != "pdfjs" {
		t.Fatalf("me %+v", me)
	}
	expect(t, c.do("PATCH", "/api/v1/me", map[string]any{"prefs": map[string]string{"pdf_viewer": "acrobat"}}, nil), 400, "invalid viewer")
	c.ok("PATCH", "/api/v1/me", map[string]any{"prefs": map[string]string{"pdf_viewer": "native"}}, &me)
	if me.Prefs.PDFViewer != "native" {
		t.Fatalf("patched %+v", me)
	}
	if got := csp("?inline=1&viewer=native"); got != "" {
		t.Fatalf("native viewer with preference: CSP %q", got)
	}
	if !strings.Contains(csp("?inline=1"), "sandbox") || !strings.Contains(csp(""), "sandbox") {
		t.Fatal("other modes stay sandboxed")
	}
}
