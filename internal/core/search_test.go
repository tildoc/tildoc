package core_test

import (
	"testing"

	"github.com/tildoc/tildoc/internal/core"
)

func TestSearchHitPage(t *testing.T) {
	f := newFixture(t)
	res := f.put(t, f.alice, f.home.ID, "/docs/report.pdf", "%PDF-1.4 placeholder")
	text := "Introduction\nnothing here\f" + "Second page\f" + "Third page mentions the Zebra budget\f"
	if err := f.svc.StoreText(f.ctx, res.File.SHA256, "pdf-digital", text); err != nil {
		t.Fatal(err)
	}
	r := ck(f.svc.Search(f.ctx, f.alice, core.SearchQuery{Q: "zebra", Limit: 10}))
	if len(r.Hits) != 1 || r.Hits[0].Page == nil || *r.Hits[0].Page != 3 {
		t.Fatalf("hits %+v", r.Hits)
	}
	// name-only match carries no page
	r = ck(f.svc.Search(f.ctx, f.alice, core.SearchQuery{Q: "report", Limit: 10}))
	if len(r.Hits) != 1 || r.Hits[0].Page != nil {
		t.Fatalf("name hit %+v", r.Hits[0])
	}
}

func TestPrefs(t *testing.T) {
	f := newFixture(t)
	p := ck(f.svc.GetPrefs(f.ctx, f.alice.UserID))
	if p.PDFViewer != core.PDFViewerPDFJS {
		t.Fatalf("default %+v", p)
	}
	native := core.PDFViewerNative
	ck(f.svc.SetPrefs(f.ctx, f.alice, core.PrefsUpdate{PDFViewer: &native}))
	if p = ck(f.svc.GetPrefs(f.ctx, f.alice.UserID)); p.PDFViewer != native {
		t.Fatalf("after set %+v", p)
	}
	bad := "acrobat"
	if _, err := f.svc.SetPrefs(f.ctx, f.alice, core.PrefsUpdate{PDFViewer: &bad}); core.KindOf(err) != core.ErrInvalid {
		t.Fatalf("invalid viewer: %v", err)
	}
	if p = ck(f.svc.GetPrefs(f.ctx, f.bob.UserID)); p.PDFViewer != core.PDFViewerPDFJS {
		t.Fatalf("bob %+v", p)
	}
}
