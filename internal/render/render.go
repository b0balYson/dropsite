// Package render talks to the optional screenshot renderer (Gotenberg with
// Chromium, see docs/screenshots.md) and serves it the files of one release.
// control never runs a browser: the renderer runs in its own container on an
// internal network and loads the page from control's render listener, which
// picks the release by a one-time token sent in TokenHeader.
package render

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// TokenHeader carries the render token on every request of the renderer.
const TokenHeader = "X-Dropsite-Render-Token"

// screenshotPath is Gotenberg's route for a screenshot of a URL.
const screenshotPath = "/forms/chromium/screenshot/url"

// maxImageBytes bounds a screenshot answer.
const maxImageBytes = 20 << 20

// Timeout bounds one screenshot.
const Timeout = 45 * time.Second

// ErrUnavailable means the renderer could not be reached or failed.
var ErrUnavailable = errors.New("renderer unavailable")

// Client calls the renderer.
type Client struct {
	base string
	hc   *http.Client
}

// New returns a client for the renderer at baseURL.
func New(baseURL string, hc *http.Client) *Client {
	if hc == nil {
		hc = &http.Client{Timeout: Timeout}
	}
	return &Client{base: strings.TrimSuffix(baseURL, "/"), hc: hc}
}

// Shot is one screenshot request: the page to load, the token the page's
// requests carry, and the viewport. The image is the first screen only.
type Shot struct {
	URL    string
	Token  string
	Width  int
	Height int
}

// Screenshot returns the PNG of the first screen of shot.URL.
func (c *Client) Screenshot(ctx context.Context, shot Shot) ([]byte, error) {
	headers, err := json.Marshal(map[string]string{TokenHeader: shot.Token})
	if err != nil {
		return nil, err
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for _, f := range [][2]string{
		{"url", shot.URL},
		{"width", strconv.Itoa(shot.Width)},
		{"height", strconv.Itoa(shot.Height)},
		{"clip", "true"},
		{"format", "png"},
		{"emulatedMediaType", "screen"},
		{"skipNetworkIdleEvent", "false"},
		{"extraHttpHeaders", string(headers)},
	} {
		if err := mw.WriteField(f[0], f[1]); err != nil {
			return nil, err
		}
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+screenshotPath, &body)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := c.hc.Do(req) //nolint:gosec // the renderer URL comes from the operator's configuration
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxImageBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%w: read answer: %w", ErrUnavailable, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: status %d: %s", ErrUnavailable, resp.StatusCode, firstLine(data))
	}
	if len(data) > maxImageBytes {
		return nil, fmt.Errorf("%w: screenshot is larger than %d bytes", ErrUnavailable, maxImageBytes)
	}
	if !bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")) {
		return nil, fmt.Errorf("%w: the answer is not a PNG image", ErrUnavailable)
	}
	return data, nil
}

func firstLine(b []byte) string {
	s := strings.TrimSpace(string(b))
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

// Release opens the files a token grants; done releases them.
type Release func(token string) (files fs.FS, done func(), ok bool)

// Handler serves the release a request's token grants, read only. Without
// a valid token every path is "not found".
func Handler(open Release) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		files, done, ok := open(r.Header.Get(TokenHeader))
		if !ok {
			http.NotFound(w, r)
			return
		}
		defer done()
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		http.FileServerFS(files).ServeHTTP(w, r)
	})
}
