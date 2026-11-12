package sitefetch

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
)

// GetOptions describe one document download.
type GetOptions struct {
	// Types are the accepted media types, e.g. text/html.
	Types []string
	// MaxBytes caps the document (at most MaxFileBytes).
	MaxBytes int64
	// Accept is the Accept header.
	Accept string
	// UserAgent replaces the default User-Agent.
	UserAgent string
}

// Document is a downloaded document.
type Document struct {
	// URL is the address after redirects.
	URL         string
	ContentType string
	Data        []byte
}

// Get downloads one document of an accepted type through the same guarded
// transport as Fetch: https on port 443 only, public addresses only (each
// redirect hop included), bounded size. ctx bounds the time.
func (c *Client) Get(ctx context.Context, rawURL string, o GetOptions) (Document, error) {
	if err := ValidateURL(rawURL); err != nil {
		return Document{}, fail(rawURL, err.Error())
	}
	limit := o.MaxBytes
	if limit <= 0 || limit > MaxFileBytes {
		limit = MaxFileBytes
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSpace(rawURL), nil)
	if err != nil {
		return Document{}, fail(rawURL, "invalid address")
	}
	ua := o.UserAgent
	if ua == "" {
		ua = userAgent
	}
	req.Header.Set("User-Agent", ua)
	if o.Accept != "" {
		req.Header.Set("Accept", o.Accept)
	}
	resp, err := c.http.Do(req) //nolint:gosec // SSRF guarded: public addresses only, checked on every connection
	if err != nil {
		return Document{}, fail(rawURL, describe(err))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return Document{}, fail(rawURL, fmt.Sprintf("the server answered HTTP %d", resp.StatusCode))
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return Document{}, fail(rawURL, describe(err))
	}
	if int64(len(data)) > limit {
		return Document{}, fail(rawURL, fmt.Sprintf("the document is larger than %d KB", limit>>10))
	}
	ct := contentType(resp.Header.Get("Content-Type"), data)
	if !slices.Contains(o.Types, ct) {
		return Document{}, fail(rawURL, fmt.Sprintf("content type %q is not accepted here", ct))
	}
	return Document{URL: resp.Request.URL.String(), ContentType: ct, Data: data}, nil
}
