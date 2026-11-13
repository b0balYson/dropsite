package service

import (
	"context"
	"sync"
	"time"
)

// ProxyRejectedFlushInterval is the shortest time between two writes of the
// rejected-call counters: calls refused before the rate limiter (origin,
// method, path) are unlimited, so they are counted in memory and written in
// batches instead of one database write per request.
const ProxyRejectedFlushInterval = 10 * time.Second

// rejectedCount is the unwritten part of one endpoint's rejected counter.
type rejectedCount struct {
	n      int
	status int
	at     time.Time
}

// rejectedUsage accumulates rejected proxy calls per endpoint ID.
type rejectedUsage struct {
	mu        sync.Mutex
	pending   map[int64]rejectedCount
	lastFlush time.Time
}

func newRejectedUsage() *rejectedUsage {
	return &rejectedUsage{pending: map[int64]rejectedCount{}}
}

// add counts one rejected call and reports whether a flush is due.
func (u *rejectedUsage) add(id int64, status int, now time.Time) bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	c := u.pending[id]
	u.pending[id] = rejectedCount{n: c.n + 1, status: status, at: now}
	return now.Sub(u.lastFlush) >= ProxyRejectedFlushInterval
}

// take removes and returns everything pending.
func (u *rejectedUsage) take(now time.Time) map[int64]rejectedCount {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.lastFlush = now
	if len(u.pending) == 0 {
		return nil
	}
	out := u.pending
	u.pending = map[int64]rejectedCount{}
	return out
}

// recordProxyRejected counts a call refused before forwarding; the counter
// reaches the database at most once per ProxyRejectedFlushInterval.
func (s *Service) recordProxyRejected(ctx context.Context, id int64, status int) {
	if s.proxyRejected.add(id, status, s.now()) {
		s.flushProxyRejected(ctx)
	}
}

// flushProxyRejected writes the accumulated rejected counters. Owner reads
// call it first so the counters they see are complete.
func (s *Service) flushProxyRejected(ctx context.Context) {
	ctx = context.WithoutCancel(ctx)
	for id, c := range s.proxyRejected.take(s.now()) {
		if err := s.repo.AddProxyRejected(ctx, id, c.n, c.status, c.at); err != nil {
			s.log.Warn("proxy usage not recorded", "endpoint", id, "rejected", c.n, "err", err)
		}
	}
}
