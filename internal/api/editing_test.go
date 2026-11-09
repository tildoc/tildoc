package api_test

import (
	"net/http"
	"strings"
	"testing"
)

func TestEditLeaseAPI(t *testing.T) {
	ts, _ := liveServer(t, "")
	alice, sp := aliceClient(t, ts.URL)
	var up upload
	alice.ok("PUT", "/api/folders/"+sp.RootFolderID+"/files?path=todo.md", "- one\n", &up)
	doc := "/api/documents/" + up.File.DocumentID
	const a, b = "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"

	var lease struct {
		User string
		Mine bool
	}
	alice.ok("POST", doc+"/edit-lease", map[string]any{"client_id": a}, &lease)
	if !lease.Mine || lease.User != "alice" {
		t.Fatalf("lease %+v", lease)
	}
	// another window: 409 with the holder
	var conflict string
	expect(t, alice.do("POST", doc+"/edit-lease", map[string]any{"client_id": b}, &conflict), 409, "second window")
	if !strings.Contains(conflict, `"lease"`) || !strings.Contains(conflict, `"mine":true`) {
		t.Fatalf("conflict body %s", conflict)
	}
	expect(t, alice.do("PUT", doc+"/edit-lease", map[string]any{"client_id": a}, nil), 204, "heartbeat")
	expect(t, alice.do("PUT", doc+"/text?client="+b, "- one\n- two\n", nil), 409, "save without lease")
	var d struct {
		Version  int
		Versions []struct{ Source string }
	}
	alice.ok("PUT", doc+"/text?client="+a, "- one\n- two\n", &d)
	if d.Version != 2 || d.Versions[0].Source != "editor" {
		t.Fatalf("saved %+v", d)
	}
	expect(t, alice.do("PUT", doc+"/text?client="+a, strings.Repeat("x", 5<<20+10), nil), 413, "too large")
	expect(t, alice.do(http.MethodDelete, doc+"/edit-lease?client="+a, nil, nil), 204, "release")
	alice.ok("POST", doc+"/edit-lease", map[string]any{"client_id": b}, nil) // free again
	// CSRF protection applies to the lease too
	alice.csrf = false
	expect(t, alice.do("POST", doc+"/edit-lease", map[string]any{"client_id": a}, nil), 403, "csrf")
}

func TestVersionOnlyAfterLogin(t *testing.T) {
	ts, _ := liveServer(t, "")
	alice, _ := aliceClient(t, ts.URL)
	var me struct{ Version string }
	alice.ok("GET", "/api/me", nil, &me)
	if me.Version == "" {
		t.Fatal("no version")
	}
	anon := newClient(t, ts.URL)
	var info string
	anon.ok("GET", "/api/info", nil, &info)
	if strings.Contains(info, "version") {
		t.Fatalf("/api/info: %s", info)
	}
}
