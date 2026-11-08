package api

import (
	"encoding/json"
	"net/http"
	"time"
)

const (
	eventHeartbeat   = 25 * time.Second
	eventMaxLifetime = 5 * time.Minute // reconnect re-authenticates and refreshes grants
	eventWriteWait   = 10 * time.Second
)

// events streams live changes as Server-Sent Events (GET /api/events).
// Events carry only ids; clients reload through the permission-checked API.
func (s *Server) events(w http.ResponseWriter, r *http.Request) error {
	p := principal(r)
	sub, err := s.Events.Subscribe(p)
	if err != nil {
		w.Header().Set("Retry-After", "30")
		return err
	}
	defer sub.Close()

	rc := http.NewResponseController(w)
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	write := func(b []byte) bool {
		rc.SetWriteDeadline(time.Now().Add(eventWriteWait))
		if _, err := w.Write(b); err != nil {
			return false
		}
		return rc.Flush() == nil
	}
	// retry: reconnect delay for EventSource; "ready" tells the client it is live
	if !write([]byte("retry: 3000\nevent: ready\ndata: {}\n\n")) {
		return nil
	}
	beat := time.NewTicker(eventHeartbeat)
	defer beat.Stop()
	end := time.NewTimer(eventMaxLifetime)
	defer end.Stop()
	for {
		select {
		case <-r.Context().Done():
			return nil
		case <-s.stop:
			return nil
		case <-end.C:
			return nil
		case <-beat.C:
			if !write([]byte(": ping\n\n")) {
				return nil
			}
		case evs := <-sub.C:
			b, err := json.Marshal(evs)
			if err != nil {
				return nil
			}
			if !write(append(append([]byte("data: "), b...), '\n', '\n')) {
				return nil
			}
		}
	}
}

// StopStreams ends all event streams (call on shutdown, streams never end
// on their own within http.Server.Shutdown's grace period).
func (s *Server) StopStreams() {
	s.stopOnce.Do(func() { close(s.stop) })
}
