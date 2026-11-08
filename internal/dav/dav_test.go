package dav_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tildoc/tildoc/internal/core"
	"github.com/tildoc/tildoc/internal/dav"
	"github.com/tildoc/tildoc/internal/testutil"
	"github.com/tildoc/tildoc/internal/vfs"
)

type dc struct {
	t          *testing.T
	base       string
	user, pass string
}

func (c *dc) do(method, path string, body string, hdr map[string]string) (int, string, http.Header) {
	c.t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	r, _ := http.NewRequest(method, c.base+dav.Prefix+path, rd)
	r.SetBasicAuth(c.user, c.pass)
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), resp.Header
}

func (c *dc) want(method, path, body string, hdr map[string]string, status int) string {
	c.t.Helper()
	got, b, _ := c.do(method, path, body, hdr)
	if got != status {
		c.t.Fatalf("%s %s: %d want %d: %s", method, path, got, status, b)
	}
	return b
}

func TestWebDAV(t *testing.T) {
	env := testutil.New(t)
	ctx := context.Background()
	svc := env.Svc
	svc.SetupAdmin(ctx, "alice", "alicepass", "")
	p, _ := svc.Authenticate(ctx, "alice", "alicepass")
	p.Via = "session"
	_, secret, _ := svc.CreateAccessKey(ctx, p, "dav", "", false)

	h := dav.New(vfs.New(svc), slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	mux := http.NewServeMux()
	mux.Handle(dav.Prefix+"/", h)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	bad := &dc{t: t, base: ts.URL, user: "alice", pass: "alicepass"} // account password is not accepted
	bad.want("PROPFIND", "/", "", map[string]string{"Depth": "0"}, 401)
	c := &dc{t: t, base: ts.URL, user: "alice", pass: secret}

	_, _, hdr := c.do("OPTIONS", "/", "", nil)
	if !strings.Contains(hdr.Get("DAV"), "1") {
		t.Fatalf("DAV header: %v", hdr)
	}
	out := c.want("PROPFIND", "/", "", map[string]string{"Depth": "1"}, 207)
	if !strings.Contains(out, "/dav/alice/") {
		t.Fatalf("root propfind: %s", out)
	}
	c.want("MKCOL", "/alice/Docs", "", nil, 201)
	c.want("MKCOL", "/alice/Docs", "", nil, 405)
	c.want("MKCOL", "/alice/missing/sub", "", nil, 409)
	c.want("MKCOL", "/newspace", "", nil, 405) // spaces are created in the UI

	c.want("PUT", "/alice/Docs/report.txt", "version one", nil, 201)
	c.want("PUT", "/alice/Docs/report.txt", "version two", nil, 204)
	c.want("PUT", "/alice/nope/x.txt", "x", nil, 409)
	if b := c.want("GET", "/alice/Docs/report.txt", "", nil, 200); b != "version two" {
		t.Fatalf("get: %q", b)
	}
	_, _, gh := c.do("GET", "/alice/Docs/report.txt", "", nil)
	if !strings.Contains(gh.Get("Content-Security-Policy"), "sandbox") || gh.Get("ETag") == "" {
		t.Fatalf("headers: %v", gh)
	}

	out = c.want("PROPFIND", "/alice/Docs", "", map[string]string{"Depth": "1"}, 207)
	if !strings.Contains(out, "report.txt") || !strings.Contains(out, "getcontentlength") {
		t.Fatalf("propfind: %s", out)
	}
	// tildoc:links property
	propReq := `<?xml version="1.0"?><d:propfind xmlns:d="DAV:" xmlns:x="urn:tildoc"><d:prop><x:links/><x:document-id/></d:prop></d:propfind>`
	out = c.want("PROPFIND", "/alice/Docs/report.txt", propReq, map[string]string{"Depth": "0"}, 207)
	if !strings.Contains(out, "/alice/Docs/report.txt</path>") {
		t.Fatalf("links prop: %s", out)
	}

	// KIO-style atomic save: PUT .part then MOVE over the original
	c.want("PUT", "/alice/Docs/report.txt.part", "version three", nil, 201)
	c.want("MOVE", "/alice/Docs/report.txt.part", "", map[string]string{"Destination": ts.URL + "/dav/alice/Docs/report.txt", "Overwrite": "T"}, 204)
	sp, _ := svc.GetSpace(ctx, p, "alice")
	f, err := svc.FileByPath(ctx, p, sp.ID, []string{"Docs", "report.txt"})
	if err != nil || f.Version != 3 {
		t.Fatalf("rename-over must create version 3: %+v %v", f, err)
	}
	c.want("MOVE", "/alice/Docs/report.txt", "", map[string]string{"Destination": ts.URL + "/dav/alice/Docs/x.txt", "Overwrite": "F"}, 201)
	c.want("PUT", "/alice/Docs/y.txt", "y", nil, 201)
	c.want("MOVE", "/alice/Docs/y.txt", "", map[string]string{"Destination": ts.URL + "/dav/alice/Docs/x.txt", "Overwrite": "F"}, 412)

	// COPY: same document, both visible in tildoc:links
	c.want("COPY", "/alice/Docs/x.txt", "", map[string]string{"Destination": ts.URL + "/dav/alice/copy.txt"}, 201)
	out = c.want("PROPFIND", "/alice/copy.txt", propReq, map[string]string{"Depth": "0"}, 207)
	if !strings.Contains(out, "/alice/Docs/x.txt</path>") || !strings.Contains(out, "/alice/copy.txt</path>") {
		t.Fatalf("links after copy: %s", out)
	}
	// writing the copy forks it (copy-on-write)
	c.want("PUT", "/alice/copy.txt", "edited copy", nil, 204)
	if b := c.want("GET", "/alice/Docs/x.txt", "", nil, 200); b != "version three" {
		t.Fatalf("original changed by copy edit: %q", b)
	}

	// folder MOVE / COPY / DELETE
	c.want("COPY", "/alice/Docs", "", map[string]string{"Destination": ts.URL + "/dav/alice/Docs2", "Depth": "infinity"}, 201)
	c.want("MOVE", "/alice/Docs2", "", map[string]string{"Destination": ts.URL + "/dav/alice/Archive"}, 201)
	if b := c.want("GET", "/alice/Archive/x.txt", "", nil, 200); b != "version three" {
		t.Fatalf("moved folder content: %q", b)
	}
	c.want("DELETE", "/alice/Archive", "", nil, 204)
	c.want("GET", "/alice/Archive/x.txt", "", nil, 404)
	if tr, _ := svc.ListTrash(ctx, p, ""); len(tr) != 0 {
		// x.txt in Archive was a copy (same document as Docs/x.txt), y.txt too
		for _, it := range tr {
			if !strings.HasPrefix(it.Path, "/Archive") {
				t.Fatalf("unexpected trash entry: %+v", it)
			}
		}
	}

	// LOCK on unmapped URL creates an empty file; UNLOCK
	lockBody := `<?xml version="1.0"?><d:lockinfo xmlns:d="DAV:"><d:lockscope><d:exclusive/></d:lockscope><d:locktype><d:write/></d:locktype><d:owner>t</d:owner></d:lockinfo>`
	code, body, lh := c.do("LOCK", "/alice/locked.txt", lockBody, map[string]string{"Timeout": "Second-60"})
	if code != 201 && code != 200 {
		t.Fatalf("lock: %d %s", code, body)
	}
	c.want("UNLOCK", "/alice/locked.txt", "", map[string]string{"Lock-Token": lh.Get("Lock-Token")}, 204)
	c.want("GET", "/alice/locked.txt", "", nil, 200)

	// read-only key cannot write
	_, roSecret, _ := svc.CreateAccessKey(ctx, p, "ro", "", true)
	ro := &dc{t: t, base: ts.URL, user: "alice", pass: roSecret}
	ro.want("GET", "/alice/Docs/x.txt", "", nil, 200)
	ro.want("PUT", "/alice/Docs/z.txt", "z", nil, 403)
	ro.want("DELETE", "/alice/Docs/x.txt", "", nil, 403)
	_ = core.ErrNotFound
}
