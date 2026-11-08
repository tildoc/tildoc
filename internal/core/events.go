package core

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"sync"
	"time"
)

// Event is what a subscriber (the web UI) receives. Events only say what
// changed; clients reload the data through the normal, permission-checked
// API.
type Event struct {
	Type string `json:"type"` // space | transfer | resync
	// space: something in the space changed; Tree when folders changed,
	// Documents lists changed documents when known.
	SpaceID   string   `json:"space_id,omitempty"`
	Tree      bool     `json:"tree,omitempty"`
	Documents []string `json:"documents,omitempty"`
	// transfer: status of one of the subscriber's own transfers.
	TransferID string  `json:"transfer_id,omitempty"`
	Status     string  `json:"status,omitempty"`
	Progress   float64 `json:"progress,omitempty"`
}

// ErrTooManyStreams is returned when a user has too many open event streams.
var ErrTooManyStreams = &Error{Kind: ErrTooMany, Msg: "too many event streams"}

const (
	eventChannel       = "tildoc_events"
	eventFlushInterval = 250 * time.Millisecond
	eventReadableTTL   = 30 * time.Second
	maxStreamsPerUser  = 8
	subscriberBuffer   = 32
)

// rawEvent is a notification payload written by the triggers in 0001_init.sql.
type rawEvent struct {
	T        string  `json:"t"`
	ID       string  `json:"id"`
	Doc      *string `json:"doc"`
	Tree     bool    `json:"tree"`
	SHA      string  `json:"sha"`
	User     string  `json:"user"`
	Status   string  `json:"status"`
	Progress float64 `json:"progress"`
}

// Subscription receives events for one principal.
type Subscription struct {
	C          <-chan []Event
	c          chan []Event
	p          *Principal
	hub        *EventHub
	readable   map[string]bool
	readableAt time.Time
	lost       bool
}

// Close unsubscribes.
func (sub *Subscription) Close() { sub.hub.unsubscribe(sub) }

// EventHub fans PostgreSQL notifications out to subscribers. It coalesces
// notifications for eventFlushInterval and sends each subscriber only events
// of spaces it can read and of its own transfers.
type EventHub struct {
	svc     *Service
	mu      sync.Mutex
	subs    map[*Subscription]struct{}
	perUser map[string]int
	raw     chan rawEvent
	resync  chan struct{}
	ready   chan struct{} // closed once the first LISTEN is active
}

// NewEventHub creates a hub; Run must be started for events to flow.
func (s *Service) NewEventHub() *EventHub {
	return &EventHub{svc: s, subs: map[*Subscription]struct{}{}, perUser: map[string]int{},
		raw: make(chan rawEvent, 4096), resync: make(chan struct{}, 1), ready: make(chan struct{})}
}

// Ready is closed once the hub listens (for tests and health checks).
func (h *EventHub) Ready() <-chan struct{} { return h.ready }

// Subscribe registers a principal.
func (h *EventHub) Subscribe(p *Principal) (*Subscription, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.perUser[p.UserID] >= maxStreamsPerUser {
		return nil, ErrTooManyStreams
	}
	c := make(chan []Event, subscriberBuffer)
	sub := &Subscription{C: c, c: c, p: p, hub: h}
	h.subs[sub] = struct{}{}
	h.perUser[p.UserID]++
	return sub, nil
}

func (h *EventHub) unsubscribe(sub *Subscription) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.subs[sub]; !ok {
		return
	}
	delete(h.subs, sub)
	if h.perUser[sub.p.UserID]--; h.perUser[sub.p.UserID] <= 0 {
		delete(h.perUser, sub.p.UserID)
	}
}

// Run listens for notifications until ctx ends, reconnecting on errors.
// After a reconnect every subscriber gets a resync (events may be lost).
func (h *EventHub) Run(ctx context.Context) {
	go h.dispatch(ctx)
	first := true
	for ctx.Err() == nil {
		if err := h.listen(ctx, &first); err != nil && ctx.Err() == nil {
			h.svc.Log.Warn("event listener", "err", err)
			select {
			case <-ctx.Done():
			case <-time.After(time.Second):
			}
		}
	}
}

func (h *EventHub) listen(ctx context.Context, first *bool) error {
	conn, err := h.svc.DB.Acquire(ctx)
	if err != nil {
		return err
	}
	defer func() {
		conn.Conn().Close(context.Background())
		conn.Release()
	}()
	if _, err := conn.Exec(ctx, "LISTEN "+eventChannel); err != nil {
		return err
	}
	if *first {
		close(h.ready)
	} else {
		select {
		case h.resync <- struct{}{}:
		default:
		}
	}
	*first = false
	for {
		n, err := conn.Conn().WaitForNotification(ctx)
		if err != nil {
			return err
		}
		var ev rawEvent
		if json.Unmarshal([]byte(n.Payload), &ev) != nil {
			continue
		}
		select {
		case h.raw <- ev:
		default: // dispatcher is far behind: tell everyone to reload
			select {
			case h.resync <- struct{}{}:
			default:
			}
		}
	}
}

func (h *EventHub) dispatch(ctx context.Context) {
	var batch []rawEvent
	t := time.NewTicker(eventFlushInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-h.raw:
			batch = append(batch, ev)
		case <-h.resync:
			batch = nil
			h.broadcastResync()
		case <-t.C:
			if len(batch) > 0 {
				h.flush(ctx, batch)
				batch = nil
			}
		}
	}
}

func (h *EventHub) broadcastResync() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for sub := range h.subs {
		sub.send([]Event{{Type: "resync"}})
	}
}

// send delivers without blocking; a subscriber that cannot keep up gets a
// resync with its next delivery instead of the lost events.
func (sub *Subscription) send(evs []Event) {
	if sub.lost {
		evs = []Event{{Type: "resync"}}
	}
	select {
	case sub.c <- evs:
		sub.lost = false
	default:
		sub.lost = true
	}
}

type spaceChange struct {
	tree bool
	docs map[string]bool
}

// flush merges a batch into per-space changes and transfer updates and
// delivers them to the subscribers allowed to see them.
func (h *EventHub) flush(ctx context.Context, batch []rawEvent) {
	spaces := map[string]*spaceChange{}
	change := func(id string) *spaceChange {
		c := spaces[id]
		if c == nil {
			c = &spaceChange{docs: map[string]bool{}}
			spaces[id] = c
		}
		return c
	}
	transfers := map[string]rawEvent{}
	var shas [][]byte
	for _, ev := range batch {
		switch ev.T {
		case "space":
			c := change(ev.ID)
			c.tree = c.tree || ev.Tree
			if ev.Doc != nil {
				c.docs[*ev.Doc] = true
			}
		case "blob":
			if b, err := hex.DecodeString(ev.SHA); err == nil && len(b) == 32 {
				shas = append(shas, b)
			}
		case "transfer":
			transfers[ev.ID] = ev
		}
	}
	if len(shas) > 0 {
		// documents whose current version is (or derives from) a processed blob
		rows, err := h.svc.DB.Query(ctx, `
			SELECT DISTINCT d.space_id::text, d.id::text FROM documents d
			JOIN document_versions v ON v.document_id = d.id AND v.version = d.current_version
			WHERE v.sha256 = ANY($1)
			   OR v.sha256 IN (SELECT source_sha256 FROM blob_derivatives WHERE derived_sha256 = ANY($1))`, shas)
		if err == nil {
			for rows.Next() {
				var sid, did string
				if rows.Scan(&sid, &did) == nil {
					change(sid).docs[did] = true
				}
			}
			rows.Close()
		}
	}
	if len(spaces) == 0 && len(transfers) == 0 {
		return
	}

	h.mu.Lock()
	subs := make([]*Subscription, 0, len(h.subs))
	for sub := range h.subs {
		subs = append(subs, sub)
	}
	h.mu.Unlock()

	for _, sub := range subs {
		var out []Event
		if len(spaces) > 0 {
			readable := h.readable(ctx, sub)
			for sid, c := range spaces {
				if !readable[sid] {
					continue
				}
				ev := Event{Type: "space", SpaceID: sid, Tree: c.tree}
				for d := range c.docs {
					ev.Documents = append(ev.Documents, d)
				}
				out = append(out, ev)
			}
		}
		for _, t := range transfers {
			if t.User == sub.p.UserID {
				out = append(out, Event{Type: "transfer", TransferID: t.ID, Status: t.Status, Progress: t.Progress})
			}
		}
		if len(out) > 0 {
			h.mu.Lock()
			if _, ok := h.subs[sub]; ok {
				sub.send(out)
			}
			h.mu.Unlock()
		}
	}
}

// readable returns the subscriber's readable spaces, cached for
// eventReadableTTL (grants changed meanwhile take effect after the TTL or
// on reconnect).
func (h *EventHub) readable(ctx context.Context, sub *Subscription) map[string]bool {
	if sub.readable != nil && time.Since(sub.readableAt) < eventReadableTTL {
		return sub.readable
	}
	ids, err := h.svc.readableSpaceIDs(ctx, sub.p)
	if err != nil {
		if sub.readable == nil {
			return map[string]bool{}
		}
		return sub.readable
	}
	m := make(map[string]bool, len(ids))
	for _, id := range ids {
		m[id] = true
	}
	sub.readable, sub.readableAt = m, time.Now()
	return m
}
