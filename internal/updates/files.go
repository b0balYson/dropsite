package updates

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/b0balYson/dropsite/internal/semver"
)

// Files of the update directory (DirName under the state directory). control
// writes only the request; the host helper writes the status and the
// heartbeat, as control's own user, so it never gains anything by following
// a link control could have planted.
const (
	DirName       = "update"
	RequestFile   = "request"
	StatusFile    = "status"
	HeartbeatFile = "heartbeat"

	maxStatusBytes    = 64 << 10
	maxHeartbeatBytes = 64
	// HelperFresh is how old the heartbeat may be for the helper to count as
	// installed: it writes one every minute.
	HelperFresh = 5 * time.Minute
	// StaleRunning is how long a "running" status is believed: an update
	// takes minutes; after this the helper is assumed to have died.
	StaleRunning = 2 * time.Hour
)

// States of the status file.
const (
	StateQueued  = "queued"
	StateRunning = "running"
	StateDone    = "done"
	StateFailed  = "failed"
)

// ErrPending is returned when a request is already waiting.
var ErrPending = errors.New("an update request is already waiting")

// Status is what the host helper reports.
type Status struct {
	State      string   `json:"state"`
	From       string   `json:"from"`
	To         string   `json:"to"`
	StartedAt  string   `json:"started_at"`
	FinishedAt string   `json:"finished_at"`
	Message    string   `json:"message"`
	Log        []string `json:"log"`
}

// Started and Finished parse the times of the status ("" gives zero).
func (s Status) Started() time.Time  { return parseTime(s.StartedAt) }
func (s Status) Finished() time.Time { return parseTime(s.FinishedAt) }

// Active reports whether the update is waiting or running (and not stale).
func (s Status) Active(now time.Time) bool {
	switch s.State {
	case StateQueued, StateRunning:
		st := s.Started()
		return st.IsZero() || now.Sub(st) < StaleRunning
	}
	return false
}

func parseTime(v string) time.Time {
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return time.Time{}
	}
	return t
}

// Files is the update directory of one installation.
type Files struct{ Dir string }

// NewFiles returns the update directory under stateDir.
func NewFiles(stateDir string) Files { return Files{Dir: filepath.Join(stateDir, DirName)} }

func (f Files) path(name string) string { return filepath.Join(f.Dir, name) }

// RequestBody is the exact content of a request file: two lines, nothing
// the helper could execute. The helper rejects anything else.
func RequestBody(v semver.Version, adminID int64) string {
	return "version=" + v.String() + "\nadmin=" + strconv.FormatInt(adminID, 10) + "\n"
}

// WriteRequest stores a request for version v by administrator adminID. It
// never replaces a waiting request: the file appears atomically (link of a
// complete temporary file) or the call fails with ErrPending.
func (f Files) WriteRequest(v semver.Version, adminID int64) error {
	if !StrictVersion(v.String()) || adminID <= 0 {
		return fmt.Errorf("invalid update request %s by %d", v, adminID)
	}
	if err := os.MkdirAll(f.Dir, 0o700); err != nil {
		return fmt.Errorf("create update directory: %w", err)
	}
	tmp, err := os.CreateTemp(f.Dir, ".request-*")
	if err != nil {
		return fmt.Errorf("create update request: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := io.WriteString(tmp, RequestBody(v, adminID)); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write update request: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write update request: %w", err)
	}
	if err := os.Link(tmp.Name(), f.path(RequestFile)); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return ErrPending
		}
		return fmt.Errorf("store update request: %w", err)
	}
	return nil
}

// Pending reports whether a request waits for the helper.
func (f Files) Pending() bool {
	_, err := os.Lstat(f.path(RequestFile))
	return err == nil
}

// Status reads the helper's status; ok is false when there is none.
func (f Files) Status() (Status, bool, error) {
	b, err := readSmall(f.path(StatusFile), maxStatusBytes)
	if errors.Is(err, fs.ErrNotExist) {
		return Status{}, false, nil
	}
	if err != nil {
		return Status{}, false, err
	}
	var st Status
	if err := json.Unmarshal(b, &st); err != nil {
		return Status{}, false, fmt.Errorf("read update status: %w", err)
	}
	switch st.State {
	case StateQueued, StateRunning, StateDone, StateFailed:
	default:
		return Status{}, false, fmt.Errorf("read update status: unknown state %q", st.State)
	}
	if st.From != "" && !validReported(st.From) || st.To != "" && !StrictVersion(st.To) {
		return Status{}, false, errors.New("read update status: invalid version")
	}
	return st, true, nil
}

// validReported accepts what the running binary reports: X.Y.Z (with an
// optional suffix) or dev+<commit>.
func validReported(v string) bool {
	if len(v) > 64 {
		return false
	}
	for _, r := range v {
		alnum := r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'
		if !alnum && !strings.ContainsRune(".-+", r) {
			return false
		}
	}
	return true
}

// Heartbeat returns when the helper last ran; ok is false without one.
func (f Files) Heartbeat() (time.Time, bool) {
	b, err := readSmall(f.path(HeartbeatFile), maxHeartbeatBytes)
	if err != nil {
		return time.Time{}, false
	}
	n, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	if err != nil || n <= 0 {
		return time.Time{}, false
	}
	return time.Unix(n, 0).UTC(), true
}

// HelperPresent reports whether the helper ran within HelperFresh of now.
func (f Files) HelperPresent(now time.Time) bool {
	t, ok := f.Heartbeat()
	return ok && now.Sub(t) < HelperFresh && t.Sub(now) < HelperFresh
}

// readSmall reads a regular file of at most limit bytes.
func readSmall(path string, limit int64) ([]byte, error) {
	fh, err := os.Open(path) //nolint:gosec // fixed names inside the update directory
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	st, err := fh.Stat()
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", filepath.Base(path))
	}
	b, err := io.ReadAll(io.LimitReader(fh, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("%s is too large", filepath.Base(path))
	}
	return b, nil
}
