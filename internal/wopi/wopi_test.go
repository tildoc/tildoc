package wopi

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The testdata are excerpts of what the real servers publish
// (CODE 26.04.6.1.1, ONLYOFFICE 10.0.0.1).
func parseFile(t *testing.T, name string) *Discovery {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	d, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestParseCollabora(t *testing.T) {
	d := parseFile(t, "collabora.xml")
	p, ok := d.ActionPath("DOCX", true, false)
	if !ok || !strings.HasSuffix(p, "/cool.html") || !strings.HasPrefix(p, "/browser/") {
		t.Fatalf("docx: %q %v", p, ok)
	}
	if len(d.keys) != 0 {
		t.Fatal("this excerpt has no proof key")
	}
}

func TestParseOnlyOffice(t *testing.T) {
	d := parseFile(t, "onlyoffice.xml")
	if p, ok := d.ActionPath("docx", true, false); !ok || p != "/hosting/wopi/word/edit" {
		t.Fatalf("edit: %q %v", p, ok)
	}
	if p, ok := d.ActionPath("docx", false, false); !ok || p != "/hosting/wopi/word/view" {
		t.Fatalf("view: %q %v", p, ok)
	}
	// binary formats have no edit action: they open read-only
	if p, ok := d.ActionPath("doc", true, false); !ok || p != "/hosting/wopi/word/view" {
		t.Fatalf("doc: %q %v", p, ok)
	}
	if len(d.keys) != 2 || d.keys[0].E != 65537 || d.keys[0].N.BitLen() < 2048 {
		t.Fatalf("proof keys: %d", len(d.keys))
	}
}

func TestCreatable(t *testing.T) {
	d, err := Parse([]byte(`<wopi-discovery><net-zone><app>
<action ext="docx" name="edit" urlsrc="https://x/edit"/><action ext="docx" name="editnew" urlsrc="https://x/new"/>
<action ext="txt" name="editnew" urlsrc="https://x/new"/><action ext="pdf" name="view" urlsrc="https://x/view"/>
</app></net-zone></wopi-discovery>`))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(d.Creatable(), ","); got != "docx" {
		t.Fatalf("creatable %q", got)
	}
	if got := strings.Join(d.Editable(), ","); got != "docx" {
		t.Fatalf("editable %q", got)
	}
	if p, _ := d.ActionPath("docx", true, true); p != "/new" {
		t.Fatalf("empty file: %q", p)
	}
	if p, _ := d.ActionPath("docx", true, false); p != "/edit" {
		t.Fatalf("file with content: %q", p)
	}
	if p, _ := d.ActionPath("docx", false, true); p != "/edit" { // only "edit" exists, read-only by UserCanWrite
		t.Fatalf("reader: %q", p)
	}
}
