package api_test

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tildoc/tildoc/internal/api"
	"github.com/tildoc/tildoc/internal/testutil"
)

// liveServer is newServer with the event hub running.
func liveServer(t *testing.T, uiDir string) (*httptest.Server, *api.Server) {
	env := testutil.New(t)
	env.Cfg.UIDir = uiDir
	srv := api.New(env.Svc, env.Cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { srv.Events.Run(ctx); close(done) }()
	<-srv.Events.Ready()
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() { srv.StopStreams(); ts.Close(); cancel(); <-done })
	return ts, srv
}

func aliceClient(t *testing.T, base string) (*client, space) {
	admin := newClient(t, base)
	admin.ok("POST", "/api/setup", map[string]string{"username": "admin", "password": "supersecret"}, nil)
	admin.ok("POST", "/api/users", map[string]any{"username": "alice", "password": "alicepass"}, nil)
	alice := newClient(t, base)
	alice.ok("POST", "/api/auth/login", map[string]string{"username": "alice", "password": "alicepass"}, nil)
	var spaces []space
	alice.ok("GET", "/api/spaces", nil, &spaces)
	return alice, spaces[0]
}

func get(t *testing.T, c *client, path string, hdr map[string]string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("GET", c.base+path, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestJSONETagGzipAndInclude(t *testing.T) {
	ts, _ := liveServer(t, "")
	alice, sp := aliceClient(t, ts.URL)
	var up upload
	for i := range 40 {
		alice.ok("PUT", "/api/folders/"+sp.RootFolderID+"/files?path="+url.QueryEscape(fmt.Sprintf("file-%02d.txt", i)), fmt.Sprintf("content %d", i), &up)
	}
	path := "/api/folders/" + sp.RootFolderID

	// gzip when accepted (the header is set by hand, so Go does not decode)
	resp := get(t, alice, path, map[string]string{"Accept-Encoding": "gzip"})
	if resp.Header.Get("Content-Encoding") != "gzip" || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("headers: %v", resp.Header)
	}
	zr, err := gzip.NewReader(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	var listing struct{ Files []fileInfo }
	if err := json.NewDecoder(zr).Decode(&listing); err != nil || len(listing.Files) != 40 {
		t.Fatalf("gzip body: %v %d", err, len(listing.Files))
	}
	resp.Body.Close()
	etag := resp.Header.Get("ETag")
	if !strings.HasPrefix(etag, `W/"`) {
		t.Fatalf("etag %q", etag)
	}

	// unchanged: 304 without body, for either encoding
	for _, ae := range []string{"gzip", ""} {
		resp = get(t, alice, path, map[string]string{"If-None-Match": etag, "Accept-Encoding": ae})
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotModified || len(b) != 0 {
			t.Fatalf("revalidate (%q): %d %q", ae, resp.StatusCode, b)
		}
	}
	// changed: full response with a new ETag
	alice.ok("PUT", "/api/folders/"+sp.RootFolderID+"/files?path=new.txt", "new", nil)
	resp = get(t, alice, path, map[string]string{"If-None-Match": etag})
	resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("ETag") == etag {
		t.Fatalf("after change: %d %s", resp.StatusCode, resp.Header.Get("ETag"))
	}
	// small responses are not compressed; errors carry no ETag
	resp = get(t, alice, "/api/me", map[string]string{"Accept-Encoding": "gzip"})
	resp.Body.Close()
	if resp.Header.Get("Content-Encoding") != "" || resp.Header.Get("ETag") == "" {
		t.Fatalf("small: %v", resp.Header)
	}
	resp = get(t, alice, "/api/folders/00000000-0000-0000-0000-000000000000", nil)
	resp.Body.Close()
	if resp.StatusCode != 404 || resp.Header.Get("ETag") != "" {
		t.Fatalf("404: %d %v", resp.StatusCode, resp.Header)
	}

	// the viewer's one request: file plus document
	var fd struct {
		fileInfo
		Document struct {
			ID      string
			Version int
		}
	}
	alice.ok("GET", "/api/files/"+up.File.LinkID+"?include=document", nil, &fd)
	if fd.LinkID != up.File.LinkID || fd.Document.ID != up.File.DocumentID || fd.Document.Version != 1 {
		t.Fatalf("include=document: %+v", fd)
	}
}

func TestEventStream(t *testing.T) {
	ts, srv := liveServer(t, "")
	alice, sp := aliceClient(t, ts.URL)

	anon := newClient(t, ts.URL)
	expect(t, anon.do("GET", "/api/events", nil, nil), 401, "anonymous stream")

	resp := get(t, alice, "/api/events", nil)
	defer resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "text/event-stream" || resp.Header.Get("Content-Encoding") != "" {
		t.Fatalf("stream: %d %v", resp.StatusCode, resp.Header)
	}
	lines := make(chan string, 64)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	waitLine := func(what string, match func(string) bool) string {
		t.Helper()
		deadline := time.After(5 * time.Second)
		for {
			select {
			case l, ok := <-lines:
				if !ok {
					t.Fatalf("stream closed while waiting for %s", what)
				}
				if match(l) {
					return l
				}
			case <-deadline:
				t.Fatalf("timeout waiting for %s", what)
			}
		}
	}
	waitLine("ready", func(l string) bool { return l == "event: ready" })

	var up upload
	alice.ok("PUT", "/api/folders/"+sp.RootFolderID+"/files?path=live.txt", "live", &up)
	l := waitLine("space event", func(l string) bool { return strings.HasPrefix(l, "data: [") && strings.Contains(l, up.File.DocumentID) })
	var evs []struct {
		Type      string
		SpaceID   string `json:"space_id"`
		Documents []string
	}
	if err := json.Unmarshal([]byte(strings.TrimPrefix(l, "data: ")), &evs); err != nil || evs[0].Type != "space" || evs[0].SpaceID != sp.ID {
		t.Fatalf("event %q: %v", l, err)
	}

	// shutdown ends the stream (http.Server.Shutdown would wait forever otherwise)
	srv.StopStreams()
	for range lines {
	}
}

func TestPrecompressedUI(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "_app", "immutable"), 0o755)
	js := strings.Repeat("console.log('tildoc');\n", 100)
	os.WriteFile(filepath.Join(dir, "_app", "immutable", "app.js"), []byte(js), 0o644)
	f, _ := os.Create(filepath.Join(dir, "_app", "immutable", "app.js.gz"))
	zw := gzip.NewWriter(f)
	zw.Write([]byte(js))
	zw.Close()
	f.Close()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!doctype html>"), 0o644)
	ts, _ := liveServer(t, dir)
	c := newClient(t, ts.URL)

	resp := get(t, c, "/_app/immutable/app.js", map[string]string{"Accept-Encoding": "br, gzip"})
	zr, err := gzip.NewReader(resp.Body)
	if err != nil {
		t.Fatalf("not gzip: %v %v", err, resp.Header)
	}
	b, _ := io.ReadAll(zr)
	resp.Body.Close()
	if string(b) != js || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/javascript") ||
		!strings.Contains(resp.Header.Get("Cache-Control"), "immutable") || resp.Header.Get("Vary") != "Accept-Encoding" {
		t.Fatalf("gzip asset: %v", resp.Header)
	}
	resp = get(t, c, "/_app/immutable/app.js", map[string]string{"Accept-Encoding": "identity"})
	b, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(b) != js || resp.Header.Get("Content-Encoding") != "" {
		t.Fatalf("plain asset: %v", resp.Header)
	}
}
