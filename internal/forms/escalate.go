package forms

import (
	"sync"
	"time"
)

// Escalation parameters of the proof-of-work difficulty per source.
const (
	EscalationWindow = 10 * time.Minute
	// EscalationFactor multiplies the difficulty per recent event.
	EscalationFactor = 4
	// MaxEscalationLevel caps the difficulty at base × 4^4 = 256 × base.
	MaxEscalationLevel = 4
)

// Escalator counts recent events (issued challenges and rejected posts) per
// key in a sliding window. The level is the number of events still in the
// window, capped; it decays as events age out.
type Escalator struct {
	mu     sync.Mutex
	now    func() time.Time
	events map[string][]time.Time
	calls  int
}

// NewEscalator returns an Escalator using now as its clock.
func NewEscalator(now func() time.Time) *Escalator {
	if now == nil {
		now = time.Now
	}
	return &Escalator{now: now, events: map[string][]time.Time{}}
}

func (e *Escalator) live(key string, now time.Time) []time.Time {
	cutoff := now.Add(-EscalationWindow)
	evs := e.events[key]
	i := 0
	for i < len(evs) && !evs[i].After(cutoff) {
		i++
	}
	evs = evs[i:]
	if len(evs) == 0 {
		delete(e.events, key)
		return nil
	}
	e.events[key] = evs
	return evs
}

// Level returns the current level of key.
func (e *Escalator) Level(key string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return min(len(e.live(key, e.now())), MaxEscalationLevel)
}

// Next returns the level for a new challenge and records it as an event, so
// the following challenge from the same key is harder.
func (e *Escalator) Next(key string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.now()
	e.sweep(now)
	evs := e.live(key, now)
	level := min(len(evs), MaxEscalationLevel)
	if len(evs) <= MaxEscalationLevel {
		e.events[key] = append(evs, now)
	}
	return level
}

// Penalize raises the level of key at once (a rejected submission).
func (e *Escalator) Penalize(key string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.now()
	evs := e.live(key, now)
	if len(evs) <= MaxEscalationLevel {
		e.events[key] = append(evs, now)
	}
}

// sweep drops idle keys now and then to bound memory. Caller holds mu.
func (e *Escalator) sweep(now time.Time) {
	e.calls++
	if e.calls%1024 != 0 {
		return
	}
	for k := range e.events {
		e.live(k, now)
	}
}

// Difficulty returns base × 4^level (level is at most MaxEscalationLevel).
func Difficulty(base, level int) int {
	n := base
	for range min(level, MaxEscalationLevel) {
		n *= EscalationFactor
	}
	return n
}
