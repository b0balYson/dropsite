package api

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/service"
)

// registerProxyPath mounts the secret proxy that pages of a site call on
// their own address (Caddy forwards /_dropsite/proxy/* after the access
// check of protected sites). It is not part of API v1.
func (a *api) registerProxyPath(mux *http.ServeMux) {
	mux.Handle(service.ProxyPathPrefix, a.wrap(service.ProxyPathPrefix+"{name}/*", a.proxyCall))
}

// proxyCopyBuffer is the chunk size of streamed responses.
const proxyCopyBuffer = 32 << 10

func (a *api) proxyCall(w http.ResponseWriter, r *http.Request) error {
	rest := strings.TrimPrefix(r.URL.EscapedPath(), service.ProxyPathPrefix)
	name, path, _ := strings.Cut(rest, "/")
	if path != "" || strings.HasSuffix(rest, "/") {
		path = "/" + path
	}
	resp, err := a.svc.OpenProxy(r.Context(), service.ProxyCall{
		Host: r.Host, Name: name, Path: path, RawQuery: r.URL.RawQuery, Method: r.Method,
		Origin: r.Header.Get("Origin"), FetchSite: r.Header.Get("Sec-Fetch-Site"), IP: infoFrom(r.Context()).ip,
		Header: r.Header, Body: http.MaxBytesReader(w, r.Body, service.ProxyMaxRequestBytes),
	})
	if err != nil {
		a.proxyError(w, r, err)
		return nil
	}
	defer resp.Body.Close()
	h := w.Header()
	for k, vs := range resp.Header {
		h[k] = vs
	}
	w.WriteHeader(resp.Status)
	if r.Method == http.MethodHead {
		return nil
	}
	if err := streamProxyBody(w, resp.Body); errors.Is(err, service.ErrProxyResponseTooLarge) {
		// The status is already sent: break the connection so the page
		// sees a failed response, not a silently cut one.
		panic(http.ErrAbortHandler)
	}
	return nil
}

// streamProxyBody copies the upstream body, flushing every chunk so
// streamed answers (text/event-stream) reach the page without delay.
func streamProxyBody(w http.ResponseWriter, body io.Reader) error {
	rc := http.NewResponseController(w)
	buf := make([]byte, proxyCopyBuffer)
	for {
		n, err := body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return werr
			}
			_ = rc.Flush()
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// proxyError writes a refusal in the API error format with the status the
// service assigns to each proxy code.
func (a *api) proxyError(w http.ResponseWriter, r *http.Request, err error) {
	e := domain.AsError(err)
	if e.Kind == domain.KindInternal {
		a.logInternal(r, err)
		e = internalError()
	}
	status, ok := service.ProxyErrorStatus(e.Code)
	if !ok {
		status = statusFor(e)
	}
	h := w.Header()
	if status == http.StatusTooManyRequests {
		h.Set("Retry-After", retryAfterSeconds(e))
	}
	h.Set("X-Robots-Tag", "noindex")
	writeJSON(w, status, errorBody(domain.E(e.Kind, e.Code, e.Message)))
}
