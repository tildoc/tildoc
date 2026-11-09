package core_test

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tildoc/tildoc/internal/core"
)

func allowSharing(t *testing.T, f *fixture, spaceID string) {
	t.Helper()
	ck(f.svc.UpdateSpaceSettings(f.ctx, f.alice, spaceID, core.SpaceSettings{AllowLoginLinks: true, AllowPublicLinks: true, AllowDropLinks: true, AllowFileShares: true}))
}

func TestRestrictedViewerCannotGetTheFile(t *testing.T) {
	f := newFixture(t)
	doc := f.put(t, f.alice, f.home.ID, "/a.pdf", "%PDF-1.7 secret")
	ck(f.svc.SetGrant(f.ctx, f.alice, f.home.ID, "bob", "", "restricted_viewer"))

	sp := spaceFor(t, f, f.bob, f.home.ID)
	if sp == nil || sp.Role != "restricted_viewer" || !slices.Equal(sp.Caps, []string{"view"}) {
		t.Fatalf("space: %+v", sp)
	}
	// shown in the browser: yes
	fi := ck(f.svc.GetFile(f.ctx, f.bob, doc.File.LinkID))
	rd := ck(f.svc.PreviewFile(f.ctx, f.bob, fi))
	rd.Close()
	r2, _, err := f.svc.OpenDocument(f.ctx, f.bob, doc.File.DocumentID, 0, "", true)
	noErr(t, err)
	r2.Close()
	// as a file, by any channel: no
	_, err = f.svc.OpenFile(f.ctx, f.bob, fi)
	wantKind(t, err, core.ErrForbidden)
	_, _, err = f.svc.OpenDocument(f.ctx, f.bob, doc.File.DocumentID, 0, "", false)
	wantKind(t, err, core.ErrForbidden)
	_, err = f.svc.CopyLink(f.ctx, f.bob, doc.File.LinkID, ck(f.svc.FolderByPath(f.ctx, f.bob, f.bhome.ID, nil)).ID, "", false)
	wantKind(t, err, core.ErrForbidden)
	// a type the browser can't render counts as a download
	zip := f.put(t, f.alice, f.home.ID, "/b.bin", "\x00\x01binary")
	_, _, err = f.svc.OpenDocument(f.ctx, f.bob, zip.File.DocumentID, 0, "", true)
	wantKind(t, err, core.ErrForbidden)
}

func TestGrantRowsAddUpAndCannotExceedTheGranter(t *testing.T) {
	f := newFixture(t)
	admin := session(t, f.svc, "admin", "supersecret")
	carol := ck(f.svc.CreateUser(f.ctx, admin, "carol", "carolpass", "", false))
	_ = carol
	cp := session(t, f.svc, "carol", "carolpass")
	// role plus individual permission
	g := ck(f.svc.SetGrant(f.ctx, f.alice, f.home.ID, "bob", "", "viewer", "share.space.people"))
	if g.Role != "viewer" || !slices.Equal(g.Permissions, []string{"share.space.people"}) {
		t.Fatalf("grant %+v", g)
	}
	// individual permissions only
	ck(f.svc.SetGrant(f.ctx, f.bob, f.home.ID, "carol", "", "", "view"))
	if sp := spaceFor(t, f, cp, f.home.ID); sp == nil || !slices.Equal(sp.Caps, []string{"view"}) {
		t.Fatalf("carol %+v", sp)
	}
	// nobody grants what they lack
	_, err := f.svc.SetGrant(f.ctx, f.bob, f.home.ID, "carol", "", "editor")
	wantKind(t, err, core.ErrForbidden)
	_, err = f.svc.SetGrant(f.ctx, f.bob, f.home.ID, "carol", "", "viewer", "manage")
	wantKind(t, err, core.ErrForbidden)
	_, err = f.svc.SetGrant(f.ctx, f.alice, f.home.ID, "carol", "", "", "nonsense")
	wantKind(t, err, core.ErrInvalid)
	_, err = f.svc.SetGrant(f.ctx, f.alice, f.home.ID, "carol", "", "administrator")
	wantKind(t, err, core.ErrInvalid)
	// a team's grant and a personal one add up
	team := ck(f.svc.CreateTeam(f.ctx, f.alice, "T"))
	bob := ck(f.svc.UserByName(f.ctx, "bob"))
	noErr(t, f.svc.SetMember(f.ctx, f.alice, team.ID, bob.ID, "member"))
	ck(f.svc.SetGrant(f.ctx, f.alice, f.home.ID, "", team.ID, "", "write"))
	sp := spaceFor(t, f, f.bob, f.home.ID)
	if !slices.Contains(sp.Caps, "write") || !slices.Contains(sp.Caps, "export") || sp.Role != "custom" {
		t.Fatalf("union %+v", sp)
	}
}

func TestRolesAreData(t *testing.T) {
	f := newFixture(t)
	admin := session(t, f.svc, "admin", "supersecret")
	_, err := f.svc.CreateRole(f.ctx, f.alice, "Nope", "space", []string{"view"})
	wantKind(t, err, core.ErrForbidden)
	r := ck(f.svc.CreateRole(f.ctx, admin, "Prüfer", "space", []string{"view", "download"}))
	ck(f.svc.SetGrant(f.ctx, f.alice, f.home.ID, "bob", "", r.Slug))
	if sp := spaceFor(t, f, f.bob, f.home.ID); !slices.Equal(sp.Caps, []string{"view", "download"}) {
		t.Fatalf("%+v", sp)
	}
	// editing the role changes everyone who holds it
	ck(f.svc.UpdateRole(f.ctx, admin, r.ID, nil, []string{"view"}))
	if sp := spaceFor(t, f, f.bob, f.home.ID); !slices.Equal(sp.Caps, []string{"view"}) {
		t.Fatalf("%+v", sp)
	}
	wantKind(t, f.svc.DeleteRole(f.ctx, admin, r.ID), core.ErrConflict) // in use
	roles := ck(f.svc.ListRoles(f.ctx, admin))
	var adminRole, viewer *core.Role
	for i := range roles {
		switch roles[i].Slug {
		case "administrator":
			adminRole = &roles[i]
		case "viewer":
			viewer = &roles[i]
		}
	}
	_, err = f.svc.UpdateRole(f.ctx, admin, adminRole.ID, nil, []string{})
	wantKind(t, err, core.ErrInvalid)
	wantKind(t, f.svc.DeleteRole(f.ctx, admin, viewer.ID), core.ErrInvalid)
	_, err = f.svc.CreateRole(f.ctx, admin, "Bad", "space", []string{"users.manage"})
	wantKind(t, err, core.ErrInvalid)
}

func TestAdministratorsSeeNoDocumentsAndCannotLockOut(t *testing.T) {
	f := newFixture(t)
	admin := session(t, f.svc, "admin", "supersecret")
	doc := f.put(t, f.alice, f.home.ID, "/a.txt", "private")
	_, err := f.svc.GetDocument(f.ctx, admin, doc.File.DocumentID)
	wantKind(t, err, core.ErrNotFound)
	if spaceFor(t, f, admin, f.home.ID) != nil {
		t.Fatal("admin sees alice's space")
	}
	// the last administrator stays
	au := ck(f.svc.UserByName(f.ctx, "admin"))
	second := ck(f.svc.CreateUser(f.ctx, admin, "root2", "root2pass", "", true))
	yes, no := true, false
	_, err = f.svc.UpdateUser(f.ctx, admin, second.ID, core.UserUpdate{IsAdmin: &no})
	noErr(t, err)
	_, err = f.svc.UpdateUser(f.ctx, session(t, f.svc, "admin", "supersecret"), au.ID, core.UserUpdate{Disabled: &yes})
	wantKind(t, err, core.ErrInvalid)
	// instance permissions can be given individually
	bob := ck(f.svc.UserByName(f.ctx, "bob"))
	ck(f.svc.SetInstanceGrant(f.ctx, admin, bob.ID, "", "user", "tags.manage"))
	bp := session(t, f.svc, "bob", "bobpass12")
	if !bp.Can(core.InstTags) || bp.Can(core.InstUsers) {
		t.Fatalf("bob: %v", bp.Inst.Names())
	}
}

func TestTransferSpace(t *testing.T) {
	f := newFixture(t)
	admin := session(t, f.svc, "admin", "supersecret")
	doc := f.put(t, f.alice, f.home.ID, "/a.txt", "content")
	sp := ck(f.svc.CreateSpace(f.ctx, f.alice, "projekt", "Projekt", ""))
	f.put(t, f.alice, sp.ID, "/p.txt", "projekt")

	wantKind(t, f.svc.TransferSpace(f.ctx, f.alice, sp.ID, "bob", "", "", "wrong"), core.ErrInvalid)
	wantKind(t, f.svc.TransferSpace(f.ctx, f.bob, sp.ID, "bob", "", "", sp.Slug), core.ErrNotFound)
	wantKind(t, f.svc.TransferSpace(f.ctx, f.alice, sp.ID, "alice", "", "", sp.Slug), core.ErrInvalid)
	// takes effect at once, the previous owner keeps a chosen role
	noErr(t, f.svc.TransferSpace(f.ctx, f.alice, sp.ID, "bob", "", "viewer", sp.Slug))
	if got := spaceFor(t, f, f.bob, sp.ID); got.Role != "owner" || got.Shared {
		t.Fatalf("new owner %+v", got)
	}
	if got := spaceFor(t, f, f.alice, sp.ID); got.Role != "viewer" {
		t.Fatalf("previous owner %+v", got)
	}
	// keys follow the rights: the previous owner can no longer write
	_, err := f.svc.PutFile(f.ctx, f.alice, sp.ID, []string{"x.txt"}, strings.NewReader("x"), core.PutOptions{})
	wantKind(t, err, core.ErrForbidden)

	// the last personal space: a new empty one appears
	noErr(t, f.svc.TransferSpace(f.ctx, f.alice, f.home.ID, "bob", "", "", f.home.Slug))
	if _, err := f.svc.GetDocument(f.ctx, f.alice, doc.File.DocumentID); core.KindOf(err) != core.ErrNotFound {
		t.Fatalf("alice still reads it: %v", err)
	}
	owned := 0
	for _, s := range ck(f.svc.ListSpaces(f.ctx, f.alice)) {
		if s.Role == "owner" {
			owned++
		}
	}
	if owned != 1 {
		t.Fatalf("alice owns %d spaces", owned)
	}

	// an admin can recover a space without being able to read it
	noErr(t, f.svc.TransferSpace(f.ctx, admin, sp.ID, "alice", "", "", sp.Slug))
	if got := spaceFor(t, f, admin, sp.ID); got != nil {
		t.Fatalf("admin reads it: %+v", got)
	}

	// a user who still owns documents cannot simply be deleted
	bob := ck(f.svc.UserByName(f.ctx, "bob"))
	wantKind(t, f.svc.DeleteUser(f.ctx, admin, bob.ID), core.ErrConflict)
}

func TestFileGrant(t *testing.T) {
	f := newFixture(t)
	allowSharing(t, f, f.home.ID)
	doc := f.put(t, f.alice, f.home.ID, "/Geheim/Ordner/a.txt", "text")
	other := f.put(t, f.alice, f.home.ID, "/b.txt", "other")
	id := doc.File.DocumentID
	_, err := f.svc.GetDocument(f.ctx, f.bob, id)
	wantKind(t, err, core.ErrNotFound)
	g := ck(f.svc.SetDocumentGrant(f.ctx, f.alice, id, "bob", "", "viewer"))

	d := ck(f.svc.GetDocument(f.ctx, f.bob, id))
	if len(d.Links) != 0 || !slices.Contains(d.Caps, "download") {
		t.Fatalf("doc %+v", d)
	}
	fi := ck(f.svc.GetFile(f.ctx, f.bob, doc.File.LinkID))
	if fi.Path != "/a.txt" || fi.FolderID != "" {
		t.Fatalf("the folder path leaks: %+v", fi)
	}
	shared := ck(f.svc.ListSharedFiles(f.ctx, f.bob))
	if len(shared) != 1 || shared[0].LinkID != doc.File.LinkID || shared[0].Path != "/a.txt" {
		t.Fatalf("shared %+v", shared)
	}
	// nothing else of the space
	if spaceFor(t, f, f.bob, f.home.ID) != nil {
		t.Fatal("the space became visible")
	}
	_, err = f.svc.GetDocument(f.ctx, f.bob, other.File.DocumentID)
	wantKind(t, err, core.ErrNotFound)
	// write only when granted
	_, err = f.svc.AddDocumentVersion(f.ctx, f.bob, id, strings.NewReader("new"), "web")
	wantKind(t, err, core.ErrForbidden)
	ck(f.svc.SetDocumentGrant(f.ctx, f.alice, id, "bob", "", "", "view", "write"))
	ck(f.svc.AddDocumentVersion(f.ctx, f.bob, id, strings.NewReader("new"), "web"))
	// a file grant can't carry space-wide rights
	_, err = f.svc.SetDocumentGrant(f.ctx, f.alice, id, "bob", "", "", "delete")
	wantKind(t, err, core.ErrInvalid)
	noErr(t, f.svc.DeleteDocumentGrant(f.ctx, f.alice, id, g.ID))
	_, err = f.svc.GetDocument(f.ctx, f.bob, id)
	wantKind(t, err, core.ErrNotFound)
}

func TestShareLinks(t *testing.T) {
	f := newFixture(t)
	admin := session(t, f.svc, "admin", "supersecret")
	allowSharing(t, f, f.home.ID)
	doc := f.put(t, f.alice, f.home.ID, "/a.txt", "text")
	id := doc.File.DocumentID
	pub := core.NewShareLink{DocumentID: id, Kind: "public", Preset: "restricted"}
	// off by default
	_, _, err := f.svc.CreateShareLink(f.ctx, f.alice, pub)
	wantKind(t, err, core.ErrForbidden)
	on := true
	ck(f.svc.UpdateSettings(f.ctx, admin, core.SettingsUpdate{PublicLinks: &on}))

	l, token := ck2(f.svc.CreateShareLink(f.ctx, f.alice, pub))
	a, sess := ck2(f.svc.OpenShare(f.ctx, token, "", "", nil))
	info := ck(f.svc.ShareInfo(f.ctx, a))
	if info.Name != "a.txt" || info.CanDownload {
		t.Fatalf("info %+v", info)
	}
	a2 := ck(f.svc.ResolveShare(f.ctx, l.ID, sess, nil))
	rd, _, err := f.svc.OpenShareContent(f.ctx, a2, "", true)
	noErr(t, err)
	rd.Close()
	_, _, err = f.svc.OpenShareContent(f.ctx, a2, "", false)
	wantKind(t, err, core.ErrForbidden)
	_, _, err = f.svc.OpenShare(f.ctx, "wrong", "", "", nil)
	wantKind(t, err, core.ErrNotFound)

	// password
	pw := core.NewShareLink{DocumentID: id, Kind: "public", Preset: "view", Password: "geheim123"}
	_, token2 := ck2(f.svc.CreateShareLink(f.ctx, f.alice, pw))
	if _, _, err := f.svc.OpenShare(f.ctx, token2, "falsch", "", nil); err != core.ErrSharePassword {
		t.Fatalf("wrong password: %v", err)
	}
	ck2(f.svc.OpenShare(f.ctx, token2, "geheim123", "", nil))

	// a link that needs a login
	lg, token3 := ck2(f.svc.CreateShareLink(f.ctx, f.alice, core.NewShareLink{DocumentID: id, Kind: "login", Preset: "view"}))
	if _, _, err := f.svc.OpenShare(f.ctx, token3, "", "", nil); err != core.ErrShareLogin {
		t.Fatalf("anonymous: %v", err)
	}
	_, s3 := ck2(f.svc.OpenShare(f.ctx, token3, "", "", f.bob))
	ck(f.svc.ResolveShare(f.ctx, lg.ID, s3, f.bob))
	_, err = f.svc.ResolveShare(f.ctx, lg.ID, s3, nil)
	wantKind(t, err, core.ErrNotFound)

	// space link: browse only inside the space
	sl, token4 := ck2(f.svc.CreateShareLink(f.ctx, f.alice, core.NewShareLink{SpaceID: f.home.ID, Kind: "public", Preset: "view"}))
	as, _ := ck2(f.svc.OpenShare(f.ctx, token4, "", "", nil))
	root := ck(f.svc.ShareInfo(f.ctx, as)).RootFolderID
	if files := ck(f.svc.ShareFolder(f.ctx, as, root)).Files; len(files) != 1 || files[0].Name != "a.txt" {
		t.Fatalf("listing %+v", files)
	}
	bobRoot := ck(f.svc.FolderByPath(f.ctx, f.bob, f.bhome.ID, nil)).ID
	_, err = f.svc.ShareFolder(f.ctx, as, bobRoot)
	wantKind(t, err, core.ErrNotFound)
	bobDoc := f.put(t, f.bob, f.bhome.ID, "/b.txt", "bobs")
	_, _, err = f.svc.OpenShareContent(f.ctx, as, bobDoc.File.LinkID, true)
	wantKind(t, err, core.ErrNotFound)

	// a reader can't make a link that gives more than they have
	ck(f.svc.SetGrant(f.ctx, f.alice, f.home.ID, "bob", "", "restricted_viewer", "share.space.public_link"))
	_, _, err = f.svc.CreateShareLink(f.ctx, f.bob, core.NewShareLink{SpaceID: f.home.ID, Kind: "public", Preset: "view"})
	wantKind(t, err, core.ErrForbidden)

	// the list of links is for those who may share
	ck(f.svc.SetGrant(f.ctx, f.alice, f.home.ID, "bob", "", "viewer"))
	_, err = f.svc.ListShareLinks(f.ctx, f.bob, f.home.ID, "")
	wantKind(t, err, core.ErrForbidden)
	ck(f.svc.SetGrant(f.ctx, f.alice, f.home.ID, "bob", "", "restricted_viewer", "share.space.public_link"))

	// a link never outlives its creator's rights
	bl, tb := ck2(f.svc.CreateShareLink(f.ctx, f.bob, core.NewShareLink{SpaceID: f.home.ID, Kind: "public", Preset: "restricted"}))
	_, bs := ck2(f.svc.OpenShare(f.ctx, tb, "", "", nil))
	for _, g := range ck(f.svc.ListGrants(f.ctx, f.alice, f.home.ID)) {
		noErr(t, f.svc.DeleteGrant(f.ctx, f.alice, f.home.ID, g.ID))
	}
	_, err = f.svc.ResolveShare(f.ctx, bl.ID, bs, nil)
	wantKind(t, err, core.ErrNotFound)

	// revoked and expired links are gone; so is everything when public links are switched off
	noErr(t, f.svc.RevokeShareLink(f.ctx, f.alice, sl.ID))
	_, _, err = f.svc.OpenShare(f.ctx, token4, "", "", nil)
	wantKind(t, err, core.ErrNotFound)
	soon := time.Now().Add(-time.Second)
	_, _, err = f.svc.CreateShareLink(f.ctx, f.alice, core.NewShareLink{DocumentID: id, Kind: "public", Preset: "view", ExpiresAt: &soon})
	wantKind(t, err, core.ErrInvalid)
	off := false
	ck(f.svc.UpdateSettings(f.ctx, admin, core.SettingsUpdate{PublicLinks: &off}))
	_, err = f.svc.ResolveShare(f.ctx, l.ID, sess, nil)
	wantKind(t, err, core.ErrNotFound)
}

func TestDropLink(t *testing.T) {
	f := newFixture(t)
	admin := session(t, f.svc, "admin", "supersecret")
	on := true
	ck(f.svc.UpdateSettings(f.ctx, admin, core.SettingsUpdate{PublicLinks: &on}))
	allowSharing(t, f, f.home.ID)
	f.put(t, f.alice, f.home.ID, "/Eingang/a.txt", "already there")
	inbox := ck(f.svc.FolderByPath(f.ctx, f.alice, f.home.ID, []string{"Eingang"}))
	l, token := ck2(f.svc.CreateShareLink(f.ctx, f.alice, core.NewShareLink{SpaceID: f.home.ID, FolderID: inbox.ID, Kind: "drop"}))
	if l.Preset != "drop" {
		t.Fatalf("%+v", l)
	}
	a, _ := ck2(f.svc.OpenShare(f.ctx, token, "", "", nil))
	info := ck(f.svc.ShareInfo(f.ctx, a))
	if info.RootFolderID != "" || info.File != nil || info.Name != "Eingang" {
		t.Fatalf("a drop link shows something: %+v", info)
	}
	// upload only: no listing, no reading
	_, err := f.svc.ShareFolder(f.ctx, a, inbox.ID)
	wantKind(t, err, core.ErrNotFound)
	_, _, err = f.svc.OpenShareContent(f.ctx, a, "", true)
	wantKind(t, err, core.ErrNotFound)
	// the same name never overwrites, the same content never merges
	noErr(t, f.svc.ShareUpload(f.ctx, a, "a.txt", strings.NewReader("from outside")))
	noErr(t, f.svc.ShareUpload(f.ctx, a, "a.txt", strings.NewReader("already there")))
	_, _, files, err := f.svc.ListFolder(f.ctx, f.alice, inbox.ID)
	noErr(t, err)
	names := []string{}
	for _, x := range files {
		names = append(names, x.Name)
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"a (2).txt", "a (3).txt", "a.txt"}) {
		t.Fatalf("names %v", names)
	}
	if f.read(t, f.alice, f.home.ID, "/Eingang/a.txt") != "already there" {
		t.Fatal("overwrote the existing file")
	}
	// a file can't have a drop link
	_, _, err = f.svc.CreateShareLink(f.ctx, f.alice, core.NewShareLink{DocumentID: files[0].DocumentID, Kind: "drop"})
	wantKind(t, err, core.ErrInvalid)
}

func TestDropInKey(t *testing.T) {
	f := newFixture(t)
	f.put(t, f.alice, f.home.ID, "/Eingang/a.txt", "already there")
	inbox := ck(f.svc.FolderByPath(f.ctx, f.alice, f.home.ID, []string{"Eingang"}))
	// off until the space allows it
	_, _, err := f.svc.CreateDropInKey(f.ctx, f.alice, "scanner", f.home.ID, inbox.ID, nil)
	wantKind(t, err, core.ErrForbidden)
	ck(f.svc.UpdateSpaceSettings(f.ctx, f.alice, f.home.ID, core.SpaceSettings{AllowDropIn: true}))
	k, token := ck2(f.svc.CreateDropInKey(f.ctx, f.alice, "scanner", f.home.ID, inbox.ID, nil))
	if k.FolderID == nil || *k.FolderID != inbox.ID || !slices.Equal(k.Permissions, []string{"drop_in"}) {
		t.Fatalf("key %+v", k)
	}
	p := ck(f.svc.APIKeyPrincipal(f.ctx, token))
	noErr(t, f.svc.DropIn(f.ctx, p, "a.txt", strings.NewReader("from a script")))
	noErr(t, f.svc.DropIn(f.ctx, p, "a.txt", strings.NewReader("already there")))
	_, _, files, err := f.svc.ListFolder(f.ctx, f.alice, inbox.ID)
	noErr(t, err)
	if len(files) != 3 || f.read(t, f.alice, f.home.ID, "/Eingang/a.txt") != "already there" {
		t.Fatalf("files %d", len(files))
	}
	// the key can do nothing else
	if spaceFor(t, f, p, f.home.ID) != nil {
		t.Fatal("a Drop-In key sees the space")
	}
	_, err = f.svc.GetDocument(f.ctx, p, files[0].DocumentID)
	wantKind(t, err, core.ErrNotFound)
	_, err = f.svc.PutFile(f.ctx, p, f.home.ID, []string{"x.txt"}, strings.NewReader("x"), core.PutOptions{})
	wantKind(t, err, core.ErrNotFound)
	// a normal key can't be one, a normal key can't drop
	_, _, err = f.svc.CreateAPIKey(f.ctx, f.alice, "x", []string{"drop_in"}, []string{f.home.ID}, nil)
	wantKind(t, err, core.ErrInvalid)
	_, normal := ck2(f.svc.CreateAPIKey(f.ctx, f.alice, "x", []string{"view", "write", "add"}, nil, nil))
	wantKind(t, f.svc.DropIn(f.ctx, ck(f.svc.APIKeyPrincipal(f.ctx, normal)), "b.txt", strings.NewReader("x")), core.ErrForbidden)
	// write protection and the setting stop it at once
	ck(f.svc.UpdateSpaceSettings(f.ctx, f.alice, f.home.ID, core.SpaceSettings{AllowDropIn: true, WritePolicy: core.WriteReadOnly}))
	wantKind(t, f.svc.DropIn(f.ctx, p, "c.txt", strings.NewReader("x")), core.ErrForbidden)
	ck(f.svc.UpdateSpaceSettings(f.ctx, f.alice, f.home.ID, core.SpaceSettings{WritePolicy: core.WriteOpen}))
	wantKind(t, f.svc.DropIn(f.ctx, p, "c.txt", strings.NewReader("x")), core.ErrForbidden)
	// a viewer can't create one
	ck(f.svc.UpdateSpaceSettings(f.ctx, f.alice, f.home.ID, core.SpaceSettings{AllowDropIn: true}))
	ck(f.svc.SetGrant(f.ctx, f.alice, f.home.ID, "bob", "", "viewer"))
	_, _, err = f.svc.CreateDropInKey(f.ctx, f.bob, "x", f.home.ID, "", nil)
	wantKind(t, err, core.ErrForbidden)
}

func TestPendingAccounts(t *testing.T) {
	f := newFixture(t)
	admin := session(t, f.svc, "admin", "supersecret")
	res := ck(f.svc.LoginExternal(f.ctx, "google", "https://accounts.google.com", "sub-1", "Gerd.Muster@example.org", "Gerd Muster"))
	if !res.Pending || res.Principal != nil {
		t.Fatalf("first login %+v", res)
	}
	var pending *core.User
	for _, u := range ck(f.svc.ListUsers(f.ctx, admin)) {
		if u.Status == "pending" {
			pending = &u
		}
	}
	if pending == nil || pending.Username != "gerd-muster" && pending.Username != "gerd.muster" {
		t.Fatalf("pending %+v", pending)
	}
	// invisible and unusable until approved
	for _, u := range ck(f.svc.ListUsers(f.ctx, f.alice)) {
		if u.Status == "pending" {
			t.Fatal("others see pending accounts")
		}
	}
	_, err := f.svc.SetGrant(f.ctx, f.alice, f.home.ID, pending.Username, "", "viewer")
	wantKind(t, err, core.ErrNotFound)
	if res := ck(f.svc.LoginExternal(f.ctx, "google", "https://accounts.google.com", "sub-1", "x@example.org", "")); !res.Pending {
		t.Fatal("still waiting")
	}
	_, err = f.svc.ApproveUser(f.ctx, f.alice, pending.ID)
	wantKind(t, err, core.ErrForbidden)

	// approved: a session, a personal space, the default role
	ck(f.svc.ApproveUser(f.ctx, admin, pending.ID))
	res = ck(f.svc.LoginExternal(f.ctx, "google", "https://accounts.google.com", "sub-1", "new-mail@example.org", ""))
	if res.Pending || res.Principal == nil || !res.Principal.Can(core.InstSpacesCreate) || res.Principal.Can(core.InstUsers) {
		t.Fatalf("after approval %+v", res)
	}
	if spaces := ck(f.svc.ListSpaces(f.ctx, res.Principal)); len(spaces) != 1 || spaces[0].Role != "owner" {
		t.Fatalf("spaces %+v", spaces)
	}
	// the account follows the provider's subject, not the e-mail address
	other := ck(f.svc.LoginExternal(f.ctx, "google", "https://accounts.google.com", "sub-2", "new-mail@example.org", ""))
	if !other.Pending {
		t.Fatal("same e-mail address must not reach the account")
	}
	// declining removes the account
	for _, u := range ck(f.svc.ListUsers(f.ctx, admin)) {
		if u.Status == "pending" {
			noErr(t, f.svc.DeleteUser(f.ctx, admin, u.ID))
		}
	}
	if !ck(f.svc.LoginExternal(f.ctx, "google", "https://accounts.google.com", "sub-2", "new-mail@example.org", "")).Pending {
		t.Fatal("declined accounts start over")
	}
	// a disabled account stays out
	yes := true
	ck(f.svc.UpdateUser(f.ctx, admin, pending.ID, core.UserUpdate{Disabled: &yes}))
	_, err = f.svc.LoginExternal(f.ctx, "google", "https://accounts.google.com", "sub-1", "x@example.org", "")
	wantKind(t, err, core.ErrUnauthorized)
}

func TestAPIKeys(t *testing.T) {
	f := newFixture(t)
	doc := f.put(t, f.alice, f.home.ID, "/a.txt", "text")
	_, _, err := f.svc.CreateAPIKey(f.ctx, f.alice, "x", nil, nil, nil)
	wantKind(t, err, core.ErrInvalid)
	k, token := ck2(f.svc.CreateAPIKey(f.ctx, f.alice, "script", []string{"view", "download"}, []string{f.home.ID}, nil))
	if !strings.HasPrefix(token, core.APIKeyPrefix) {
		t.Fatalf("token %q", token)
	}
	p := ck(f.svc.APIKeyPrincipal(f.ctx, token))
	// limited to its permissions, spaces, and without instance rights or key management
	ck(f.svc.GetDocument(f.ctx, p, doc.File.DocumentID))
	_, err = f.svc.PutFile(f.ctx, p, f.home.ID, []string{"x.txt"}, strings.NewReader("x"), core.PutOptions{})
	wantKind(t, err, core.ErrForbidden)
	other := ck(f.svc.CreateSpace(f.ctx, f.alice, "andere", "", ""))
	if spaceFor(t, f, p, other.ID) != nil {
		t.Fatal("key sees a space outside its scope")
	}
	if p.Inst != 0 {
		t.Fatalf("key carries instance permissions: %v", p.Inst.Names())
	}
	_, _, err = f.svc.CreateAPIKey(f.ctx, p, "y", []string{"view"}, nil, nil)
	wantKind(t, err, core.ErrForbidden)
	// a key whose only space is gone covers nothing, not everything
	empty := ck(f.svc.CreateSpace(f.ctx, f.alice, "kurzlebig", "", ""))
	_, tok2 := ck2(f.svc.CreateAPIKey(f.ctx, f.alice, "s", []string{"view"}, []string{empty.ID}, nil))
	noErr(t, f.svc.DeleteSpace(f.ctx, f.alice, empty.ID))
	if sp := spaceFor(t, f, ck(f.svc.APIKeyPrincipal(f.ctx, tok2)), f.home.ID); sp != nil {
		t.Fatal("a key whose space was deleted reaches other spaces")
	}
	_, all := ck2(f.svc.CreateAPIKey(f.ctx, f.alice, "a", []string{"view"}, nil, nil))
	if spaceFor(t, f, ck(f.svc.APIKeyPrincipal(f.ctx, all)), f.home.ID) == nil {
		t.Fatal("a key for all spaces")
	}

	// never more than the user: losing the space ends the key's access there
	noErr(t, f.svc.TransferSpace(f.ctx, f.alice, f.home.ID, "bob", "", "", f.home.Slug))
	_, err = f.svc.GetDocument(f.ctx, p, doc.File.DocumentID)
	wantKind(t, err, core.ErrNotFound)
	// wrong secret, revoked, expired
	_, err = f.svc.APIKeyPrincipal(f.ctx, core.APIKeyPrefix+k.ID+"_wrong")
	wantKind(t, err, core.ErrUnauthorized)
	noErr(t, f.svc.RevokeAPIKey(f.ctx, f.alice, k.ID))
	_, err = f.svc.APIKeyPrincipal(f.ctx, token)
	wantKind(t, err, core.ErrUnauthorized)
	past := time.Now().Add(-time.Hour)
	_, _, err = f.svc.CreateAPIKey(f.ctx, f.alice, "old", []string{"view"}, nil, &past)
	wantKind(t, err, core.ErrInvalid)
}

func TestSpaceSettings(t *testing.T) {
	f := newFixture(t)
	admin := session(t, f.svc, "admin", "supersecret")
	on := true
	ck(f.svc.UpdateSettings(f.ctx, admin, core.SettingsUpdate{PublicLinks: &on}))
	doc := f.put(t, f.alice, f.home.ID, "/a.txt", "text")

	// everything off by default: no way of sharing by link or file
	if st := spaceFor(t, f, f.alice, f.home.ID).Settings; st != (core.SpaceSettings{WritePolicy: core.WriteOpen}) {
		t.Fatalf("defaults %+v", st)
	}
	_, _, err := f.svc.CreateShareLink(f.ctx, f.alice, core.NewShareLink{SpaceID: f.home.ID, Kind: "public", Preset: "view"})
	wantKind(t, err, core.ErrForbidden)
	_, err = f.svc.SetDocumentGrant(f.ctx, f.alice, doc.File.DocumentID, "bob", "", "viewer")
	wantKind(t, err, core.ErrForbidden)
	// sharing with people on the space itself doesn't need a setting; a
	// Manager can still be granted although links are off
	ck(f.svc.SetGrant(f.ctx, f.alice, f.home.ID, "bob", "", "manager"))
	if sp := spaceFor(t, f, f.bob, f.home.ID); sp.Role != "manager" {
		t.Fatalf("label follows what is held: %+v", sp)
	}
	wantKind(t, errOf(f.svc.UpdateSpaceSettings(f.ctx, f.alice, f.bhome.ID, core.SpaceSettings{})), core.ErrNotFound)

	// enabled: works; off again: existing links and file shares stop at once
	allowSharing(t, f, f.home.ID)
	l, token := ck2(f.svc.CreateShareLink(f.ctx, f.alice, core.NewShareLink{SpaceID: f.home.ID, Kind: "public", Preset: "view"}))
	_, sess := ck2(f.svc.OpenShare(f.ctx, token, "", "", nil))
	ck(f.svc.SetDocumentGrant(f.ctx, f.alice, doc.File.DocumentID, "bob", "", "viewer"))
	carol := ck(f.svc.CreateUser(f.ctx, admin, "carol", "carolpass", "", false))
	ck(f.svc.SetDocumentGrant(f.ctx, f.alice, doc.File.DocumentID, carol.Username, "", "viewer"))
	cp := session(t, f.svc, "carol", "carolpass")
	ck(f.svc.GetDocument(f.ctx, cp, doc.File.DocumentID))
	ck(f.svc.UpdateSpaceSettings(f.ctx, f.alice, f.home.ID, core.SpaceSettings{}))
	_, err = f.svc.ResolveShare(f.ctx, l.ID, sess, nil)
	wantKind(t, err, core.ErrNotFound)
	_, err = f.svc.GetDocument(f.ctx, cp, doc.File.DocumentID)
	wantKind(t, err, core.ErrNotFound)

	// write protection: nobody writes, the owner neither, until it is switched off
	ck(f.svc.UpdateSpaceSettings(f.ctx, f.alice, f.home.ID, core.SpaceSettings{WritePolicy: core.WriteReadOnly}))
	_, err = f.svc.PutFile(f.ctx, f.alice, f.home.ID, []string{"x.txt"}, strings.NewReader("x"), core.PutOptions{})
	wantKind(t, err, core.ErrForbidden)
	_, err = f.svc.AddDocumentVersion(f.ctx, f.alice, doc.File.DocumentID, strings.NewReader("y"), "web")
	wantKind(t, err, core.ErrForbidden)
	wantKind(t, f.svc.DeleteLink(f.ctx, f.alice, doc.File.LinkID), core.ErrForbidden)
	ck(f.svc.GetDocument(f.ctx, f.alice, doc.File.DocumentID)) // reading goes on
	sp := spaceFor(t, f, f.alice, f.home.ID)
	if sp.Role != "owner" || slices.Contains(sp.Caps, "write") || !slices.Contains(sp.Caps, "manage") {
		t.Fatalf("write protected %+v", sp)
	}
	ck(f.svc.UpdateSpaceSettings(f.ctx, f.alice, f.home.ID, core.SpaceSettings{WritePolicy: core.WriteOpen}))
	f.put(t, f.alice, f.home.ID, "/x.txt", "x")

	// OCR: content is processed once, so one space allowing it is enough
	same := f.put(t, f.alice, f.home.ID, "/scan.pdf", "%PDF same scan")
	other := ck(f.svc.CreateSpace(f.ctx, f.alice, "zweite", "", ""))
	f.put(t, f.alice, other.ID, "/scan.pdf", "%PDF same scan")
	if !ck(f.svc.OCRAllowed(f.ctx, same.File.SHA256)) {
		t.Fatal("OCR on by default")
	}
	ck(f.svc.UpdateSpaceSettings(f.ctx, f.alice, f.home.ID, core.SpaceSettings{NoOCR: true}))
	if !ck(f.svc.OCRAllowed(f.ctx, same.File.SHA256)) {
		t.Fatal("the other space still allows it")
	}
	ck(f.svc.UpdateSpaceSettings(f.ctx, f.alice, other.ID, core.SpaceSettings{NoOCR: true}))
	if ck(f.svc.OCRAllowed(f.ctx, same.File.SHA256)) {
		t.Fatal("no space allows it")
	}
}

func TestPurgeVersion(t *testing.T) {
	f := newFixture(t)
	doc := f.put(t, f.alice, f.home.ID, "/a.txt", "harmless")
	id := doc.File.DocumentID
	ck(f.svc.AddDocumentVersion(f.ctx, f.alice, id, strings.NewReader("my secret password"), "web"))
	secret := ck(f.svc.GetDocument(f.ctx, f.alice, id)).SHA256
	ck(f.svc.AddDocumentVersion(f.ctx, f.alice, id, strings.NewReader("clean again"), "web"))
	ck(f.svc.RestoreVersion(f.ctx, f.alice, id, 2)) // v4 restored from v2, same content
	ck(f.svc.AddDocumentVersion(f.ctx, f.alice, id, strings.NewReader("final"), "web"))

	ck(f.svc.SetGrant(f.ctx, f.alice, f.home.ID, "bob", "", "editor"))
	wantKind(t, f.svc.PurgeVersion(f.ctx, f.bob, id, 2), core.ErrForbidden)
	wantKind(t, f.svc.PurgeVersion(f.ctx, f.alice, id, 5), core.ErrInvalid) // current
	wantKind(t, f.svc.PurgeVersion(f.ctx, f.alice, id, 99), core.ErrNotFound)
	noErr(t, f.svc.PurgeVersion(f.ctx, f.alice, id, 2))
	if !f.svc.Blobs.Exists(secret) { // v4 still has the same content
		t.Fatal("content still used by v4 was removed")
	}
	noErr(t, f.svc.PurgeVersion(f.ctx, f.alice, id, 4))
	if f.svc.Blobs.Exists(secret) {
		t.Fatal("purged content is still stored")
	}
	d := ck(f.svc.GetDocument(f.ctx, f.alice, id))
	if len(d.Versions) != 3 || d.Version != 5 {
		t.Fatalf("versions %+v", d.Versions)
	}
	var logged int
	noErr(t, f.svc.DB.QueryRow(f.ctx, `SELECT count(*) FROM purge_log WHERE document_id=$1`, id).Scan(&logged))
	if logged != 2 {
		t.Fatalf("purge log: %d", logged)
	}
}

func TestAuditTrail(t *testing.T) {
	f := newFixture(t)
	admin := session(t, f.svc, "admin", "supersecret")
	_, err := f.svc.ListAuditEvents(f.ctx, f.alice, "", 10)
	wantKind(t, err, core.ErrForbidden)
	g := ck(f.svc.SetGrant(f.ctx, f.alice, f.home.ID, "bob", "", "viewer"))
	noErr(t, f.svc.DeleteGrant(f.ctx, f.alice, f.home.ID, g.ID))
	ev := ck(f.svc.ListAuditEvents(f.ctx, admin, "", 2))
	if len(ev) != 2 || ev[0].Action != "grant.delete" || ev[1].Action != "grant.set" || ev[0].Actor != "alice" || ev[1].Details["role"] != "viewer" {
		t.Fatalf("events %+v", ev)
	}
	// append-only
	if _, err := f.svc.DB.Exec(f.ctx, `DELETE FROM audit_events`); err == nil {
		t.Fatal("audit events can be deleted")
	}
	if _, err := f.svc.DB.Exec(f.ctx, `UPDATE audit_events SET actor='x'`); err == nil {
		t.Fatal("audit events can be changed")
	}
	// a failed change leaves no event
	before := len(ck(f.svc.ListAuditEvents(f.ctx, admin, "", 500)))
	_, _ = f.svc.SetGrant(f.ctx, f.alice, f.home.ID, "bob", "", "nonsense")
	if after := len(ck(f.svc.ListAuditEvents(f.ctx, admin, "", 500))); after != before {
		t.Fatalf("%d -> %d", before, after)
	}
}

func TestIdentityBoundToIssuer(t *testing.T) {
	f := newFixture(t)
	admin := session(t, f.svc, "admin", "supersecret")
	ck(f.svc.LoginExternal(f.ctx, "google", "https://accounts.google.com", "same-sub", "a@example.org", ""))
	for _, u := range ck(f.svc.ListUsers(f.ctx, admin)) {
		if u.Status == "pending" {
			ck(f.svc.ApproveUser(f.ctx, admin, u.ID))
		}
	}
	if res := ck(f.svc.LoginExternal(f.ctx, "google", "https://accounts.google.com", "same-sub", "", "")); res.Principal == nil {
		t.Fatal("same issuer signs in")
	}
	// the same subject from another issuer is someone else
	if res := ck(f.svc.LoginExternal(f.ctx, "google", "https://evil.example", "same-sub", "", "")); !res.Pending || res.Principal != nil {
		t.Fatalf("other issuer: %+v", res)
	}
}

func TestDatabaseRefusesFolderCycle(t *testing.T) {
	f := newFixture(t)
	f.put(t, f.alice, f.home.ID, "/a/b/x.txt", "x")
	a := ck(f.svc.FolderByPath(f.ctx, f.alice, f.home.ID, []string{"a"}))
	b := ck(f.svc.FolderByPath(f.ctx, f.alice, f.home.ID, []string{"a", "b"}))
	// what two concurrent moves could leave behind
	if _, err := f.svc.DB.Exec(f.ctx, `UPDATE entries SET parent_id=$1 WHERE id=$2`, b.ID, a.ID); err == nil {
		t.Fatal("a folder cycle was accepted")
	}
	if f.svc.DB.QueryRow(f.ctx, `SELECT folder_path($1)`, a.ID).Scan(new(string)) != nil {
		t.Fatal("folder_path")
	}
}

func TestSharesEndOnCrossSpaceMove(t *testing.T) {
	f := newFixture(t)
	admin := session(t, f.svc, "admin", "supersecret")
	on := true
	ck(f.svc.UpdateSettings(f.ctx, admin, core.SettingsUpdate{PublicLinks: &on}))
	allowSharing(t, f, f.home.ID)
	doc := f.put(t, f.alice, f.home.ID, "/a.txt", "text")
	ck(f.svc.SetDocumentGrant(f.ctx, f.alice, doc.File.DocumentID, "bob", "", "viewer"))
	_, token := ck2(f.svc.CreateShareLink(f.ctx, f.alice, core.NewShareLink{DocumentID: doc.File.DocumentID, Kind: "public", Preset: "view"}))
	other := ck(f.svc.CreateSpace(f.ctx, f.alice, "ziel", "", ""))
	allowSharing(t, f, other.ID)
	root := ck(f.svc.FolderByPath(f.ctx, f.alice, other.ID, nil))
	ck(f.svc.MoveLink(f.ctx, f.alice, doc.File.LinkID, root.ID, "", false))
	_, err := f.svc.GetDocument(f.ctx, f.bob, doc.File.DocumentID)
	wantKind(t, err, core.ErrNotFound)
	_, _, err = f.svc.OpenShare(f.ctx, token, "", "", nil)
	wantKind(t, err, core.ErrNotFound)
}

func TestLinkAndKeyFoldersMustBeFoldersOfTheSpace(t *testing.T) {
	f := newFixture(t)
	doc := f.put(t, f.alice, f.home.ID, "/d/a.txt", "x")
	other := ck(f.svc.FolderByPath(f.ctx, f.bob, f.bhome.ID, nil))
	for name, sql := range map[string]string{
		"file as link target": `INSERT INTO share_links(token_hash, space_id, folder_id, kind, preset, created_by) SELECT sha256('a')::bytea, $1, $2, 'drop', 'drop', owner_user_id FROM spaces WHERE id=$1`,
	} {
		if _, err := f.svc.DB.Exec(f.ctx, sql, f.home.ID, doc.File.LinkID); err == nil {
			t.Fatal(name)
		}
	}
	if _, err := f.svc.DB.Exec(f.ctx, `INSERT INTO share_links(token_hash, space_id, folder_id, kind, preset, created_by) SELECT sha256('b')::bytea, $1, $2, 'drop', 'drop', owner_user_id FROM spaces WHERE id=$1`, f.home.ID, other.ID); err == nil {
		t.Fatal("a folder of another space")
	}
}

// Every column of a table that belongs in a server backup must be exported:
// a new column fails here until backup.go handles it (or it is listed).
func TestBackupCoversEveryColumn(t *testing.T) {
	f := newFixture(t)
	notBackedUp := map[string]bool{ // transient state or rebuilt on restore
		"sessions": true, "share_sessions": true, "wopi_tokens": true, "office_sessions": true, "oauth_logins": true,
		"edit_leases": true, "jobs": true, "multipart_uploads": true, "multipart_parts": true, "transfers": true, "backups": true,
		"permissions": true, "schema_migrations": true, "instance_settings": false,
	}
	var src strings.Builder
	for _, name := range []string{"backup.go", "bundle.go"} {
		b, err := os.ReadFile(name)
		noErr(t, err)
		src.Write(b)
	}
	code := src.String()
	rows, err := f.svc.DB.Query(f.ctx, `SELECT c.table_name, c.column_name FROM information_schema.columns c
		JOIN information_schema.tables t USING (table_schema, table_name)
		WHERE c.table_schema = 'public' AND t.table_type = 'BASE TABLE' AND c.is_generated = 'NEVER' ORDER BY 1, 2`)
	noErr(t, err)
	defer rows.Close()
	for rows.Next() {
		var table, col string
		noErr(t, rows.Scan(&table, &col))
		if notBackedUp[table] || col == "singleton" || (table == "blob_texts" && col == "tsv") || strings.HasSuffix(col, "_kind") {
			continue
		}
		if !strings.Contains(code, table) {
			t.Errorf("table %s is not in the backup; add it, or list it as not backed up", table)
			continue
		}
		if !regexp.MustCompile(`\b` + col + `\b`).MatchString(code) {
			t.Errorf("column %s.%s is not in the backup", table, col)
		}
	}
	noErr(t, rows.Err())
}
