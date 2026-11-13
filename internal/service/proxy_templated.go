package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/b0balYson/dropsite/internal/domain"
)

// Calls of the templated classes (ai, send): the visitor sends only the
// declared fields as a JSON object with POST to the endpoint itself; the
// server sends the stored template to the upstream API.

const sseMaxLine = 1 << 20

func (s *Service) openTemplated(ctx context.Context, site domain.Site, host string, e domain.ProxyEndpoint, c ProxyCall) (*ProxyResponse, error) {
	values, err := s.checkTemplatedCall(ctx, site, host, e, c)
	if err != nil {
		s.recordProxyRejected(ctx, e.ID, proxyStatusOf(err))
		return nil, err
	}
	s.observeCall(ctx, c.IP, "proxy:"+e.SiteID+"/"+e.Name)
	resp, err := s.forwardTemplated(ctx, e, host, values)
	return s.finishProxy(ctx, e, resp, err)
}

// checkTemplatedCall runs the checks of a templated call, cheapest first,
// and returns the visitor's field values.
func (s *Service) checkTemplatedCall(ctx context.Context, site domain.Site, host string, e domain.ProxyEndpoint, c ProxyCall) (map[string]any, error) {
	if !sameOrigin(c.Origin, c.Host) || c.FetchSite == "cross-site" || c.FetchSite == "same-site" {
		return nil, errProxyOrigin
	}
	if c.Method != http.MethodPost {
		return nil, errProxyMethod
	}
	if c.Path != "" && c.Path != "/" {
		return nil, errProxyPath
	}
	if err := s.useSession(e, c.IP, c.Header.Get(SessionHeader)); err != nil {
		return nil, err
	}
	if err := s.proxyRateLimits(e, site.ID, c.IP); err != nil {
		return nil, err
	}
	if !e.SecretSet() {
		return nil, errProxyNoKey
	}
	if e.Class == domain.ProxyClassSend {
		if err := s.usePow(ctx, host, c.Header.Get(PowHeader)); err != nil {
			return nil, err
		}
	}
	body, err := readProxyBody(c.Body)
	if err != nil {
		return nil, err
	}
	values, err := parseProxyFields(*e.Template, body)
	if err != nil {
		return nil, err
	}
	if err := s.spendBudget(ctx, site, e); err != nil {
		return nil, err
	}
	return values, nil
}

func (s *Service) forwardTemplated(ctx context.Context, e domain.ProxyEndpoint, host string, values map[string]any) (*ProxyResponse, error) {
	t := *e.Template
	body, err := renderProxyBody(t, values)
	if err != nil {
		return nil, err
	}
	secret, err := s.sealer.Open(e.SecretEnc, proxySealContext(e))
	if err != nil {
		s.log.Warn("proxy secret cannot be opened", "site", e.SiteID, "endpoint", e.Name, "err", err)
		return nil, errProxyNoKey
	}
	var segs []string
	if t.Path != "" && t.Path != "/" {
		segs = strings.Split(strings.TrimPrefix(t.Path, "/"), "/")
	}
	target, err := proxyTarget(e, secret, segs, "")
	if err != nil {
		return nil, errProxyPath
	}
	hdr := http.Header{"Content-Type": {"application/json"}, "Accept": {"application/json"}}
	if t.Stream {
		hdr.Set("Accept", "text/event-stream")
	}
	injectHeader(hdr, e, secret)
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), ProxyTimeout)
	resp, err := s.proxyDo(ctx, e, secret, t.Method, target, hdr, body)
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
	upstream := &limitedBody{ReadCloser: resp.Body, n: ProxyMaxResponseBytes}
	switch {
	case t.Response == "":
		return &ProxyResponse{Status: resp.StatusCode, Header: proxyResponseHeader(resp.Header),
			Body: cancelBody{ReadCloser: upstream, cancel: cancel}}, nil
	case t.Stream && resp.StatusCode < 300 && strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream"):
		h := proxyResponseHeader(nil)
		h.Set("Content-Type", "text/event-stream; charset=utf-8")
		return &ProxyResponse{Status: http.StatusOK, Header: h, Body: newSSEReducer(upstream, cancel, t.Response)}, nil
	}
	defer cancel()
	defer resp.Body.Close()
	return s.extractedResponse(e, upstream, resp.StatusCode, t.Response)
}

// extractedResponse reads the whole answer and returns only the value at
// path as {"result": value}; an upstream error becomes proxy_upstream_status
// without its body (it may describe the owner's account).
func (s *Service) extractedResponse(e domain.ProxyEndpoint, body io.Reader, status int, path string) (*ProxyResponse, error) {
	raw, err := io.ReadAll(body)
	if err != nil {
		return nil, errProxyUpstream.WithParams(map[string]any{"reason": "response too large"})
	}
	if status >= http.StatusBadRequest {
		s.log.Info("proxy upstream refused", "site", e.SiteID, "endpoint", e.Name, "status", status)
		return nil, domain.E(domain.KindInvalid, domain.CodeProxyStatus,
			"the upstream service answered "+strconv.Itoa(status)).WithParams(map[string]any{"status": status})
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, errProxyUpstream.WithParams(map[string]any{"reason": "the answer is not JSON"})
	}
	val, _ := extractJSONPath(v, path)
	out, err := json.Marshal(map[string]any{"result": val})
	if err != nil {
		return nil, errProxyUpstream
	}
	h := proxyResponseHeader(nil)
	h.Set("Content-Type", "application/json; charset=utf-8")
	return &ProxyResponse{Status: http.StatusOK, Header: h, Body: io.NopCloser(bytes.NewReader(out))}, nil
}

// sseReducer turns an upstream event stream into events that carry only
// the value at path, as JSON ("data: \"text\""); [DONE] passes through.
type sseReducer struct {
	*io.PipeReader
	upstream io.Closer
	cancel   context.CancelFunc
}

func newSSEReducer(upstream io.ReadCloser, cancel context.CancelFunc, path string) *sseReducer {
	pr, pw := io.Pipe()
	go func() {
		sc := bufio.NewScanner(upstream)
		sc.Buffer(make([]byte, 64<<10), sseMaxLine)
		for sc.Scan() {
			out, ok := reduceSSELine(sc.Text(), path)
			if !ok {
				continue
			}
			if _, err := pw.Write([]byte("data: " + out + "\n\n")); err != nil {
				return
			}
		}
		_ = pw.CloseWithError(sc.Err())
	}()
	return &sseReducer{PipeReader: pr, upstream: upstream, cancel: cancel}
}

func (r *sseReducer) Close() error {
	_ = r.PipeReader.Close()
	err := r.upstream.Close()
	r.cancel()
	return err
}

func reduceSSELine(line, path string) (string, bool) {
	data, ok := strings.CutPrefix(line, "data:")
	if !ok {
		return "", false
	}
	data = strings.TrimSpace(data)
	if data == "[DONE]" {
		return data, true
	}
	dec := json.NewDecoder(strings.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return "", false
	}
	val, found := extractJSONPath(v, path)
	if !found || val == nil {
		return "", false
	}
	b, err := json.Marshal(val)
	if err != nil {
		return "", false
	}
	return string(b), true
}
