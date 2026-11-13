package ratelimit

import (
	"testing"
	"time"
)

func TestAllowMax(t *testing.T) {
	c := newClock()
	w := New(60, time.Minute).WithClock(c.now)
	for i := range 3 {
		if ok, _ := w.AllowMax("e", 3); !ok {
			t.Fatalf("event %d refused", i+1)
		}
		c.advance(10 * time.Second)
	}
	ok, retry := w.AllowMax("e", 3)
	if ok || retry != 30*time.Second {
		t.Fatalf("4th: %v %v", ok, retry)
	}
	if ok, _ := w.AllowMax("e", 0); !ok {
		t.Fatal("max 0 uses the limiter's own limit")
	}
}
