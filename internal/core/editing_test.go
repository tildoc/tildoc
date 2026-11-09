package core_test

import (
	"strings"
	"testing"

	"github.com/tildoc/tildoc/internal/core"
)

const (
	tabA = "11111111-1111-4111-8111-111111111111"
	tabB = "22222222-2222-4222-8222-222222222222"
	tabC = "33333333-3333-4333-8333-333333333333"
)

func TestEditLease(t *testing.T) {
	f := newFixture(t)
	team := ck(f.svc.CreateTeam(f.ctx, f.alice, "T"))
	ts := ck(f.svc.CreateSpace(f.ctx, f.alice, "team", "", team.ID))
	bob := ck(f.svc.UserByName(f.ctx, "bob"))
	noErr(t, f.svc.SetMember(f.ctx, f.alice, team.ID, bob.ID, "member"))
	r := f.put(t, f.alice, ts.ID, "/notes.md", "# Notes\n")
	doc := r.File.DocumentID

	// alice edits in tab A
	l := ck(f.svc.AcquireEditLease(f.ctx, f.alice, doc, tabA, false))
	if !l.Mine || l.User != "alice" {
		t.Fatalf("lease: %+v", l)
	}
	if d := ck(f.svc.GetDocument(f.ctx, f.bob, doc)); d.EditLease == nil || d.EditLease.Mine || d.EditLease.User != "alice" {
		t.Fatalf("bob sees lease: %+v", d.EditLease)
	}
	// bob cannot, alice's other tab cannot without takeover
	l, err := f.svc.AcquireEditLease(f.ctx, f.bob, doc, tabC, false)
	wantKind(t, err, core.ErrConflict)
	if l == nil || l.User != "alice" || !strings.Contains(err.Error(), "alice") {
		t.Fatalf("conflict: %+v %v", l, err)
	}
	_, err = f.svc.AcquireEditLease(f.ctx, f.bob, doc, tabC, true) // takeover is only for one's own tabs
	wantKind(t, err, core.ErrConflict)
	l, err = f.svc.AcquireEditLease(f.ctx, f.alice, doc, tabB, false)
	wantKind(t, err, core.ErrConflict)
	if !l.Mine {
		t.Fatal("own lease in another tab must be reported as mine")
	}
	// same tab again is fine; takeover moves it to tab B, tab A loses it
	ck(f.svc.AcquireEditLease(f.ctx, f.alice, doc, tabA, false))
	ck(f.svc.AcquireEditLease(f.ctx, f.alice, doc, tabB, true))
	wantKind(t, f.svc.RenewEditLease(f.ctx, f.alice, doc, tabA), core.ErrConflict)
	noErr(t, f.svc.RenewEditLease(f.ctx, f.alice, doc, tabB))

	// saving needs the lease of this tab
	_, err = f.svc.SaveText(f.ctx, f.alice, doc, tabA, strings.NewReader("lost"))
	wantKind(t, err, core.ErrConflict)
	d := ck(f.svc.SaveText(f.ctx, f.alice, doc, tabB, strings.NewReader("# Notes\nsecond\n")))
	if d.Version != 2 || d.Versions[0].Source != "editor" || d.EditLease == nil {
		t.Fatalf("save: v%d %+v %+v", d.Version, d.Versions[0], d.EditLease)
	}
	_, err = f.svc.SaveText(f.ctx, f.alice, doc, tabB, strings.NewReader("bad \xff utf8"))
	wantKind(t, err, core.ErrInvalid)
	_, err = f.svc.SaveText(f.ctx, f.alice, doc, tabB, strings.NewReader(strings.Repeat("x", core.MaxEditBytes+1)))
	wantKind(t, err, core.ErrTooLarge)

	// an expired lease (tab closed, no heartbeat for 15 s) is free for others
	_, err = f.env.Pool.Exec(f.ctx, `UPDATE edit_leases SET acquired_at = now() - interval '1 minute', expires_at = now() - interval '1 second'`)
	noErr(t, err)
	if d := ck(f.svc.GetDocument(f.ctx, f.bob, doc)); d.EditLease != nil {
		t.Fatalf("expired lease still shown: %+v", d.EditLease)
	}
	l = ck(f.svc.AcquireEditLease(f.ctx, f.bob, doc, tabC, false))
	if !l.Mine || l.User != "bob" {
		t.Fatalf("bob after expiry: %+v", l)
	}
	noErr(t, f.svc.ReleaseEditLease(f.ctx, f.bob, doc, tabC))
	if d := ck(f.svc.GetDocument(f.ctx, f.alice, doc)); d.EditLease != nil {
		t.Fatal("released lease still shown")
	}

	// only writable text documents within the limit
	pdf := f.put(t, f.alice, ts.ID, "/scan.pdf", "%PDF-1.4 x")
	_, err = f.svc.AcquireEditLease(f.ctx, f.alice, pdf.File.DocumentID, tabA, false)
	wantKind(t, err, core.ErrInvalid)
	noErr(t, f.svc.SetMember(f.ctx, f.alice, team.ID, bob.ID, "viewer"))
	_, err = f.svc.AcquireEditLease(f.ctx, f.bob, doc, tabC, false)
	wantKind(t, err, core.ErrForbidden)
	_, err = f.svc.AcquireEditLease(f.ctx, f.alice, doc, "not-a-uuid", false)
	wantKind(t, err, core.ErrInvalid)
}
