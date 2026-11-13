package updates

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/b0balYson/dropsite/internal/semver"
)

func mustVersion(t *testing.T, s string) semver.Version {
	t.Helper()
	v, ok := semver.Parse(s)
	if !ok {
		t.Fatal(s)
	}
	return v
}

func TestWriteRequest(t *testing.T) {
	f := NewFiles(t.TempDir())
	if f.Pending() {
		t.Fatal("pending before any request")
	}
	if err := f.WriteRequest(mustVersion(t, "0.5.0"), 7); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(f.Dir, RequestFile))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "version=0.5.0\nadmin=7\n" {
		t.Fatalf("request body %q", b)
	}
	st, _ := os.Stat(f.Dir)
	if st.Mode().Perm() != 0o700 {
		t.Fatalf("directory mode %v", st.Mode().Perm())
	}
	if !f.Pending() {
		t.Fatal("not pending")
	}
	if err := f.WriteRequest(mustVersion(t, "0.6.0"), 7); !errors.Is(err, ErrPending) {
		t.Fatalf("second request: %v", err)
	}
	if b2, _ := os.ReadFile(filepath.Join(f.Dir, RequestFile)); string(b2) != string(b) {
		t.Fatal("the waiting request was replaced")
	}
	entries, _ := os.ReadDir(f.Dir)
	if len(entries) != 1 {
		t.Fatalf("temporary files left: %v", entries)
	}
}

func TestWriteRequestRefusesInvalid(t *testing.T) {
	f := NewFiles(t.TempDir())
	if err := f.WriteRequest(mustVersion(t, "0.5.0-rc.1"), 1); err == nil {
		t.Fatal("pre-release accepted")
	}
	if err := f.WriteRequest(mustVersion(t, "0.5.0"), 0); err == nil {
		t.Fatal("no administrator accepted")
	}
	if f.Pending() {
		t.Fatal("request written")
	}
}

func writeFile(t *testing.T, f Files, name, body string) {
	t.Helper()
	if err := os.MkdirAll(f.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.Dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestStatus(t *testing.T) {
	f := NewFiles(t.TempDir())
	if _, ok, err := f.Status(); ok || err != nil {
		t.Fatalf("no status: %v %v", ok, err)
	}
	now := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	writeFile(t, f, StatusFile, `{"state":"running","from":"0.4.0","to":"0.5.0","started_at":"2026-11-13T09:59:00Z","log":["==> Backing up"]}`)
	st, ok, err := f.Status()
	if err != nil || !ok || st.State != StateRunning || st.To != "0.5.0" || len(st.Log) != 1 {
		t.Fatalf("status %+v %v %v", st, ok, err)
	}
	if !st.Active(now) || st.Active(now.Add(3*time.Hour)) {
		t.Fatal("running status: active now, stale after hours")
	}
	for _, bad := range []string{`{"state":"exploded"}`, `{"state":"done","to":"0.5.0<script>"}`, `nope`} {
		writeFile(t, f, StatusFile, bad)
		if _, ok, err := f.Status(); ok || err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
}

func TestStatusIgnoresNonRegularFiles(t *testing.T) {
	f := NewFiles(t.TempDir())
	if err := os.MkdirAll(filepath.Join(f.Dir, StatusFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := f.Status(); ok || err == nil {
		t.Fatal("a directory read as the status")
	}
}

func TestHeartbeat(t *testing.T) {
	f := NewFiles(t.TempDir())
	now := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	if f.HelperPresent(now) {
		t.Fatal("helper without a heartbeat")
	}
	writeFile(t, f, HeartbeatFile, strconv.FormatInt(now.Add(-time.Minute).Unix(), 10)+"\n")
	if !f.HelperPresent(now) {
		t.Fatal("fresh heartbeat not seen")
	}
	if f.HelperPresent(now.Add(10 * time.Minute)) {
		t.Fatal("old heartbeat counted")
	}
	writeFile(t, f, HeartbeatFile, "soon")
	if f.HelperPresent(now) {
		t.Fatal("garbage heartbeat counted")
	}
}
