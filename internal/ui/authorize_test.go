package ui

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/b0balYson/dropsite/internal/oauth"
	"github.com/b0balYson/dropsite/internal/service"
	"github.com/b0balYson/dropsite/internal/testenv"
)

var sealedRe = regexp.MustCompile(`name="request" value="([^"]+)"`)

type consentRig struct {
	t      *testing.T
	env    *testenv.Env
	srv    *httptest.Server
	c      *http.Client
	client string
}

func newConsentRig(t *testing.T) *consentRig {
	t.Helper()
	env := testenv.New(t)
	az := oauth.New(env.Svc, oauth.Options{})
	srv := httptest.NewTLSServer(NewAuthorize(env.Svc, az, func(*http.Request) string { return "203.0.113.7" }, nil))
	t.Cleanup(srv.Close)
	c := srv.Client()
	c.Jar, _ = cookiejar.New(nil)
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	cl, err := env.Svc.RegisterOAuthClient(context.Background(), "Local tool", []string{"http://127.0.0.1:7777/cb"}, "1.1.1.1")
	if err != nil {
		t.Fatal(err)
	}
	return &consentRig{t: t, env: env, srv: srv, c: c, client: cl.ID}
}

func (r *consentRig) do(req *http.Request) (*http.Response, string) {
	r.t.Helper()
	resp, err := r.c.Do(req)
	if err != nil {
		r.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

func (r *consentRig) open(lang string) (string, string, string) {
	r.t.Helper()
	q := url.Values{"response_type": {"code"}, "client_id": {r.client}, "code_challenge_method": {"S256"},
		"code_challenge": {service.PKCEChallenge(strings.Repeat("v", 43))}, "ui_lang": {lang}}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, r.srv.URL+"/oauth/authorize?"+q.Encode(), nil)
	resp, body := r.do(req)
	if resp.StatusCode != http.StatusOK {
		r.t.Fatalf("open: %d %s", resp.StatusCode, body)
	}
	return body, csrfRe.FindStringSubmatch(body)[1], strings.ReplaceAll(sealedRe.FindStringSubmatch(body)[1], "&#43;", "+")
}

func (r *consentRig) post(form url.Values) (*http.Response, string) {
	r.t.Helper()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, r.srv.URL+"/oauth/authorize?ui_lang=en", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return r.do(req)
}

func TestConsentPage(t *testing.T) {
	r := newConsentRig(t)
	body, csrf, sealed := r.open("en")
	wantContains(t, body, "Local tool", "7777", "runs on this computer", "ui_lang=ru", "/oauth/static/app.css")

	// A wrong code keeps the user on the page with an explanation.
	resp, page := r.post(url.Values{"_csrf": {csrf}, "request": {sealed}, "code": {"AAAA-AAAA-AAAAA"}})
	if resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(page, "connect code is wrong") {
		t.Fatalf("wrong code: %d %.300s", resp.StatusCode, page)
	}
	// The limit of failures turns into 429.
	for range service.FailedCodesPerIP {
		resp, _ = r.post(url.Values{"_csrf": {csrf}, "request": {sealed}, "code": {"AAAA-AAAA-AAAAA"}})
	}
	if resp.StatusCode != http.StatusTooManyRequests || resp.Header.Get("Retry-After") == "" {
		t.Fatalf("rate limit: %d", resp.StatusCode)
	}
	// A tampered request is refused.
	resp, _ = r.post(url.Values{"_csrf": {csrf}, "request": {sealed + "x"}})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("tampered: %d", resp.StatusCode)
	}
	// Cross-site posts are refused.
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, r.srv.URL+"/oauth/authorize",
		strings.NewReader(url.Values{"_csrf": {csrf}, "request": {sealed}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://evil.example")
	if resp, _ := r.do(req); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-site: %d", resp.StatusCode)
	}
}

func TestConsentApproveAndBadResource(t *testing.T) {
	r := newConsentRig(t)
	u := r.env.User("u@pp.test")
	_, csrf, sealed := r.open("ru")
	c, _ := r.env.Svc.CreateConnectCode(context.Background(), u)
	resp, _ := r.post(url.Values{"_csrf": {csrf}, "request": {sealed}, "code": {strings.ToLower(c.Code)}, "action": {"allow"}})
	loc, _ := url.Parse(resp.Header.Get("Location"))
	if resp.StatusCode != http.StatusSeeOther || loc.Host != "127.0.0.1:7777" || loc.Query().Get("code") == "" {
		t.Fatalf("approve: %d %s", resp.StatusCode, loc)
	}
	if !strings.Contains(resp.Header.Get("Content-Security-Policy"), "form-action 'self' http://127.0.0.1:7777") {
		t.Fatalf("csp: %s", resp.Header.Get("Content-Security-Policy"))
	}
	if consentCSP("::bad") != csp {
		t.Fatal("bad redirect CSP")
	}
	// Errors that can be reported to the client are redirected.
	q := url.Values{"response_type": {"token"}, "client_id": {r.client}}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, r.srv.URL+"/oauth/authorize?"+q.Encode(), nil)
	resp, _ = r.do(req)
	if resp.StatusCode != http.StatusFound || !strings.Contains(resp.Header.Get("Location"), "unsupported_response_type") {
		t.Fatalf("redirect error: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
}
