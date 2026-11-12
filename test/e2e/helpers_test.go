//go:build e2e

package e2e

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
)

var (
	csrfRe   = regexp.MustCompile(`name="_csrf" value="([^"]+)"`)
	tokenRe  = regexp.MustCompile(`ds_[0-9A-Za-z]{43}`)
	inviteRe = regexp.MustCompile(`/invite/([A-Za-z0-9_-]{43})`)
)

const apiHost = "api." + baseDomain

// ui is a browser-like session against the web interface.
type ui struct {
	t *testing.T
	c *http.Client
}

func newUI(t *testing.T) *ui { return &ui{t: t, c: uiClient()} }

func (u *ui) csrf(path string) string {
	u.t.Helper()
	resp, err := u.c.Get(uiURL + path) //nolint:noctx // test
	if err != nil {
		u.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	m := csrfRe.FindSubmatch(b)
	if m == nil {
		u.t.Fatalf("no CSRF token on %s (status %d)", path, resp.StatusCode)
	}
	return string(m[1])
}

func (u *ui) post(path string, form url.Values) (int, string) {
	u.t.Helper()
	req, _ := http.NewRequest(http.MethodPost, uiURL+path, strings.NewReader(form.Encode())) //nolint:noctx // test
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", uiURL)
	resp, err := u.c.Do(req)
	if err != nil {
		u.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func (u *ui) login(email, password string) {
	u.t.Helper()
	tok := u.csrf("/login")
	status, body := u.post("/login", url.Values{"_csrf": {tok}, "email": {email}, "password": {password}, "next": {"/"}})
	if status != http.StatusSeeOther && status != http.StatusFound {
		u.t.Fatalf("login as %s: status %d\n%.300s", email, status, body)
	}
}

func (u *ui) issueToken(name string) string {
	u.t.Helper()
	tok := u.csrf("/connect")
	_, body := u.post("/connect/tokens", url.Values{"_csrf": {tok}, "name": {name}, "ttl": {"0"}})
	m := tokenRe.FindString(body)
	if m == "" {
		u.t.Fatalf("no token in response:\n%.500s", body)
	}
	return m
}

func (u *ui) acceptInvite(secret, password string) {
	u.t.Helper()
	tok := u.csrf("/invite/" + secret)
	status, body := u.post("/invite/"+secret, url.Values{"_csrf": {tok}, "password": {password}, "password2": {password}})
	if status >= 400 {
		u.t.Fatalf("accept invite: %d\n%.300s", status, body)
	}
}

// api calls the public API through Caddy.
type api struct {
	s     *stack
	token string
}

func (a *api) do(method, path string, body io.Reader, ctype string) (int, map[string]any) {
	a.s.t.Helper()
	req, _ := http.NewRequestWithContext(a.s.ctx, method, "https://"+apiHost+":"+httpsPort+"/api/v1"+path, body)
	if a.token != "" {
		req.Header.Set("Authorization", "Bearer "+a.token)
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	resp, err := a.s.client.Do(req)
	if err != nil {
		a.s.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func (a *api) json(method, path string, v any) (int, map[string]any) {
	var body io.Reader
	if v != nil {
		b, _ := json.Marshal(v)
		body = bytes.NewReader(b)
	}
	return a.do(method, path, body, "application/json")
}

func (a *api) deploy(ref string, archive []byte) (int, map[string]any) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	w, _ := mw.CreateFormFile("archive", "site.zip")
	_, _ = w.Write(archive)
	_ = mw.Close()
	return a.do(http.MethodPost, "/sites/"+ref+"/deploys", &buf, mw.FormDataContentType())
}

func must(t *testing.T, status, want int, body map[string]any, what string) {
	t.Helper()
	if status != want {
		t.Fatalf("%s: status %d, want %d: %v", what, status, want, body)
	}
}

func errCode(body map[string]any) string {
	e, _ := body["error"].(map[string]any)
	c, _ := e["code"].(string)
	return c
}

type zipEntry struct {
	name    string
	body    string
	symlink bool
	store   bool
}

func makeZip(t *testing.T, entries ...zipEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		h := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		if e.store {
			h.Method = zip.Store
		}
		if e.symlink {
			h.SetMode(os.ModeSymlink | 0o777)
		}
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(e.body))
	}
	_ = zw.Close()
	return buf.Bytes()
}

func site(files map[string]string) []zipEntry {
	out := make([]zipEntry, 0, len(files))
	for n, b := range files {
		out = append(out, zipEntry{name: n, body: b})
	}
	return out
}

func str(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}

func hostOf(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.Hostname()
}
