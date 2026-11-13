package guard

import (
	"container/list"
	"math"
	"sync"
	"time"
)

// Thresholds of the periodicity detector. A script that calls an endpoint
// from a loop with sleep() produces intervals that hardly vary; people do
// not.
const (
	// RingSize is how many recent timestamps are kept per key.
	RingSize = 20
	// PeriodicMinSamples is the fewest requests a periodic verdict needs.
	PeriodicMinSamples = 10
	// PeriodicMinMean and PeriodicMaxMean bound the mean interval that
	// counts as scheduled work: faster is the rate limiter's job, slower
	// is too rare to matter.
	PeriodicMinMean = time.Second
	PeriodicMaxMean = 30 * time.Minute
	// PeriodicMaxCV is the coefficient of variation (stddev / mean) of the
	// intervals below which the series is called periodic.
	PeriodicMaxCV = 0.1

	// ContinuousDuration is how long an uninterrupted streak may last
	// before it counts as a bot; ContinuousMaxGap ends a streak.
	ContinuousDuration = 45 * time.Minute
	ContinuousMaxGap   = 5 * time.Minute
	// ContinuousMinRequests is the fewest requests such a streak needs, and
	// ContinuousMaxCV how uniform its recent intervals must be.
	ContinuousMinRequests = 30
	ContinuousMaxCV       = 0.5

	// DefaultMaxKeys bounds the detector's memory (least recently used
	// keys are dropped first).
	DefaultMaxKeys = 10000
)

// Signal is what an observation revealed.
type Signal int

const (
	// SignalNone means nothing suspicious.
	SignalNone Signal = iota
	// SignalPeriodic means requests arrive at a near-constant interval.
	SignalPeriodic
	// SignalContinuous means uniform activity without pauses for too long.
	SignalContinuous
)

// String names the signal for logs and audit entries.
func (s Signal) String() string {
	switch s {
	case SignalPeriodic:
		return "periodic"
	case SignalContinuous:
		return "continuous"
	}
	return "none"
}

type series struct {
	key         string
	times       [RingSize]time.Time
	n           int // stored timestamps (≤ RingSize)
	next        int // ring write position
	streakStart time.Time
	streakCount int
}

func (s *series) add(t time.Time) {
	if s.n > 0 {
		last := s.times[(s.next+RingSize-1)%RingSize]
		if t.Sub(last) > ContinuousMaxGap {
			s.streakStart, s.streakCount = t, 0
		}
	} else {
		s.streakStart = t
	}
	s.streakCount++
	s.times[s.next] = t
	s.next = (s.next + 1) % RingSize
	if s.n < RingSize {
		s.n++
	}
}

// intervals returns the gaps between the stored timestamps, oldest first.
func (s *series) intervals() []float64 {
	if s.n < 2 {
		return nil
	}
	start := (s.next + RingSize - s.n) % RingSize
	out := make([]float64, 0, s.n-1)
	prev := s.times[start]
	for i := 1; i < s.n; i++ {
		cur := s.times[(start+i)%RingSize]
		out = append(out, cur.Sub(prev).Seconds())
		prev = cur
	}
	return out
}

// MeanCV returns the mean and coefficient of variation of xs.
func MeanCV(xs []float64) (float64, float64) {
	if len(xs) == 0 {
		return 0, 0
	}
	var sum float64
	for _, x := range xs {
		sum += x
	}
	mean := sum / float64(len(xs))
	if mean <= 0 {
		return mean, 0
	}
	var sq float64
	for _, x := range xs {
		sq += (x - mean) * (x - mean)
	}
	return mean, math.Sqrt(sq/float64(len(xs))) / mean
}

func (s *series) verdict(now time.Time) Signal {
	iv := s.intervals()
	if s.n >= PeriodicMinSamples {
		mean, cv := MeanCV(iv)
		m := time.Duration(mean * float64(time.Second))
		if m >= PeriodicMinMean && m <= PeriodicMaxMean && cv < PeriodicMaxCV {
			return SignalPeriodic
		}
	}
	if s.streakCount >= ContinuousMinRequests && now.Sub(s.streakStart) > ContinuousDuration {
		if _, cv := MeanCV(iv); cv < ContinuousMaxCV {
			return SignalContinuous
		}
	}
	return SignalNone
}

// Periodicity keeps the recent request times per key in a bounded LRU and
// tells when a key behaves like a scheduled script. It is safe for
// concurrent use.
type Periodicity struct {
	mu      sync.Mutex
	maxKeys int
	order   *list.List // front = most recently used
	items   map[string]*list.Element
}

// NewPeriodicity returns a detector holding at most maxKeys keys
// (DefaultMaxKeys when maxKeys ≤ 0).
func NewPeriodicity(maxKeys int) *Periodicity {
	if maxKeys <= 0 {
		maxKeys = DefaultMaxKeys
	}
	return &Periodicity{maxKeys: maxKeys, order: list.New(), items: map[string]*list.Element{}}
}

// Observe records a request of key at t and returns the verdict. After a
// signal the key's history is cleared, so the next signal needs a new run.
func (p *Periodicity) Observe(key string, t time.Time) Signal {
	p.mu.Lock()
	defer p.mu.Unlock()
	el, ok := p.items[key]
	if ok {
		p.order.MoveToFront(el)
	} else {
		el = p.order.PushFront(&series{key: key})
		p.items[key] = el
		for p.order.Len() > p.maxKeys {
			old := p.order.Back()
			p.order.Remove(old)
			delete(p.items, old.Value.(*series).key)
		}
	}
	s := el.Value.(*series)
	s.add(t)
	sig := s.verdict(t)
	if sig != SignalNone {
		*s = series{key: key}
	}
	return sig
}

// Len returns the number of keys held.
func (p *Periodicity) Len() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.order.Len()
}
