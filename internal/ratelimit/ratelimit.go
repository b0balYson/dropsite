// Package ratelimit implements in-memory sliding-window limits keyed by an
// arbitrary string (user ID, IP address). State is lost on restart, which is
// acceptable for a single-process deployment.
package ratelimit

import (
	"sync"
	"time"
)

// Window allows at most Max events per Period for each key.
type Window struct {
	max    int
	period time.Duration
	now    func() time.Time

	mu     sync.Mutex
	events map[string][]time.Time
	calls  int
}

// New returns a limiter allowing max events per period per key.
func New(max int, period time.Duration) *Window {
	return &Window{max: max, period: period, now: time.Now, events: map[string][]time.Time{}}
}

// WithClock replaces the time source (tests).
func (w *Window) WithClock(now func() time.Time) *Window {
	w.now = now
	return w
}

// sweepEvery bounds memory: every N calls, keys with only expired events are dropped.
const sweepEvery = 1024

// prune drops expired events for key and returns the live ones. Caller holds mu.
func (w *Window) prune(key string, now time.Time) []time.Time {
	cutoff := now.Add(-w.period)
	evs := w.events[key]
	i := 0
	for i < len(evs) && !evs[i].After(cutoff) {
		i++
	}
	if i == len(evs) {
		delete(w.events, key)
		return nil
	}
	evs = evs[i:]
	w.events[key] = evs
	return evs
}

func (w *Window) maybeSweep(now time.Time) {
	w.calls++
	if w.calls%sweepEvery != 0 {
		return
	}
	for k := range w.events {
		w.prune(k, now)
	}
}

// retryAfter is how long until the oldest live event leaves the window.
func (w *Window) retryAfter(evs []time.Time, now time.Time) time.Duration {
	d := evs[0].Add(w.period).Sub(now)
	if d < time.Second {
		d = time.Second
	}
	return d
}

// Check reports whether one more event is allowed for key without recording it.
// When it is not, it returns the time until the next event is allowed.
func (w *Window) Check(key string) (bool, time.Duration) {
	w.mu.Lock()
	defer w.mu.Unlock()
	now := w.now()
	evs := w.prune(key, now)
	if len(evs) < w.max {
		return true, 0
	}
	return false, w.retryAfter(evs, now)
}

// Allow records an event for key if the limit permits it.
func (w *Window) Allow(key string) (bool, time.Duration) {
	w.mu.Lock()
	defer w.mu.Unlock()
	now := w.now()
	w.maybeSweep(now)
	evs := w.prune(key, now)
	if len(evs) >= w.max {
		return false, w.retryAfter(evs, now)
	}
	w.events[key] = append(evs, now)
	return true, 0
}

// Record adds an event for key unconditionally (e.g. a failed login).
func (w *Window) Record(key string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	now := w.now()
	w.maybeSweep(now)
	w.events[key] = append(w.prune(key, now), now)
}

// Reset forgets every event for key.
func (w *Window) Reset(key string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.events, key)
}

// Reservation is an event recorded by Reserve. Cancel removes it again, for
// attempts that turned out not to count (e.g. a successful login).
type Reservation struct {
	w    *Window
	key  string
	at   time.Time
	done *bool
}

// Reserve atomically checks the limit and records an event for key, so
// concurrent callers cannot all pass a Check before any of them Records.
// When the limit is reached it returns false and the time until the next
// event is allowed.
func (w *Window) Reserve(key string) (Reservation, bool, time.Duration) {
	w.mu.Lock()
	defer w.mu.Unlock()
	now := w.now()
	w.maybeSweep(now)
	evs := w.prune(key, now)
	if len(evs) >= w.max {
		return Reservation{}, false, w.retryAfter(evs, now)
	}
	w.events[key] = append(evs, now)
	return Reservation{w: w, key: key, at: now, done: new(bool)}, true, 0
}

// Cancel removes the reserved event. It is idempotent and a no-op on the zero
// Reservation or once the event has left the window.
func (r Reservation) Cancel() {
	if r.w == nil {
		return
	}
	r.w.mu.Lock()
	defer r.w.mu.Unlock()
	if *r.done {
		return
	}
	*r.done = true
	evs := r.w.events[r.key]
	for i := len(evs) - 1; i >= 0; i-- {
		if evs[i].Equal(r.at) {
			rest := make([]time.Time, 0, len(evs)-1)
			rest = append(rest, evs[:i]...)
			rest = append(rest, evs[i+1:]...)
			if len(rest) == 0 {
				delete(r.w.events, r.key)
				return
			}
			r.w.events[r.key] = rest
			return
		}
	}
}
