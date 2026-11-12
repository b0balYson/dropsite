package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"unicode"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/store"
)

// Secret proxy size limits.
const (
	ProxyMaxRequestBytes  = 1 << 20
	ProxyMaxResponseBytes = 10 << 20
	proxyMaxRedirects     = 3
)

// ProxyCall is a visitor request to /_dropsite/proxy/<name><path>.
type ProxyCall struct {
	Host string // Host header as received
	Name string
	// Path is the escaped rest after the name: "" or starting with "/".
	Path      string
	RawQuery  string
	Method    string
	Origin    string
	FetchSite string // Sec-Fetch-Site
	IP        string
	Header    http.Header
	Body      io.Reader
}

// ProxyResponse is the upstream answer to copy to the visitor. Body must be
// closed; reading past ProxyMaxResponseBytes returns ErrProxyResponseTooLarge.
type ProxyResponse struct {
	Status int
	Header http.Header
	Body   io.ReadCloser
}

// ErrProxyResponseTooLarge ends a response body cut at the size limit.
var ErrProxyResponseTooLarge = errors.New("proxy response exceeds the size limit")

func proxyErr(kind domain.Kind, code, msg string) *domain.Error { return domain.E(kind, code, msg) }

var (
	errProxyOrigin = proxyErr(domain.KindInvalid, domain.CodeProxyOrigin, "the proxy was called from another site")
	errProxyMethod = proxyErr(domain.KindInvalid, domain.CodeProxyMethod, "this method is not allowed for the endpoint")
	errProxyPath   = proxyErr(domain.KindInvalid, domain.CodeProxyPath, "this path is not allowed for the endpoint")
	errProxyNoKey  = proxyErr(domain.KindInvalid, domain.CodeProxySecretMissing,
		"the endpoint has no key yet; the site owner pastes it in the web interface")
	errProxyTooLarge = proxyErr(domain.KindTooLarge, domain.CodeProxyTooLarge, "the request body is too large")
	errProxyUpstream = proxyErr(domain.KindInvalid, domain.CodeProxyUpstream, "the upstream service could not be reached")
	errProxyTimeout  = proxyErr(domain.KindInvalid, domain.CodeProxyTimeout, "the upstream service did not answer in time")
)

// ProxyErrorStatus is the HTTP status of a visitor-facing proxy error code.
func ProxyErrorStatus(code string) (int, bool) {
	switch code {
	case domain.CodeProxyOrigin, domain.CodeProxyPath:
		return http.StatusForbidden, true
	case domain.CodeProxyMethod:
		return http.StatusMethodNotAllowed, true
	case domain.CodeProxySecretMissing:
		return http.StatusServiceUnavailable, true
	case domain.CodeProxyTooLarge:
		return http.StatusRequestEntityTooLarge, true
	case domain.CodeProxyUpstream:
		return http.StatusBadGateway, true
	case domain.CodeProxyTimeout:
		return http.StatusGatewayTimeout, true
	}
	return 0, false
}

func proxyStatusOf(err error) int {
	e := domain.AsError(err)
	if st, ok := ProxyErrorStatus(e.Code); ok {
		return st
	}
	if e.Kind == domain.KindRateLimited {
		return http.StatusTooManyRequests
	}
	return http.StatusInternalServerError
}

// OpenProxy forwards a visitor call to the endpoint's upstream with the
// stored secret and returns the answer to stream back.
func (s *Service) OpenProxy(ctx context.Context, c ProxyCall) (*ProxyResponse, error) {
	site, host, err := s.siteForHost(ctx, c.Host)
	if err != nil {
		return nil, err
	}
	name, err := normalizeProxyName(c.Name)
	if err != nil {
		return nil, domain.ErrNotFound
	}
	e, err := s.repo.GetProxyEndpoint(ctx, site.ID, name)
	if err != nil {
		return nil, err
	}
	if !e.Enabled {
		return nil, domain.ErrNotFound
	}
	segs, err := s.checkProxyCall(e, site.ID, c)
	if err != nil {
		s.recordProxy(ctx, e.ID, store.ProxyRejected, proxyStatusOf(err))
		return nil, err
	}
	resp, err := s.forwardProxy(ctx, e, host, segs, c)
	if err != nil {
		outcome := store.ProxyFailed
		if code := domain.AsError(err).Code; code == domain.CodeProxySecretMissing || code == domain.CodeProxyTooLarge {
			outcome = store.ProxyRejected
		}
		s.recordProxy(ctx, e.ID, outcome, proxyStatusOf(err))
		return nil, err
	}
	outcome := store.ProxyForwarded
	if resp.Status >= http.StatusInternalServerError {
		outcome = store.ProxyFailed
	}
	s.recordProxy(ctx, e.ID, outcome, resp.Status)
	return resp, nil
}

func (s *Service) recordProxy(ctx context.Context, id int64, outcome store.ProxyOutcome, status int) {
	if err := s.repo.RecordProxyUse(context.WithoutCancel(ctx), id, outcome, status, s.now()); err != nil {
		s.log.Warn("proxy usage not recorded", "endpoint", id, "err", err)
	}
}

// checkProxyCall applies the cheap checks in an order that keeps refused
// calls from other sites off the site's quota. It returns the decoded
// path segments.
func (s *Service) checkProxyCall(e domain.ProxyEndpoint, siteID string, c ProxyCall) ([]string, error) {
	if !sameOrigin(c.Origin, c.Host) || c.FetchSite == "cross-site" || c.FetchSite == "same-site" {
		return nil, errProxyOrigin
	}
	if !slices.Contains(e.Methods, c.Method) {
		return nil, errProxyMethod
	}
	segs, ok := proxySegments(c.Path)
	if !ok || !proxyPathAllowed(e.Paths, segs) {
		return nil, errProxyPath
	}
	if ok, retry := s.proxyIPLimit.Allow(siteID + "|" + ipKey(c.IP)); !ok {
		return nil, domain.ErrRateLimited.WithRetryAfter(retry)
	}
	if ok, retry := s.proxySiteLimit.Allow(siteID); !ok {
		return nil, domain.ErrRateLimited.WithRetryAfter(retry)
	}
	if !e.SecretSet() {
		return nil, errProxyNoKey
	}
	return segs, nil
}

// proxySegments decodes an escaped path. Dot segments, encoded slashes and
// backslashes and control characters are refused.
func proxySegments(escaped string) ([]string, bool) {
	if escaped == "" || escaped == "/" {
		return nil, true
	}
	if !strings.HasPrefix(escaped, "/") {
		return nil, false
	}
	raw := strings.Split(escaped[1:], "/")
	out := make([]string, 0, len(raw))
	for i, r := range raw {
		seg, err := url.PathUnescape(r)
		if err != nil || seg == "." || seg == ".." || strings.ContainsAny(seg, "/\\") ||
			strings.IndexFunc(seg, unicode.IsControl) >= 0 {
			return nil, false
		}
		if seg == "" && i != len(raw)-1 {
			return nil, false // "//" inside the path
		}
		out = append(out, seg)
	}
	return out, true
}

// proxyPathAllowed matches the decoded path against the endpoint's prefixes
// on segment boundaries; no prefixes allow every path.
func proxyPathAllowed(prefixes, segs []string) bool {
	if len(prefixes) == 0 {
		return true
	}
	p := "/" + strings.Join(segs, "/")
	for _, pre := range prefixes {
		if pre == "/" || p == pre || strings.HasPrefix(p, pre+"/") {
			return true
		}
	}
	return false
}

// proxyDropRequest lists visitor headers that are never forwarded.
var proxyDropRequest = map[string]bool{
	"Cookie": true, "Authorization": true, "Proxy-Authorization": true, "Host": true, "Origin": true,
	"Referer": true, "Connection": true, "Keep-Alive": true, "Te": true, "Trailer": true,
	"Transfer-Encoding": true, "Upgrade": true, "Proxy-Connection": true, "Forwarded": true,
	"X-Real-Ip": true, "Accept-Encoding": true, "Content-Length": true, "Expect": true, "Via": true,
	// Upstreams that honour these would run another method or path than
	// the endpoint allows.
	"X-Http-Method-Override": true, "X-Http-Method": true, "X-Method-Override": true, "X-Original-Method": true,
	"X-Original-Url": true, "X-Rewrite-Url": true,
}

// proxyPassResponse lists upstream headers returned to the visitor.
var proxyPassResponse = []string{"Content-Type", "Content-Language", "Content-Disposition", "Etag", "Last-Modified", "Retry-After"}

func proxyRequestHeader(in http.Header, e domain.ProxyEndpoint) http.Header {
	out := http.Header{}
	drop := map[string]bool{}
	for _, v := range in.Values("Connection") {
		for _, f := range strings.Split(v, ",") {
			drop[http.CanonicalHeaderKey(strings.TrimSpace(f))] = true
		}
	}
	if e.Auth == domain.ProxyAuthHeader {
		drop[http.CanonicalHeaderKey(e.AuthName)] = true
	}
	for k, vs := range in {
		ck := http.CanonicalHeaderKey(k)
		if proxyDropRequest[ck] || drop[ck] || strings.HasPrefix(ck, "Sec-") || strings.HasPrefix(ck, "X-Forwarded-") ||
			strings.HasPrefix(ck, "Cf-") {
			continue
		}
		out[ck] = slices.Clone(vs)
	}
	return out
}

func proxyResponseHeader(in http.Header) http.Header {
	out := http.Header{}
	for _, k := range proxyPassResponse {
		if vs := in.Values(k); len(vs) > 0 {
			out[k] = slices.Clone(vs)
		}
	}
	out.Set("Cache-Control", "no-store")
	out.Set("X-Content-Type-Options", "nosniff")
	out.Set("X-Robots-Tag", "noindex")
	out.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	return out
}

// proxyTarget builds the first upstream URL.
func proxyTarget(e domain.ProxyEndpoint, secret string, segs []string, rawQuery string) (*url.URL, error) {
	base := e.UpstreamURL
	if e.Auth == domain.ProxyAuthPath {
		base = strings.Replace(base, domain.ProxySecretMark, url.PathEscape(secret), 1)
	}
	u, err := url.Parse(base)
	if err != nil {
		return nil, err
	}
	if len(segs) > 0 {
		esc := make([]string, len(segs))
		for i, s := range segs {
			esc[i] = url.PathEscape(s)
		}
		u.RawPath = strings.TrimSuffix(u.EscapedPath(), "/") + "/" + strings.Join(esc, "/")
		if u.Path, err = url.PathUnescape(u.RawPath); err != nil {
			return nil, err
		}
	}
	q := u.Query()
	visitor, _ := url.ParseQuery(rawQuery)
	if e.Auth == domain.ProxyAuthQuery {
		visitor.Del(e.AuthName)
	}
	for k, vs := range visitor {
		for _, v := range vs {
			q.Add(k, v)
		}
	}
	u.RawQuery = q.Encode()
	injectQuery(u, e, secret)
	return u, nil
}

func injectQuery(u *url.URL, e domain.ProxyEndpoint, secret string) {
	if e.Auth != domain.ProxyAuthQuery {
		return
	}
	q := u.Query()
	q.Set(e.AuthName, secret)
	u.RawQuery = q.Encode()
}

func injectHeader(h http.Header, e domain.ProxyEndpoint, secret string) {
	switch e.Auth {
	case domain.ProxyAuthHeader:
		h.Set(e.AuthName, secret)
	case domain.ProxyAuthBearer:
		h.Set("Authorization", "Bearer "+secret)
	}
}

// cancelBody cancels the upstream request when the visitor's copy ends.
type cancelBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b cancelBody) Close() error {
	err := b.ReadCloser.Close()
	b.cancel()
	return err
}

// limitedBody returns ErrProxyResponseTooLarge once more than n bytes were read.
type limitedBody struct {
	io.ReadCloser
	n int64
}

func (b *limitedBody) Read(p []byte) (int, error) {
	if b.n <= 0 {
		var one [1]byte
		k, err := b.ReadCloser.Read(one[:])
		if k > 0 {
			return 0, ErrProxyResponseTooLarge
		}
		return 0, err
	}
	if int64(len(p)) > b.n {
		p = p[:b.n]
	}
	k, err := b.ReadCloser.Read(p)
	b.n -= int64(k)
	return k, err
}

func (s *Service) forwardProxy(ctx context.Context, e domain.ProxyEndpoint, host string, segs []string, c ProxyCall) (*ProxyResponse, error) {
	body, err := readProxyBody(c.Body)
	if err != nil {
		return nil, err
	}
	secret, err := s.sealer.Open(e.SecretEnc, proxySealContext(e))
	if err != nil {
		s.log.Warn("proxy secret cannot be opened", "site", e.SiteID, "endpoint", e.Name, "err", err)
		return nil, errProxyNoKey
	}
	target, err := proxyTarget(e, secret, segs, c.RawQuery)
	if err != nil {
		return nil, errProxyPath
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), ProxyTimeout)
	hdr := proxyRequestHeader(c.Header, e)
	injectHeader(hdr, e, secret)
	resp, err := s.proxyDo(ctx, e, secret, c.Method, target, hdr, body)
	if err != nil {
		cancel()
		if !isProxyDomainErr(err) {
			s.log.Info("proxy upstream failed", "site", e.SiteID, "endpoint", e.Name, "host", host, "err", redactURLErr(err))
		}
		return nil, classifyProxyErr(ctx, err)
	}
	if resp.ContentLength > ProxyMaxResponseBytes {
		_ = resp.Body.Close()
		cancel()
		return nil, errProxyUpstream.WithParams(map[string]any{"reason": "response too large"})
	}
	return &ProxyResponse{
		Status: resp.StatusCode,
		Header: proxyResponseHeader(resp.Header),
		Body:   cancelBody{ReadCloser: &limitedBody{ReadCloser: resp.Body, n: ProxyMaxResponseBytes}, cancel: cancel},
	}, nil
}

func readProxyBody(r io.Reader) ([]byte, error) {
	if r == nil {
		return nil, nil
	}
	b, err := io.ReadAll(io.LimitReader(r, ProxyMaxRequestBytes+1))
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return nil, errProxyTooLarge
		}
		return nil, errProxyUpstream.WithCause(err)
	}
	if len(b) > ProxyMaxRequestBytes {
		return nil, errProxyTooLarge
	}
	return b, nil
}

func isRedirect(code int) bool {
	switch code {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther,
		http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		return true
	}
	return false
}

// proxyDo sends the request and follows at most proxyMaxRedirects
// redirects, each to an https host of the endpoint, adding the secret again.
func (s *Service) proxyDo(ctx context.Context, e domain.ProxyEndpoint, secret, method string, target *url.URL,
	hdr http.Header, body []byte) (*http.Response, error) {
	for hop := 0; ; hop++ {
		var rd io.Reader
		if body != nil {
			rd = bytes.NewReader(body)
		}
		req, err := http.NewRequestWithContext(ctx, method, target.String(), rd)
		if err != nil {
			return nil, errProxyPath.WithCause(err)
		}
		req.Header = hdr.Clone()
		resp, err := s.proxyClient.Do(req) //nolint:gosec // destination checked against the endpoint's hosts; dialer guarded by netguard
		if err != nil {
			return nil, err
		}
		if !isRedirect(resp.StatusCode) {
			return resp, nil
		}
		loc, lerr := resp.Location()
		_ = resp.Body.Close()
		if lerr != nil || hop >= proxyMaxRedirects || !proxyHostAllowed(e, loc) {
			return nil, errProxyUpstream.WithParams(map[string]any{"reason": "redirect not allowed"})
		}
		if resp.StatusCode != http.StatusTemporaryRedirect && resp.StatusCode != http.StatusPermanentRedirect &&
			method != http.MethodGet && method != http.MethodHead {
			method, body = http.MethodGet, nil
			hdr.Del("Content-Type")
		}
		injectQuery(loc, e, secret)
		target = loc
	}
}

func proxyHostAllowed(e domain.ProxyEndpoint, u *url.URL) bool {
	if u.Scheme != "https" || u.User != nil || (u.Port() != "" && u.Port() != "443") {
		return false
	}
	return slices.Contains(e.AllowedHosts, strings.ToLower(strings.TrimSuffix(u.Hostname(), ".")))
}

func isProxyDomainErr(err error) bool {
	var de *domain.Error
	return errors.As(err, &de)
}

func classifyProxyErr(ctx context.Context, err error) error {
	if isProxyDomainErr(err) {
		return err
	}
	var ne net.Error
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) ||
		(errors.As(err, &ne) && ne.Timeout()) {
		return errProxyTimeout
	}
	if isBlockedDial(err) {
		return errProxyUpstream.WithParams(map[string]any{"reason": "address not allowed"})
	}
	return errProxyUpstream
}

// redactURLErr keeps the secret out of logs: a *url.Error carries the full
// request URL, which holds the key for the query and path placements.
func redactURLErr(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Op + ": " + ue.Err.Error()
	}
	return err.Error()
}
