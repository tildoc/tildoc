// Package auth contains protocol independent authentication helpers.
package auth

import (
	"sync"
	"time"
)

// Limiter throttles failed authentication attempts per key (e.g. IP or
// IP+login). After Burst failures within Window further attempts are
// rejected until the window has passed.
type Limiter struct {
	Burst  int
	Window time.Duration

	mu    sync.Mutex
	fails map[string][]time.Time
	last  time.Time
}

// NewLimiter returns a limiter allowing burst failures per window.
func NewLimiter(burst int, window time.Duration) *Limiter {
	return &Limiter{Burst: burst, Window: window, fails: map[string][]time.Time{}}
}

func (l *Limiter) prune(now time.Time, key string) []time.Time {
	ts := l.fails[key]
	i := 0
	for i < len(ts) && now.Sub(ts[i]) > l.Window {
		i++
	}
	ts = ts[i:]
	if len(ts) == 0 {
		delete(l.fails, key)
	} else {
		l.fails[key] = ts
	}
	return ts
}

// Allowed reports whether another attempt for key may proceed.
func (l *Limiter) Allowed(keys ...string) bool {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	if now.Sub(l.last) > l.Window {
		for k := range l.fails {
			l.prune(now, k)
		}
		l.last = now
	}
	for _, k := range keys {
		if len(l.prune(now, k)) >= l.Burst {
			return false
		}
	}
	return true
}

// Fail records a failed attempt for all keys.
func (l *Limiter) Fail(keys ...string) {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, k := range keys {
		l.fails[k] = append(l.prune(now, k), now)
	}
}

// Success clears failures for the given keys.
func (l *Limiter) Success(keys ...string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, k := range keys {
		delete(l.fails, k)
	}
}
