package core_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/tildoc/tildoc/internal/core"
	"github.com/tildoc/tildoc/internal/testutil"
)

// ck panics on error; the panic fails the test with a stack trace.
func ck[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func ck2[A, B any](a A, b B, err error) (A, B) {
	if err != nil {
		panic(err)
	}
	return a, b
}

func noErr(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func wantKind(t testing.TB, err, kind error) {
	t.Helper()
	if !errors.Is(err, kind) {
		t.Fatalf("want %v, got %v", kind, err)
	}
}

func session(t testing.TB, svc *core.Service, user, pw string) *core.Principal {
	t.Helper()
	p := ck(svc.Authenticate(context.Background(), user, pw))
	p.Via = "session"
	return p
}

func TestSetupAndUsers(t *testing.T) {
	env := testutil.New(t)
	ctx := context.Background()
	svc := env.Svc

	need := ck(svc.NeedsSetup(ctx))
	if !need {
		t.Fatal("expected setup needed")
	}
	admin := ck(svc.SetupAdmin(ctx, "Admin", "supersecret", "The Admin"))
	if admin.Username != "admin" || !admin.IsAdmin {
		t.Fatalf("admin: %+v", admin)
	}
	_, err := svc.SetupAdmin(ctx, "other", "supersecret", "")
	wantKind(t, err, core.ErrForbidden)

	ap := session(t, svc, "admin", "supersecret")
	_, err = svc.Authenticate(ctx, "admin", "wrong")
	wantKind(t, err, core.ErrUnauthorized)
	_, err = svc.Authenticate(ctx, "nobody", "whatever1")
	wantKind(t, err, core.ErrUnauthorized)

	alice := ck(svc.CreateUser(ctx, ap, "alice", "alicepass", "", false))
	_, err = svc.CreateUser(ctx, ap, "alice", "alicepass", "", false)
	wantKind(t, err, core.ErrConflict)
	_, err = svc.CreateUser(ctx, ap, "bad name", "alicepass", "", false)
	wantKind(t, err, core.ErrInvalid)
	_, err = svc.CreateUser(ctx, ap, "bob", "short", "", false)
	wantKind(t, err, core.ErrInvalid)

	alp := session(t, svc, "alice", "alicepass")
	_, err = svc.CreateUser(ctx, alp, "eve", "evepass12", "", false)
	wantKind(t, err, core.ErrForbidden)

	// personal space created automatically
	spaces := ck(svc.ListSpaces(ctx, alp))
	if len(spaces) != 1 || spaces[0].Slug != "alice" || spaces[0].Perm != core.PermManage {
		t.Fatalf("spaces: %+v", spaces)
	}

	// sessions
	tok, _ := ck2(svc.CreateSession(ctx, alp))
	sp := ck(svc.SessionPrincipal(ctx, tok))
	if sp.UserID != alice.ID {
		t.Fatal("session user mismatch")
	}
	// disabling invalidates sessions
	dis := true
	ck(svc.UpdateUser(ctx, ap, alice.ID, core.UserUpdate{Disabled: &dis}))
	_, err = svc.SessionPrincipal(ctx, tok)
	wantKind(t, err, core.ErrUnauthorized)
	_, err = svc.Authenticate(ctx, "alice", "alicepass")
	wantKind(t, err, core.ErrUnauthorized)
	en := false
	ck(svc.UpdateUser(ctx, ap, alice.ID, core.UserUpdate{Disabled: &en}))

	// password change
	alp = session(t, svc, "alice", "alicepass")
	wantKind(t, svc.ChangePassword(ctx, alp, "wrongpass", "newpass123"), core.ErrForbidden)
	if err := svc.ChangePassword(ctx, alp, "alicepass", "newpass123"); err != nil {
		t.Fatal(err)
	}
	session(t, svc, "alice", "newpass123")

	// admin cannot delete self
	wantKind(t, svc.DeleteUser(ctx, ap, admin.ID), core.ErrInvalid)
}

func TestAccessKeysAndSSH(t *testing.T) {
	env := testutil.New(t)
	ctx := context.Background()
	svc := env.Svc
	ck(svc.SetupAdmin(ctx, "admin", "supersecret", ""))
	ap := session(t, svc, "admin", "supersecret")
	ck(svc.CreateUser(ctx, ap, "alice", "alicepass", "", false))
	alp := session(t, svc, "alice", "alicepass")
	home := ck(svc.ListSpaces(ctx, alp))[0]

	key, secret := ck2(svc.CreateAccessKey(ctx, alp, "laptop", "", false))
	if len(key.ID) != 20 || len(secret) != 40 {
		t.Fatalf("key format: %q %q", key.ID, secret)
	}
	gotSecret, kp := ck2(svc.AccessKeySecret(ctx, key.ID))
	if gotSecret != secret || kp.Username != "alice" || kp.Via != "accesskey" {
		t.Fatal("access key lookup mismatch")
	}
	// app password: username or key id as login
	ck(svc.AuthenticateAppPassword(ctx, "alice", secret))
	ck(svc.AuthenticateAppPassword(ctx, key.ID, secret))
	_, err := svc.AuthenticateAppPassword(ctx, "alice", "alicepass")
	wantKind(t, err, core.ErrUnauthorized)

	// keys cannot create keys
	_, _, err = svc.CreateAccessKey(ctx, kp, "x", "", false)
	wantKind(t, err, core.ErrForbidden)

	// scoped read-only key
	rk, rsec := ck2(svc.CreateAccessKey(ctx, alp, "ro", home.ID, true))
	_, rp := ck2(svc.AccessKeySecret(ctx, rk.ID))
	sp := ck(svc.GetSpace(ctx, rp, home.ID))
	if sp.Perm != core.PermRead {
		t.Fatalf("scoped perm: %v", sp.Perm)
	}
	_ = rsec

	if keys := ck(svc.ListAccessKeys(ctx, alp)); len(keys) != 2 {
		t.Fatalf("keys: %d", len(keys))
	}
	if err := svc.RevokeAccessKey(ctx, alp, key.ID); err != nil {
		t.Fatal(err)
	}
	_, _, err = svc.AccessKeySecret(ctx, key.ID)
	wantKind(t, err, core.ErrUnauthorized)

	// ssh keys
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	spub, _ := ssh.NewPublicKey(pub)
	line := string(ssh.MarshalAuthorizedKey(spub))
	k := ck(svc.AddSSHKey(ctx, alp, line, "work"))
	if k.Type != "ssh-ed25519" {
		t.Fatalf("type %q", k.Type)
	}
	_, err = svc.AddSSHKey(ctx, alp, line, "dup")
	wantKind(t, err, core.ErrConflict)
	p := ck(svc.SSHKeyPrincipal(ctx, "alice", spub))
	if p.Username != "alice" {
		t.Fatal("ssh principal")
	}
	_, err = svc.SSHKeyPrincipal(ctx, "admin", spub)
	wantKind(t, err, core.ErrUnauthorized)
	if err := svc.DeleteSSHKey(ctx, alp, k.ID); err != nil {
		t.Fatal(err)
	}
	_, err = svc.SSHKeyPrincipal(ctx, "alice", spub)
	wantKind(t, err, core.ErrUnauthorized)
}

func TestTeamsAndSpaces(t *testing.T) {
	env := testutil.New(t)
	ctx := context.Background()
	svc := env.Svc
	ck(svc.SetupAdmin(ctx, "admin", "supersecret", ""))
	ap := session(t, svc, "admin", "supersecret")
	alice := ck(svc.CreateUser(ctx, ap, "alice", "alicepass", "", false))
	bob := ck(svc.CreateUser(ctx, ap, "bob", "bobpass12", "", false))
	carol := ck(svc.CreateUser(ctx, ap, "carol", "carolpass", "", false))
	alp := session(t, svc, "alice", "alicepass")
	bp := session(t, svc, "bob", "bobpass12")
	cp := session(t, svc, "carol", "carolpass")

	team := ck(svc.CreateTeam(ctx, alp, "Finance"))
	_, err := svc.CreateTeam(ctx, bp, "Finance")
	wantKind(t, err, core.ErrConflict)

	ts := ck(svc.CreateSpace(ctx, alp, "finance", "Finance Docs", team.ID))
	if ts.OwnerTeamID == nil || ts.Perm != core.PermManage {
		t.Fatalf("team space: %+v", ts)
	}
	// bob not a member: not visible
	_, err = svc.GetSpace(ctx, bp, ts.ID)
	wantKind(t, err, core.ErrNotFound)

	if err := svc.SetMember(ctx, alp, team.ID, bob.ID, "member"); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetMember(ctx, alp, team.ID, carol.ID, "viewer"); err != nil {
		t.Fatal(err)
	}
	if sp := ck(svc.GetSpace(ctx, bp, "finance")); sp.Perm != core.PermWrite {
		t.Fatalf("bob perm %v", sp.Perm)
	}
	if sp := ck(svc.GetSpace(ctx, cp, ts.ID)); sp.Perm != core.PermRead {
		t.Fatalf("carol perm %v", sp.Perm)
	}
	// member cannot manage members or create team spaces
	wantKind(t, svc.SetMember(ctx, bp, team.ID, carol.ID, "admin"), core.ErrForbidden)
	_, err = svc.CreateSpace(ctx, bp, "finance-2", "", team.ID)
	wantKind(t, err, core.ErrForbidden)
	// admin cannot create owners
	noErr(t, svc.SetMember(ctx, alp, team.ID, bob.ID, "admin"))
	wantKind(t, svc.SetMember(ctx, bp, team.ID, carol.ID, "owner"), core.ErrForbidden)
	// last owner protection
	wantKind(t, svc.RemoveMember(ctx, alp, team.ID, alice.ID), core.ErrInvalid)
	wantKind(t, svc.SetMember(ctx, alp, team.ID, alice.ID, "admin"), core.ErrInvalid)
	// carol can leave
	if err := svc.RemoveMember(ctx, cp, team.ID, carol.ID); err != nil {
		t.Fatal(err)
	}
	_, err = svc.GetSpace(ctx, cp, ts.ID)
	wantKind(t, err, core.ErrNotFound)

	// slug validation and uniqueness
	_, err = svc.CreateSpace(ctx, alp, "Bad_Slug", "", "")
	wantKind(t, err, core.ErrInvalid)
	_, err = svc.CreateSpace(ctx, bp, "finance", "", "")
	wantKind(t, err, core.ErrConflict)
	_, err = svc.CreateSpace(ctx, bp, "api", "", "")
	wantKind(t, err, core.ErrInvalid)
	ps := ck(svc.CreateSpace(ctx, bp, "", "Bob's Archive 2024", ""))
	if ps.Slug != "bob-s-archive-2024" {
		t.Fatalf("slugify: %q", ps.Slug)
	}
	// delete empty space; last personal space protected
	if err := svc.DeleteSpace(ctx, bp, ps.ID); err != nil {
		t.Fatal(err)
	}
	home := ck(svc.GetSpace(ctx, bp, "bob"))
	wantKind(t, svc.DeleteSpace(ctx, bp, home.ID), core.ErrInvalid)

	teams := ck(svc.ListTeams(ctx, bp))
	if len(teams) != 1 || teams[0].Role != "admin" {
		t.Fatalf("teams: %+v", teams)
	}
	if err := svc.DeleteTeam(ctx, alp, team.ID); err != nil {
		t.Fatal(err)
	}
	_, err = svc.GetSpace(ctx, alp, ts.ID)
	wantKind(t, err, core.ErrNotFound)
}

func TestNewSecretAlphabet(t *testing.T) {
	for i := 0; i < 2000; i++ {
		s := core.NewSecretForTest()
		if len(s) != 40 {
			t.Fatalf("len %d", len(s))
		}
		for _, r := range s {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
				t.Fatalf("secret %q has %q", s, r)
			}
		}
	}
}
