package guard

import (
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)

// feed observes the given intervals (after a first request at t0) and
// returns the first signal and the request index it came at (-1: none).
func feed(p *Periodicity, key string, intervals []time.Duration) (Signal, int) {
	at := t0
	if s := p.Observe(key, at); s != SignalNone {
		return s, 0
	}
	for i, d := range intervals {
		at = at.Add(d)
		if s := p.Observe(key, at); s != SignalNone {
			return s, i + 1
		}
	}
	return SignalNone, -1
}

func repeat(n int, f func(i int) time.Duration) []time.Duration {
	out := make([]time.Duration, n)
	for i := range out {
		out[i] = f(i)
	}
	return out
}

func TestPeriodicity(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	secs := func(f float64) time.Duration { return time.Duration(f * float64(time.Second)) }
	human := []time.Duration{2 * time.Second, 40 * time.Second, 5 * time.Second, 5 * time.Minute, 3 * time.Second,
		90 * time.Second, 12 * time.Second, 8 * time.Second, 25 * time.Minute, 4 * time.Second, 60 * time.Second,
		7 * time.Second, 2 * time.Minute, 15 * time.Second, 3 * time.Second, 45 * time.Second, 10 * time.Minute,
		6 * time.Second, 30 * time.Second, 9 * time.Second}
	cases := []struct {
		name      string
		intervals []time.Duration
		want      Signal
	}{
		{"human chat", human, SignalNone},
		{"sleep 60", repeat(12, func(int) time.Duration { return time.Minute }), SignalPeriodic},
		{"sleep 60 with 3% jitter", repeat(12, func(int) time.Duration { return secs(60 * (0.97 + 0.06*rng.Float64())) }), SignalPeriodic},
		{"cron every 10 minutes", repeat(12, func(int) time.Duration { return 10 * time.Minute }), SignalPeriodic},
		{"random 3-8 s, short", repeat(40, func(int) time.Duration { return secs(3 + 5*rng.Float64()) }), SignalNone},
		{"random 3-8 s for an hour", repeat(800, func(int) time.Duration { return secs(3 + 5*rng.Float64()) }), SignalContinuous},
		{"too fast for periodicity", repeat(12, func(int) time.Duration { return 200 * time.Millisecond }), SignalNone},
		{"daily job", repeat(12, func(int) time.Duration { return 24 * time.Hour }), SignalNone},
		{"long session with pauses", repeat(800, func(i int) time.Duration {
			if i%40 == 39 {
				return 6 * time.Minute
			}
			return secs(3 + 5*rng.Float64())
		}), SignalNone},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, at := feed(NewPeriodicity(0), "k", c.intervals)
			if got != c.want {
				t.Fatalf("signal = %v at %d, want %v", got, at, c.want)
			}
			if c.want == SignalPeriodic && at != PeriodicMinSamples-1 {
				t.Fatalf("periodic signal at request %d, want %d", at, PeriodicMinSamples-1)
			}
		})
	}
}

func TestPeriodicityClearsAfterSignalAndBoundsKeys(t *testing.T) {
	p := NewPeriodicity(3)
	at := t0
	for i := range 10 {
		if s := p.Observe("bot", at); s != SignalNone && i != 9 {
			t.Fatalf("early signal at %d", i)
		}
		at = at.Add(time.Minute)
	}
	if s := p.Observe("bot", at); s != SignalNone {
		t.Fatalf("history not cleared: %v", s)
	}
	for _, k := range []string{"a", "b", "c", "d"} {
		p.Observe(k, at)
	}
	if p.Len() != 3 {
		t.Fatalf("len = %d, want 3", p.Len())
	}
}

func TestMeanCV(t *testing.T) {
	if m, cv := MeanCV([]float64{2, 2, 2}); m != 2 || cv != 0 {
		t.Fatalf("got %v %v", m, cv)
	}
	if _, cv := MeanCV([]float64{1, 3}); cv != 0.5 {
		t.Fatalf("cv = %v", cv)
	}
}

func TestNetKey(t *testing.T) {
	cases := map[string]string{
		"203.0.113.7":               "203.0.113.7",
		"::ffff:203.0.113.7":        "203.0.113.7",
		"2001:db8:1:2:3:4:5:6":      "2001:db8:1:2::/64",
		"2001:db8:1:2::9":           "2001:db8:1:2::/64",
		"fe80::1%eth0":              "fe80::/64",
		"not an ip":                 "",
		"":                          "",
		"2001:DB8:0:0:0:0:0:1":      "2001:db8::/64",
		"2001:0db8:0000:0002::0001": "2001:db8:0:2::/64",
	}
	for in, want := range cases {
		if got := NetKey(in); got != want {
			t.Errorf("NetKey(%q) = %q, want %q", in, got, want)
		}
	}
	if CanonicalIP("2001:0DB8::0001") != "2001:db8::1" || !IsNet("2001:db8::/64") || IsNet("1.2.3.4") {
		t.Fatal("canonical form")
	}
}

func TestSuspicionStages(t *testing.T) {
	s := NewSuspicion()
	if s.Level("n", t0) != 0 || DifficultyFactor(0) != 1 || QuotaCap(0) != 0 {
		t.Fatal("fresh level")
	}
	if l := s.Flag("n", t0); l != 1 || DifficultyFactor(l) != 10 || QuotaCap(l) != 10 {
		t.Fatalf("level 1: %d", l)
	}
	if l := s.Flag("n", t0.Add(time.Minute)); l != 2 || DifficultyFactor(l) != 100 {
		t.Fatalf("level 2: %d", l)
	}
	if l := s.Flag("n", t0.Add(2*time.Minute)); l != BanLevel {
		t.Fatalf("level 3: %d", l)
	}
	if s.Level("n", t0.Add(2*time.Minute+SuspicionDecay+time.Second)) != 0 {
		t.Fatal("level does not decay")
	}
	if l := s.Flag("n", t0.Add(3*time.Hour)); l != 1 {
		t.Fatalf("after decay: %d", l)
	}
	many := false
	for i := range SessionsPerNetHour + 1 {
		many = s.Session("m", t0.Add(time.Duration(i)*time.Second))
	}
	if !many {
		t.Fatal("dozens of sessions not flagged")
	}
	if s.Session("x", t0) {
		t.Fatal("single session flagged")
	}
}

func TestViolations(t *testing.T) {
	v := NewViolations()
	// Hundreds of 429 within one minute are one violation.
	for i := range 300 {
		if v.Add("ip", t0.Add(time.Duration(i)*100*time.Millisecond)) {
			t.Fatal("ban after one violation minute")
		}
	}
	if v.Add("ip", t0.Add(2*time.Minute)) {
		t.Fatal("ban after two")
	}
	if !v.Add("ip", t0.Add(4*time.Minute)) {
		t.Fatal("no ban after three violations in 10 minutes")
	}
	w := NewViolations()
	w.Add("ip", t0)
	w.Add("ip", t0.Add(6*time.Minute))
	if w.Add("ip", t0.Add(12*time.Minute)) {
		t.Fatal("violations older than the window counted")
	}
	if BanDuration(1) != 15*time.Minute || BanDuration(2) != time.Hour || BanDuration(3) != 24*time.Hour || BanDuration(9) != 24*time.Hour {
		t.Fatal("ladder")
	}
}

func TestMarkers(t *testing.T) {
	root := t.TempDir()
	m := NewMarkers(root)
	if err := m.SetFlag(FlagDefenderOff, true); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(root, "flags", "defender-off"))
	if err != nil || fi.Mode().Perm() != 0o644 || !m.FlagSet(FlagDefenderOff) {
		t.Fatalf("flag marker: %v %v", fi, err)
	}
	if err := m.SetFlag(FlagDefenderOff, false); err != nil || m.FlagSet(FlagDefenderOff) {
		t.Fatalf("flag not removed: %v", err)
	}
	if err := m.SetFlag("../x", true); err == nil {
		t.Fatal("bad flag accepted")
	}
	for _, a := range []string{"203.0.113.7", "2001:db8::1"} {
		if err := m.Ban(a); err != nil {
			t.Fatal(err)
		}
	}
	for _, bad := range []string{"../etc", "2001:DB8::1", ""} {
		if err := m.Ban(bad); err == nil {
			t.Fatalf("Ban(%q) accepted", bad)
		}
	}
	got, err := m.Banned()
	if err != nil || len(got) != 2 {
		t.Fatalf("banned = %v %v", got, err)
	}
	if err := m.Unban("203.0.113.7"); err != nil {
		t.Fatal(err)
	}
	if err := m.Unban("203.0.113.7"); err != nil {
		t.Fatal("second unban:", err)
	}
	if got, _ := m.Banned(); len(got) != 1 || got[0] != "2001:db8::1" {
		t.Fatalf("after unban: %v", got)
	}
}

func TestParseAccess(t *testing.T) {
	line := []byte(`{"level":"info","ts":1763028000.5,"logger":"http.log.access.sites","request":{"remote_ip":"172.18.0.1","client_ip":"2001:DB8::5","method":"GET","host":"a.example.com","uri":"/"},"status":429,"resp_headers":{"X-Dropsite-Block":["rate"],"Retry-After":["2"]}}`)
	a, ok := ParseAccess(line)
	if !ok || a.IP != "2001:db8::5" || a.Status != 429 || a.Block != BlockRate || a.Time.Unix() != 1763028000 {
		t.Fatalf("got %+v %v", a, ok)
	}
	if _, ok := ParseAccess([]byte(`{"msg":"x"}`)); ok {
		t.Fatal("non-request line parsed")
	}
	if _, ok := ParseAccess([]byte(`not json`)); ok {
		t.Fatal("garbage parsed")
	}
}
