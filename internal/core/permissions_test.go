package core_test

import (
	"slices"
	"testing"

	"github.com/tildoc/tildoc/internal/core"
)

func spaceFor(t *testing.T, f *fixture, p *core.Principal, id string) *core.Space {
	t.Helper()
	for _, sp := range ck(f.svc.ListSpaces(f.ctx, p)) {
		if sp.ID == id {
			return &sp
		}
	}
	return nil
}

func TestSpaceSharing(t *testing.T) {
	f := newFixture(t)
	admin := session(t, f.svc, "admin", "supersecret")
	carol := ck(f.svc.CreateUser(f.ctx, admin, "carol", "carolpass", "", false))
	dave := ck(f.svc.CreateUser(f.ctx, admin, "dave", "davepass1", "", false))
	cp := session(t, f.svc, "carol", "carolpass")
	dp := session(t, f.svc, "dave", "davepass1")
	doc := f.put(t, f.alice, f.home.ID, "/Steuer/bescheid.txt", "Bescheid Zebrastreifen")
	f.index(t, doc, "Bescheid Zebrastreifen")
	hroot := ck(f.svc.FolderByPath(f.ctx, f.alice, f.home.ID, nil))

	if spaceFor(t, f, f.bob, f.home.ID) != nil {
		t.Fatal("bob sees alice's space before sharing")
	}
	// direct share as viewer
	g := ck(f.svc.SetGrant(f.ctx, f.alice, f.home.ID, "bob", "", "viewer"))
	sp := spaceFor(t, f, f.bob, f.home.ID)
	if sp == nil || sp.Role != "viewer" || !sp.Shared || !slices.Equal(sp.Caps, []string{"view", "download", "export"}) {
		t.Fatalf("viewer share: %+v", sp)
	}
	if h := f.hits(t, f.bob, core.SearchQuery{Q: "Zebrastreifen"}); len(h) != 1 {
		t.Fatalf("shared space not searchable: %d", len(h))
	}
	if _, err := f.svc.Mkdir(f.ctx, f.bob, hroot.ID, "nope"); core.KindOf(err) != core.ErrForbidden {
		t.Fatalf("viewer wrote: %v", err)
	}
	if _, err := f.svc.SetGrant(f.ctx, f.bob, f.home.ID, "carol", "", "viewer"); core.KindOf(err) != core.ErrForbidden {
		t.Fatalf("viewer shared: %v", err)
	}
	// upgrade: member writes, admin shares but never above itself
	ck(f.svc.SetGrant(f.ctx, f.alice, f.home.ID, "bob", "", "member"))
	ck(f.svc.Mkdir(f.ctx, f.bob, hroot.ID, "von-bob"))
	ck(f.svc.SetGrant(f.ctx, f.alice, f.home.ID, "bob", "", "admin"))
	ck(f.svc.SetGrant(f.ctx, f.bob, f.home.ID, "carol", "", "admin"))
	if _, err := f.svc.SetGrant(f.ctx, f.bob, f.home.ID, "dave", "", "owner"); core.KindOf(err) != core.ErrInvalid {
		t.Fatalf("owner grant: %v", err)
	}
	if err := f.svc.DeleteSpace(f.ctx, f.bob, f.home.ID); core.KindOf(err) != core.ErrForbidden {
		t.Fatalf("admin grantee deleted the space: %v", err)
	}
	if grants := ck(f.svc.ListGrants(f.ctx, cp, f.home.ID)); len(grants) != 2 {
		t.Fatalf("grants: %+v", grants)
	}

	// team share: members get the grant role, capped by their team role
	team := ck(f.svc.CreateTeam(f.ctx, f.bob, "Steuerberatung"))
	if err := f.svc.SetMember(f.ctx, f.bob, team.ID, dave.ID, "viewer"); err != nil {
		t.Fatal(err)
	}
	ck(f.svc.SetGrant(f.ctx, f.bob, f.home.ID, "", team.ID, "member"))
	if sp := spaceFor(t, f, dp, f.home.ID); sp == nil || sp.Role != "viewer" {
		t.Fatalf("team viewer through member grant: %+v", sp)
	}
	if _, err := f.svc.SetGrant(f.ctx, cp, f.home.ID, "", team.ID, "viewer"); core.KindOf(err) != core.ErrNotFound {
		t.Fatalf("shared with a team the caller is not in: %v", err)
	}

	// read-only access keys reduce capabilities
	ro := *f.bob
	ro.ReadOnly = true
	if sp := spaceFor(t, f, &ro, f.home.ID); sp == nil || slices.Contains(sp.Caps, "write") || slices.Contains(sp.Caps, "share") {
		t.Fatalf("read-only key caps: %+v", sp)
	}

	// collections across shared spaces show shared documents
	coll := ck(f.svc.CreateCollection(f.ctx, f.bob, "Mandant Alice", "", nil))
	if err := f.svc.AddToCollection(f.ctx, f.bob, coll.ID, []string{doc.File.DocumentID}); err != nil {
		t.Fatal(err)
	}

	// revoking removes every kind of access at once
	if err := f.svc.DeleteGrant(f.ctx, f.alice, f.home.ID, g.ID); err != nil {
		t.Fatal(err)
	}
	if sp := spaceFor(t, f, f.bob, f.home.ID); sp == nil || sp.Role != "member" {
		t.Fatalf("bob keeps member access through his team's grant: %+v", sp)
	}
	for _, gr := range ck(f.svc.ListGrants(f.ctx, f.alice, f.home.ID)) {
		if gr.TeamID != nil {
			if err := f.svc.DeleteGrant(f.ctx, f.alice, f.home.ID, gr.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	if spaceFor(t, f, f.bob, f.home.ID) != nil || len(f.hits(t, f.bob, core.SearchQuery{Q: "Zebrastreifen"})) != 0 {
		t.Fatal("bob kept access after revoke")
	}
	if items := ck(f.svc.CollectionItems(f.ctx, f.bob, coll.ID)); len(items) != 0 {
		t.Fatal("collection still shows a document of a space no longer shared")
	}
	// carol's own grant (made by bob while he was admin) stays until revoked
	if spaceFor(t, f, cp, f.home.ID) == nil {
		t.Fatal("carol lost access")
	}
	_ = carol
}
