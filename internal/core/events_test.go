package core_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/tildoc/tildoc/internal/core"
)

func startHub(t *testing.T, f *fixture) *core.EventHub {
	t.Helper()
	hub := f.svc.NewEventHub()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { hub.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	select {
	case <-hub.Ready():
	case <-time.After(5 * time.Second):
		t.Fatal("event hub did not start listening")
	}
	return hub
}

// waitEvent reads deliveries until one event matches.
func waitEvent(t *testing.T, sub *core.Subscription, what string, match func(core.Event) bool) core.Event {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case evs := <-sub.C:
			for _, ev := range evs {
				if match(ev) {
					return ev
				}
			}
		case <-deadline:
			t.Fatalf("no event: %s", what)
		}
	}
}

// noEvent asserts that nothing arrives for a while (after the flush interval).
func noEvent(t *testing.T, sub *core.Subscription, what string) {
	t.Helper()
	select {
	case evs := <-sub.C:
		t.Fatalf("unexpected event (%s): %+v", what, evs)
	case <-time.After(700 * time.Millisecond):
	}
}

func TestEventsFilteredByPermission(t *testing.T) {
	f := newFixture(t)
	hub := startHub(t, f)
	as := ck(hub.Subscribe(f.alice))
	defer as.Close()
	bs := ck(hub.Subscribe(f.bob))
	defer bs.Close()

	// upload: alice hears about her space and the document, bob does not
	r := f.put(t, f.alice, f.home.ID, "/Docs/a.txt", "alpha")
	ev := waitEvent(t, as, "upload", func(e core.Event) bool {
		return e.Type == "space" && e.SpaceID == f.home.ID && slices.Contains(e.Documents, r.File.DocumentID)
	})
	if !ev.Tree {
		t.Fatal("creating a folder must mark the tree as changed")
	}
	noEvent(t, bs, "bob must not see alice's space")

	// processing status of the blob reaches the documents that use it
	noErr(t, f.svc.SetProcessing(f.ctx, r.File.SHA256, core.StatusDone, "text", nil, ""))
	waitEvent(t, as, "processing", func(e core.Event) bool {
		return e.Type == "space" && e.SpaceID == f.home.ID && slices.Contains(e.Documents, r.File.DocumentID)
	})
	noEvent(t, bs, "bob must not see processing in alice's space")

	// transfers go to their owner only
	alice := ck(f.svc.UserByName(f.ctx, "alice"))
	var tid string
	noErr(t, f.env.Pool.QueryRow(f.ctx, `INSERT INTO transfers(kind, user_id, file, size) VALUES('import_zip', $1, 'x', 0) RETURNING id::text`, alice.ID).Scan(&tid))
	waitEvent(t, as, "transfer", func(e core.Event) bool { return e.Type == "transfer" && e.TransferID == tid && e.Status == "queued" })
	noEvent(t, bs, "bob must not see alice's transfer")

	// sharing the space makes bob receive its events (after the cache TTL or
	// on a new subscription, as the browser does on reconnect)
	ck(f.svc.SetGrant(f.ctx, f.alice, f.home.ID, "bob", "", "viewer"))
	bs2 := ck(hub.Subscribe(f.bob))
	defer bs2.Close()
	f.put(t, f.alice, f.home.ID, "/b.txt", "beta")
	waitEvent(t, bs2, "shared space", func(e core.Event) bool { return e.Type == "space" && e.SpaceID == f.home.ID })
}

func TestEventsCoalesceAndLimit(t *testing.T) {
	f := newFixture(t)
	hub := startHub(t, f)
	sub := ck(hub.Subscribe(f.alice))
	defer sub.Close()

	// many changes in one transaction arrive as one event per space
	tx := ck(f.env.Pool.Begin(f.ctx))
	for range 50 {
		_, err := tx.Exec(f.ctx, `SELECT notify_space($1::uuid, NULL, true)`, f.home.ID)
		noErr(t, err)
	}
	noErr(t, tx.Commit(f.ctx))
	got := 0
	deadline := time.After(1500 * time.Millisecond)
loop:
	for {
		select {
		case evs := <-sub.C:
			for _, e := range evs {
				if e.Type == "space" && e.SpaceID == f.home.ID {
					got++
				}
			}
		case <-deadline:
			break loop
		}
	}
	if got != 1 {
		t.Fatalf("want 1 coalesced space event, got %d", got)
	}

	// at most 8 streams per user
	var subs []*core.Subscription
	for range 7 {
		subs = append(subs, ck(hub.Subscribe(f.alice)))
	}
	_, err := hub.Subscribe(f.alice)
	if !errors.Is(err, core.ErrTooMany) {
		t.Fatalf("9th stream: want ErrTooMany, got %v", err)
	}
	subs[0].Close()
	subs[0] = ck(hub.Subscribe(f.alice))
	for _, s := range subs {
		s.Close()
	}
}
