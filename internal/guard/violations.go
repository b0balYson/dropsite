package guard

import (
	"sync"
	"time"
)

// Rate-limit violations that lead to a ban. A client over the global limit
// gets hundreds of 429 answers within seconds, so one violation is a minute
// with at least one of them, not every single answer.
const (
	ViolationSlot   = time.Minute
	ViolationWindow = 10 * time.Minute
	// ViolationsToBan violations within ViolationWindow ban the network.
	ViolationsToBan = 3
	maxViolators    = 100000
)

// BanLadder is the ban duration per offence within BanMemory: the first
// ban is short, repeats are longer.
var BanLadder = []time.Duration{15 * time.Minute, time.Hour, 24 * time.Hour}

// BanMemory is how long a past ban still raises the next one.
const BanMemory = 24 * time.Hour

// BanDuration returns the duration of a ban at level (1-based).
func BanDuration(level int) time.Duration {
	return BanLadder[max(0, min(level, len(BanLadder))-1)]
}

// Violations counts rate-limit violations per network key. It is safe for
// concurrent use.
type Violations struct {
	mu    sync.Mutex
	slots map[string][]time.Time // start of each violation minute, oldest first
}

// NewViolations returns an empty counter.
func NewViolations() *Violations { return &Violations{slots: map[string][]time.Time{}} }

// Add records a limited answer to key at t and reports whether the key has
// reached ViolationsToBan violations within ViolationWindow; the history of
// the key is cleared then.
func (v *Violations) Add(key string, t time.Time) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	if len(v.slots) >= maxViolators {
		for k, s := range v.slots {
			if t.Sub(s[len(s)-1]) > ViolationWindow {
				delete(v.slots, k)
			}
		}
	}
	s := v.slots[key]
	cut := t.Add(-ViolationWindow)
	i := 0
	for i < len(s) && !s[i].After(cut) {
		i++
	}
	s = s[i:]
	if len(s) > 0 && t.Sub(s[len(s)-1]) < ViolationSlot {
		v.slots[key] = s
		return false
	}
	s = append(s, t)
	if len(s) >= ViolationsToBan {
		delete(v.slots, key)
		return true
	}
	v.slots[key] = s
	return false
}
