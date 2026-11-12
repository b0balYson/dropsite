package ui

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/service"
	"github.com/b0balYson/dropsite/internal/testenv"
)

// harness is a running UI on a real service with one browser-like client.
type harness struct {
	t   *testing.T
	env *testenv.Env
	srv *httptest.Server
	c   *http.Client
	log *bytes.Buffer
}

func newHarness(t *testing.T, opts ...testenv.Option) *harness {
	t.Helper()
	env := testenv.New(t, opts...)
	logs := &bytes.Buffer{}
	srv := httptest.NewServer(New(env.Svc, slog.New(slog.NewTextHandler(logs, nil))))
	t.Cleanup(srv.Close)
	h := &harness{t: t, env: env, srv: srv, log: logs}
	h.c = h.newClient()
	return h
}

// newClient returns a client with its own cookie jar that does not follow redirects.
func (h *harness) newClient() *http.Client {
	jar, err := cookiejar.New(nil)
	if err != nil {
		h.t.Fatal(err)
	}
	return &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
}

type page struct {
	status int
	body   string
	resp   *http.Response
}

func (p page) location() string { return p.resp.Header.Get("Location") }

func (h *harness) do(req *http.Request) page {
	h.t.Helper()
	resp, err := h.c.Do(req)
	if err != nil {
		h.t.Fatalf("%s %s: %v", req.Method, req.URL, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		h.t.Fatal(err)
	}
	return page{status: resp.StatusCode, body: string(b), resp: resp}
}

func (h *harness) get(path string, headers ...string) page {
	h.t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, h.srv.URL+path, nil)
	if err != nil {
		h.t.Fatal(err)
	}
	setHeaders(req, headers)
	return h.do(req)
}

// follow GETs pages until a non-redirect answer.
func (h *harness) follow(p page) page {
	h.t.Helper()
	for i := 0; i < 5 && p.status == http.StatusSeeOther; i++ {
		p = h.get(p.location())
	}
	return p
}

func setHeaders(req *http.Request, headers []string) {
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
}

var csrfRe = regexp.MustCompile(`name="_csrf" value="([0-9a-f]+)"`)

// token returns the CSRF token rendered into a page.
func (h *harness) token(path string) string {
	h.t.Helper()
	p := h.get(path)
	m := csrfRe.FindStringSubmatch(p.body)
	if m == nil {
		h.t.Fatalf("no csrf token on %s (status %d)", path, p.status)
	}
	return m[1]
}

// post submits a url-encoded form; the caller provides _csrf when needed.
func (h *harness) post(path string, form url.Values, headers ...string) page {
	h.t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, h.srv.URL+path, strings.NewReader(form.Encode()))
	if err != nil {
		h.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	setHeaders(req, headers)
	return h.do(req)
}

// submit posts a form with the CSRF token taken from tokenPage.
func (h *harness) submit(tokenPage, path string, form url.Values, headers ...string) page {
	h.t.Helper()
	if form == nil {
		form = url.Values{}
	}
	form.Set(csrfField, h.token(tokenPage))
	return h.post(path, form, headers...)
}

func (h *harness) login(email, password string) page {
	h.t.Helper()
	return h.submit("/login", "/login", url.Values{"email": {email}, "password": {password}})
}

func (h *harness) loginAdmin() {
	h.t.Helper()
	if p := h.login(testenv.AdminEmail, testenv.AdminPassword); p.status != http.StatusSeeOther {
		h.t.Fatalf("admin login: %d %s", p.status, p.body)
	}
}

func (h *harness) loginUser(email string) service.Actor {
	h.t.Helper()
	a := h.env.User(email)
	if p := h.login(email, testenv.UserPassword); p.status != http.StatusSeeOther {
		h.t.Fatalf("user login: %d", p.status)
	}
	return a
}

// upload sends an archive through the multipart deploy endpoint.
func (h *harness) upload(ref, name string, data []byte, asXHR bool) page {
	h.t.Helper()
	tok := h.token("/sites/" + ref)
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if !asXHR {
		_ = mw.WriteField(csrfField, tok)
	}
	fw, err := mw.CreateFormFile("archive", name)
	if err != nil {
		h.t.Fatal(err)
	}
	_, _ = fw.Write(data)
	_ = mw.Close()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, h.srv.URL+"/sites/"+ref+"/deploy", &buf)
	if err != nil {
		h.t.Fatal(err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if asXHR {
		req.Header.Set("Accept", "application/json")
		req.Header.Set(csrfHeader, tok)
	}
	return h.do(req)
}

func (h *harness) createSite(a service.Actor, title, slug string) service.SiteView {
	h.t.Helper()
	v, err := h.env.Svc.CreateSite(context.Background(), a, title, slug)
	if err != nil {
		h.t.Fatal(err)
	}
	return v
}

func (h *harness) deploy(a service.Actor, ref string, files map[string]string) domain.Deploy {
	h.t.Helper()
	res, err := h.env.Svc.Deploy(context.Background(), a, ref, bytes.NewReader(testenv.Zip(h.t, files)), domain.SourceAPI)
	if err != nil {
		h.t.Fatal(err)
	}
	h.env.Clock.Advance(2e9)
	return res.Deploy
}

func wantStatus(t *testing.T, p page, status int) {
	t.Helper()
	if p.status != status {
		t.Fatalf("status = %d, want %d; body:\n%.600s", p.status, status, p.body)
	}
}

func wantContains(t *testing.T, body string, subs ...string) {
	t.Helper()
	for _, s := range subs {
		if !strings.Contains(body, s) {
			t.Errorf("body does not contain %q", s)
		}
	}
}

func wantNotContains(t *testing.T, body string, subs ...string) {
	t.Helper()
	for _, s := range subs {
		if strings.Contains(body, s) {
			t.Errorf("body unexpectedly contains %q", s)
		}
	}
}
