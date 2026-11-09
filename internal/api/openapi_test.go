package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
)

func restRoutes() []string {
	var out []string
	for _, p := range (&Server{}).routes(http.NewServeMux()) {
		if _, path, _ := strings.Cut(p, " "); strings.HasPrefix(path, "/api/v1/") {
			out = append(out, p)
		}
	}
	return out
}

func TestOpenAPIRoutesDocumented(t *testing.T) {
	routes := restRoutes()
	if len(routes) == 0 {
		t.Fatal("no routes registered")
	}
	for _, r := range routes {
		if _, ok := apiDocs[r]; !ok {
			t.Errorf("route %q has no entry in apiDocs", r)
		}
	}
	for r := range apiDocs {
		if !slices.Contains(routes, r) {
			t.Errorf("apiDocs entry %q has no route", r)
		}
	}
}

func TestOpenAPIDeterministic(t *testing.T) {
	a, err := generateOpenAPI()
	if err != nil {
		t.Fatal(err)
	}
	b, err := generateOpenAPI()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatal("generator output is not deterministic")
	}
}

// TestOpenAPIContract checks path parameters and that every reference resolves.
func TestOpenAPIContract(t *testing.T) {
	raw, err := generateOpenAPI()
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if v := doc["info"].(map[string]any)["version"]; v != "1" {
		t.Fatalf("info.version = %v, want the API version 1", v)
	}
	for path, item := range doc["paths"].(map[string]any) {
		for method, op := range item.(map[string]any) {
			declared := map[string]bool{}
			params, _ := op.(map[string]any)["parameters"].([]any)
			for _, p := range params {
				m := p.(map[string]any)
				key := m["in"].(string) + ":" + m["name"].(string)
				if declared[key] {
					t.Errorf("%s %s: duplicate parameter %s", method, path, key)
				}
				declared[key] = true
			}
			for _, name := range pathParams(path) {
				if !declared["path:"+name] {
					t.Errorf("%s %s: {%s} is not declared", method, path, name)
				}
			}
			for key := range declared {
				if name, ok := strings.CutPrefix(key, "path:"); ok && !slices.Contains(pathParams(path), name) {
					t.Errorf("%s %s: declared parameter %s is not in the path", method, path, name)
				}
			}
		}
	}
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			if ref, ok := x["$ref"].(string); ok && !resolves(doc, ref) {
				t.Errorf("unresolved reference %s", ref)
			}
			for _, e := range x {
				walk(e)
			}
		case []any:
			for _, e := range x {
				walk(e)
			}
		}
	}
	walk(doc)
}

func resolves(doc map[string]any, ref string) bool {
	var cur any = doc
	for _, seg := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
		m, ok := cur.(map[string]any)
		if !ok {
			return false
		}
		if cur, ok = m[seg]; !ok {
			return false
		}
	}
	return true
}
