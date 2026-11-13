package guard

import (
	"sync"
	"time"
)

// Staged response to a network flagged as a bot.
const (
	// SuspicionDecay forgets a network's level after this long without a
	// new signal.
	SuspicionDecay = time.Hour
	// BanLevel is the level at which the network is banned.
	BanLevel = 3
	// SessionsPerNetHour above this many issued session tokens per network
	// and hour is a signal of its own.
	SessionsPerNetHour = 30
	maxSuspects        = 100000
)

// levelFactor is the proof-of-work multiplier per level; levelQuota caps
// the calls of one session per level (0: no cap).
var (
	levelFactor = [...]int{1, 10, 100, 100}
	levelQuota  = [...]int{0, 10, 5, 5}
)

// DifficultyFactor returns the proof-of-work multiplier of level.
func DifficultyFactor(level int) int { return levelFactor[clampLevel(level)] }

// QuotaCap returns the session quota cap of level (0: none).
func QuotaCap(level int) int { return levelQuota[clampLevel(level)] }

func clampLevel(l int) int { return max(0, min(l, BanLevel)) }

type suspect struct {
	level    int
	lastFlag time.Time
	sessions []time.Time
}

// Suspicion tracks the level of each network key. It is safe for
// concurrent use.
type Suspicion struct {
	mu   sync.Mutex
	nets map[string]*suspect
}

// NewSuspicion returns an empty tracker.
func NewSuspicion() *Suspicion { return &Suspicion{nets: map[string]*suspect{}} }

func (s *Suspicion) get(key string, now time.Time) *suspect {
	sp := s.nets[key]
	if sp == nil {
		if len(s.nets) >= maxSuspects {
			s.sweep(now)
		}
		sp = &suspect{}
		s.nets[key] = sp
	}
	if sp.level > 0 && now.Sub(sp.lastFlag) > SuspicionDecay {
		sp.level = 0
	}
	return sp
}

func (s *Suspicion) sweep(now time.Time) {
	for k, sp := range s.nets {
		if now.Sub(sp.lastFlag) > SuspicionDecay && (len(sp.sessions) == 0 || now.Sub(sp.sessions[len(sp.sessions)-1]) > time.Hour) {
			delete(s.nets, k)
		}
	}
}

// Level returns the current level of key.
func (s *Suspicion) Level(key string, now time.Time) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sp, ok := s.nets[key]; ok && (sp.level == 0 || now.Sub(sp.lastFlag) <= SuspicionDecay) {
		return sp.level
	}
	return 0
}

// Flag raises the level of key and returns the new level; BanLevel or more
// means the caller bans the network.
func (s *Suspicion) Flag(key string, now time.Time) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	sp := s.get(key, now)
	sp.level = min(sp.level+1, BanLevel)
	sp.lastFlag = now
	return sp.level
}

// Session records an issued session token for key and reports whether the
// network got more than SessionsPerNetHour tokens within the last hour.
func (s *Suspicion) Session(key string, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	sp := s.get(key, now)
	cut := now.Add(-time.Hour)
	i := 0
	for i < len(sp.sessions) && !sp.sessions[i].After(cut) {
		i++
	}
	sp.sessions = append(sp.sessions[i:], now)
	if len(sp.sessions) > SessionsPerNetHour {
		sp.sessions = sp.sessions[:0]
		return true
	}
	return false
}

// Forget clears key (after an unban).
func (s *Suspicion) Forget(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.nets, key)
}
