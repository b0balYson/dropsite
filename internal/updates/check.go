// Package updates finds out whether a newer dropsite release exists and
// hands an administrator's update request to the server helper.
//
// control never updates itself: it has no Docker access. It writes a small
// request file into the state volume; dropsite-update-watch, a root job on
// the host installed by scripts/server-bootstrap.sh, validates it, runs
// dropsite-update and writes the progress back as a status file.
package updates

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/b0balYson/dropsite/internal/netguard"
	"github.com/b0balYson/dropsite/internal/semver"
)

// Limits of one release check.
const (
	CallTimeout      = 20 * time.Second
	MaxResponseBytes = 1 << 20
	dialTimeout      = 5 * time.Second
	handshakeTimeout = 10 * time.Second
)

// ReleasePageBase is the address of a release's notes; the version follows.
// The link is built here and never taken from the answer.
const ReleasePageBase = "https://github.com/b0balYson/dropsite/releases/tag/v"

// Errors of a check.
var (
	ErrTooLarge   = errors.New("release answer is too large")
	ErrNoRelease  = errors.New("no published release found")
	ErrHost       = errors.New("request to a host other than the release endpoint refused")
	errBadAnswer  = errors.New("release answer is not understood")
	errBadVersion = errors.New("release tag is not a version such as v1.2.3")
)

// Release is the newest stable release.
type Release struct {
	Version semver.Version
	URL     string
}

// ReleaseURL is the notes page of version v.
func ReleaseURL(v semver.Version) string { return ReleasePageBase + v.String() }

// Checker asks one release endpoint for the newest release.
type Checker struct {
	endpoint *url.URL
	client   *http.Client
}

// NewChecker returns a checker of endpoint (the GitHub API "releases/latest"
// or "releases" address). A nil transport means the guarded production
// transport: public addresses only, no environment proxy.
func NewChecker(endpoint string, tr http.RoundTripper) (*Checker, error) {
	u, err := url.Parse(endpoint)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil {
		return nil, fmt.Errorf("invalid release endpoint %q", endpoint)
	}
	if tr == nil {
		tr = NewTransport(u.Hostname())
	}
	c := &Checker{endpoint: u}
	c.client = &http.Client{
		Transport: hostGuard{scheme: u.Scheme, host: strings.ToLower(u.Host), next: tr},
		Timeout:   CallTimeout,
		// GitHub answers releases/latest directly; any redirect is refused.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return c, nil
}

// NewTransport connects only to public addresses, except when the endpoint
// is a loopback test server (UPDATE_CHECK_URL in tests).
func NewTransport(host string) http.RoundTripper {
	dialer := &net.Dialer{Timeout: dialTimeout}
	if host != "127.0.0.1" && host != "localhost" && host != "::1" {
		dialer.Control = netguard.Control(netguard.PublicAddr)
	}
	return &http.Transport{
		Proxy:                 nil,
		DialContext:           dialer.DialContext,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout:   handshakeTimeout,
		ResponseHeaderTimeout: CallTimeout,
		MaxIdleConns:          2,
		IdleConnTimeout:       30 * time.Second,
		ForceAttemptHTTP2:     true,
	}
}

// hostGuard lets through only requests to the endpoint's own scheme and host.
type hostGuard struct {
	scheme, host string
	next         http.RoundTripper
}

func (g hostGuard) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL == nil || r.URL.Scheme != g.scheme || strings.ToLower(r.URL.Host) != g.host || r.URL.User != nil {
		if r.Body != nil {
			_ = r.Body.Close()
		}
		return nil, ErrHost
	}
	return g.next.RoundTrip(r)
}

// release is the part of a GitHub release object the check reads.
type release struct {
	TagName    string `json:"tag_name"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
}

// Latest returns the newest stable release (no draft, no pre-release).
func (c *Checker) Latest(ctx context.Context) (Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint.String(), nil)
	if err != nil {
		return Release{}, fmt.Errorf("build release request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "dropsite-update-check")
	resp, err := c.client.Do(req)
	if err != nil {
		return Release{}, fmt.Errorf("ask for the latest release: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("ask for the latest release: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBytes+1))
	if err != nil {
		return Release{}, fmt.Errorf("read the latest release: %w", err)
	}
	if len(body) > MaxResponseBytes {
		return Release{}, ErrTooLarge
	}
	return parseReleases(body)
}

// parseReleases reads one release object (releases/latest) or a list of
// them (releases) and returns the newest stable one.
func parseReleases(body []byte) (Release, error) {
	body = bytes.TrimSpace(body)
	var list []release
	if len(body) > 0 && body[0] == '[' {
		if err := json.Unmarshal(body, &list); err != nil {
			return Release{}, errBadAnswer
		}
	} else {
		var one release
		if err := json.Unmarshal(body, &one); err != nil {
			return Release{}, errBadAnswer
		}
		list = []release{one}
	}
	var best *semver.Version
	sawTag := false
	for _, r := range list {
		if r.Draft || r.Prerelease {
			continue
		}
		v, ok := stableVersion(r.TagName)
		if !ok {
			sawTag = true
			continue
		}
		if best == nil || semver.Compare(v, *best) > 0 {
			best = &v
		}
	}
	if best == nil {
		if sawTag {
			return Release{}, errBadVersion
		}
		return Release{}, ErrNoRelease
	}
	return Release{Version: *best, URL: ReleaseURL(*best)}, nil
}

// stableVersion parses "vX.Y.Z" (or "X.Y.Z") without a pre-release suffix
// or build metadata.
func stableVersion(tag string) (semver.Version, bool) {
	if !StrictVersion(strings.TrimPrefix(tag, "v")) {
		return semver.Version{}, false
	}
	return semver.Parse(tag)
}

// StrictVersion reports whether s is exactly X.Y.Z: digits only, at most
// six per part, no leading zeros, no prefix or suffix. It is the same rule
// the host helper applies to a request.
func StrictVersion(s string) bool {
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return false
	}
	for _, p := range parts {
		if p == "" || len(p) > 6 || (len(p) > 1 && p[0] == '0') {
			return false
		}
		for i := 0; i < len(p); i++ {
			if p[i] < '0' || p[i] > '9' {
				return false
			}
		}
	}
	return true
}
