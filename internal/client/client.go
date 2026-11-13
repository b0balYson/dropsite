// Package client is a typed HTTP client for the Dropsite API v1
// (see docs/api-contract.md).
package client

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/b0balYson/dropsite/internal/brand"
)

const (
	// DefaultTimeout bounds a regular API request end to end.
	DefaultTimeout = 60 * time.Second
	// DefaultDeployHeaderTimeout bounds the wait for the deploy response
	// headers after the upload finished (the server unpacks meanwhile).
	DefaultDeployHeaderTimeout = 10 * time.Minute

	apiPrefix    = "/api/v1"
	clientHeader = "X-Dropsite-Client"
	// assistantHeader marks requests made for an AI assistant (the local
	// MCP server): the server then treats the token like one presented to
	// /mcp, without administrator powers.
	assistantHeader = "X-Dropsite-Assistant"
	maxBody         = 16 << 20
)

// Client talks to one server with one token. It is safe for concurrent use.
type Client struct {
	base      string
	token     string
	timeout   time.Duration
	http      *http.Client
	assistant bool
}

// Option configures a Client.
type Option func(*Client)

// WithHTTPClient replaces the underlying HTTP client (its Timeout should be
// zero, otherwise it also caps uploads).
func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.http = h } }

// WithTimeout sets the per-request timeout for everything except Deploy.
func WithTimeout(d time.Duration) Option { return func(c *Client) { c.timeout = d } }

// New creates a client for server, the scheme://host[:port] root of the API host.
func New(server, token string, opts ...Option) *Client {
	c := &Client{
		base:    strings.TrimSuffix(strings.TrimRight(server, "/"), apiPrefix),
		token:   token,
		timeout: DefaultTimeout,
	}
	for _, o := range opts {
		o(c)
	}
	if c.http == nil {
		c.http = defaultHTTPClient()
	}
	return c
}

// WithRootCAFile trusts the PEM certificates in file in addition to the
// system roots (for installations behind a private certificate authority).
func WithRootCAFile(file string) (Option, error) {
	pem, err := os.ReadFile(file) //nolint:gosec // path comes from the user's own DROPSITE_CA_FILE
	if err != nil {
		return nil, fmt.Errorf("read CA file: %w", err)
	}
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("CA file %s contains no PEM certificates", file)
	}
	return func(c *Client) {
		h := defaultHTTPClient()
		if tr, ok := h.Transport.(*http.Transport); ok {
			tr.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
		}
		c.http = h
	}, nil
}

// noRedirect keeps redirects as responses: the API never redirects, and a
// redirect of a path with "." or ".." segments (an ID typed by an assistant)
// must not repeat a DELETE or POST on another endpoint.
func noRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

func defaultHTTPClient() *http.Client {
	tr, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return &http.Client{CheckRedirect: noRedirect}
	}
	tr = tr.Clone()
	tr.ResponseHeaderTimeout = DefaultDeployHeaderTimeout
	return &http.Client{Transport: tr, CheckRedirect: noRedirect}
}

// AsAssistant returns a copy of the client whose requests are marked as made
// for an AI assistant, so the server applies the rights of the MCP endpoint.
func (c *Client) AsAssistant() *Client {
	cp := *c
	cp.assistant = true
	return &cp
}

// Server returns the normalized server root the client talks to.
func (c *Client) Server() string { return c.base }

// UserAgent is sent as both User-Agent and X-Dropsite-Client.
func UserAgent() string { return "cli/" + brand.Version }

func (c *Client) endpoint(path string, q url.Values) string {
	u := c.base + apiPrefix + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	return u
}

func (c *Client) newRequest(ctx context.Context, method, path string, q url.Values, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.endpoint(path, q), body)
	if err != nil {
		return nil, fmt.Errorf("build %s %s: %w", method, path, err)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("User-Agent", UserAgent())
	req.Header.Set(clientHeader, UserAgent())
	if c.assistant {
		req.Header.Set(assistantHeader, "mcp")
	}
	req.Header.Set("Accept", "application/json")
	return req, nil
}

// doJSON performs a bounded request with an optional JSON body and decodes
// the response into out (nil to discard).
func (c *Client) doJSON(ctx context.Context, method, path string, q url.Values, in, out any) error {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	var body io.Reader
	if in != nil {
		// HTML is not escaped: file content would grow by a factor of six.
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(in); err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		body = bytes.NewReader(bytes.TrimSuffix(buf.Bytes(), []byte("\n")))
	}
	req, err := c.newRequest(ctx, method, path, q, body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.send(req, out)
}

// send executes req, maps non-2xx to *APIError and decodes 2xx into out.
func (c *Client) send(req *http.Request, out any) error {
	resp, err := c.http.Do(req)
	if err != nil {
		return c.networkError(req, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return parseAPIError(resp)
	}
	if out == nil || resp.StatusCode == http.StatusNoContent {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxBody))
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(out); err != nil {
		return fmt.Errorf("%s %s: decode response: %w", req.Method, req.URL.Path, err)
	}
	return nil
}

func (c *Client) networkError(req *http.Request, err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%s %s: request to %s timed out: %w", req.Method, req.URL.Path, c.base, err)
	}
	return fmt.Errorf("%s %s: cannot reach %s: %w", req.Method, req.URL.Path, c.base, err)
}

// sitePath is the URL path of one site (ref is an ID or slug).
func sitePath(ref string) string { return "/sites/" + url.PathEscape(ref) }

func listQuery(o ListOptions) url.Values {
	q := url.Values{}
	if o.Limit > 0 {
		q.Set("limit", fmt.Sprint(o.Limit))
	}
	if o.Offset > 0 {
		q.Set("offset", fmt.Sprint(o.Offset))
	}
	return q
}
