package api_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestExternalOverHTTP(t *testing.T) {
	ts, _ := liveServer(t, "")
	alice, sp := aliceClient(t, ts.URL)
	var key struct {
		Token string `json:"token"`
	}
	alice.ok("POST", "/api/v1/me/api-keys", map[string]any{"label": "app", "permissions": []string{"view", "add", "write", "tag"}, "space_ids": []string{sp.ID}}, &key)
	call := func(method, ref, query, body string, hdr map[string]string, out any) int {
		t.Helper()
		req, _ := http.NewRequest(method, ts.URL+"/api/v1/spaces/"+sp.ID+"/external/billing/"+url.PathEscape(ref)+query, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+key.Token)
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if out != nil {
			if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
				t.Fatal(err)
			}
		}
		return resp.StatusCode
	}
	if c := call("PUT", "a/b", "?path=Docs/x.pdf", "one", nil, nil); c != 201 {
		t.Fatalf("create: %d", c)
	}
	if c := call("PUT", "a/b", "?path=Docs/x.pdf", "one", nil, nil); c != 200 {
		t.Fatalf("retry: %d", c)
	}
	if c := call("PUT", "n", "?path=n.pdf", "x", map[string]string{"X-Content-SHA256": strings.Repeat("0", 64)}, nil); c != 422 {
		t.Fatalf("checksum: %d", c)
	}
	if c := call("PUT", "a/b", "", "two", map[string]string{"If-Match": `"nope"`}, nil); c != 412 {
		t.Fatalf("if-match: %d", c)
	}
	var got struct {
		Reference struct {
			Ref string `json:"ref"`
		} `json:"reference"`
		Document struct {
			ID string `json:"id"`
		} `json:"document"`
	}
	if c := call("GET", "a/b", "", "", nil, &got); c != 200 || got.Reference.Ref != "a/b" || got.Document.ID == "" {
		t.Fatalf("get: %d %+v", c, got)
	}
	alice.ok("PATCH", "/api/v1/spaces/"+sp.ID+"/settings", map[string]any{"write_policy": "append_only"}, nil)
	if c := call("PUT", "a/b", "", "two", nil, nil); c != 403 {
		t.Fatalf("append-only: %d", c)
	}
	if c := call("DELETE", "a/b", "", "", nil, nil); c != 204 {
		t.Fatalf("delete: %d", c)
	}
	if c := call("GET", "a/b", "", "", nil, nil); c != 404 {
		t.Fatalf("after delete: %d", c)
	}
}
