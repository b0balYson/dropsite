package ratelimit

import (
	"strconv"
	"sync"
	"testing"
	"time"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }
func newClock() *clock                   { return &clock{t: time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)} }

func TestDeploysPerHour(t *testing.T) {
	c := newClock()
	w := New(30, time.Hour).WithClock(c.now)
	for i := range 30 {
		if ok, _ := w.Allow("user:1"); !ok {
			t.Fatalf("deploy %d should be allowed", i+1)
		}
		c.advance(time.Minute)
	}
	ok, retry := w.Allow("user:1")
	if ok {
		t.Fatal("31st deploy within an hour must be refused")
	}
	// The first event was at t0; now is t0+30m, so it expires in 30m.
	if retry != 30*time.Minute {
		t.Fatalf("retry after %v, want 30m", retry)
	}
	if ok, _ := w.Allow("user:2"); !ok {
		t.Fatal("other users are not affected")
	}
	c.advance(30*time.Minute + time.Millisecond)
	if ok, _ := w.Allow("user:1"); !ok {
		t.Fatal("allowed again once the oldest event left the window")
	}
}

func TestFailedLogins(t *testing.T) {
	c := newClock()
	w := New(10, 15*time.Minute).WithClock(c.now)
	for range 10 {
		if ok, _ := w.Check("ip:1.2.3.4"); !ok {
			t.Fatal("check must pass before the limit")
		}
		w.Record("ip:1.2.3.4")
	}
	if ok, retry := w.Check("ip:1.2.3.4"); ok || retry != 15*time.Minute {
		t.Fatalf("11th attempt must be refused for 15m, got %v %v", ok, retry)
	}
	c.advance(15 * time.Minute)
	if ok, _ := w.Check("ip:1.2.3.4"); !ok {
		t.Fatal("window must expire")
	}
	w.Record("ip:5.6.7.8")
	w.Reset("ip:5.6.7.8")
	if len(w.events) != 0 {
		t.Fatal("reset and expiry must free memory")
	}
}

func TestRetryAfterIsAtLeastOneSecond(t *testing.T) {
	c := newClock()
	w := New(1, time.Second).WithClock(c.now)
	w.Record("k")
	c.advance(999 * time.Millisecond)
	if ok, retry := w.Check("k"); ok || retry != time.Second {
		t.Fatalf("got %v %v", ok, retry)
	}
}

func TestSweepDropsIdleKeys(t *testing.T) {
	c := newClock()
	w := New(5, time.Minute).WithClock(c.now)
	for i := range sweepEvery - 1 {
		w.Record("k" + strconv.Itoa(i))
	}
	c.advance(2 * time.Minute)
	w.Record("fresh")
	if len(w.events) != 1 {
		t.Fatalf("expected only the fresh key, got %d", len(w.events))
	}
}

func TestConcurrentAllow(t *testing.T) {
	w := New(100, time.Hour)
	var wg sync.WaitGroup
	var mu sync.Mutex
	allowed := 0
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 10 {
				if ok, _ := w.Allow("shared"); ok {
					mu.Lock()
					allowed++
					mu.Unlock()
				}
			}
		}()
	}
	wg.Wait()
	if allowed != 100 {
		t.Fatalf("allowed %d, want exactly 100", allowed)
	}
}
