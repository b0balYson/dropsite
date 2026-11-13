package updates

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeGitHub answers the release endpoint with body and status.
func fakeGitHub(t *testing.T, status int, body string) *Checker {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "application/vnd.github+json" || r.Header.Get("User-Agent") == "" {
			http.Error(w, "headers", http.StatusBadRequest)
			return
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	c, err := NewChecker(srv.URL+"/repos/b0balYson/dropsite/releases/latest", nil)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestLatest(t *testing.T) {
	cases := []struct {
		name, body string
		status     int
		want       string
		wantErr    bool
	}{
		{"stable", `{"tag_name":"v0.5.0","prerelease":false,"draft":false,"html_url":"javascript:alert(1)"}`, 200, "0.5.0", false},
		{"older answer is still the latest", `{"tag_name":"v0.3.1"}`, 200, "0.3.1", false},
		{"prerelease", `{"tag_name":"v0.6.0-rc.1","prerelease":true}`, 200, "", true},
		{"prerelease tag without flag", `{"tag_name":"v0.6.0-rc.1"}`, 200, "", true},
		{"draft", `{"tag_name":"v0.6.0","draft":true}`, 200, "", true},
		{"list picks newest stable", `[{"tag_name":"v0.7.0-rc.1","prerelease":true},{"tag_name":"v0.6.2"},{"tag_name":"v0.6.10"},{"tag_name":"v0.5.0"}]`, 200, "0.6.10", false},
		{"garbage tag", `{"tag_name":"v1.2.3; rm -rf /"}`, 200, "", true},
		{"leading zero", `{"tag_name":"v01.2.3"}`, 200, "", true},
		{"not json", `<html>`, 200, "", true},
		{"server error", `{}`, 500, "", true},
		{"not found", `{"message":"Not Found"}`, 404, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rel, err := fakeGitHub(t, tc.status, tc.body).Latest(context.Background())
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want error, got %v", rel)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if rel.Version.String() != tc.want || rel.URL != ReleasePageBase+tc.want {
				t.Fatalf("got %s %s", rel.Version, rel.URL)
			}
		})
	}
}

func TestLatestRefusesOversizedAnswer(t *testing.T) {
	body := `{"tag_name":"v0.5.0","body":"` + strings.Repeat("x", MaxResponseBytes) + `"}`
	_, err := fakeGitHub(t, 200, body).Latest(context.Background())
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("want ErrTooLarge, got %v", err)
	}
}

func TestLatestRefusesRedirects(t *testing.T) {
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"v9.9.9"}`))
	}))
	defer other.Close()
	srv := httptest.NewServer(http.RedirectHandler(other.URL, http.StatusFound))
	defer srv.Close()
	c, err := NewChecker(srv.URL+"/latest", nil)
	if err != nil {
		t.Fatal(err)
	}
	if rel, err := c.Latest(context.Background()); err == nil {
		t.Fatalf("redirect followed: %v", rel)
	}
}

type recordRT struct{ hosts []string }

func (r *recordRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.hosts = append(r.hosts, req.URL.Host)
	return &http.Response{StatusCode: 200, Body: http.NoBody, Header: http.Header{}, Request: req}, nil
}

func TestHostGuard(t *testing.T) {
	rt := &recordRT{}
	c, err := NewChecker("https://api.github.com/repos/b0balYson/dropsite/releases/latest", rt)
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range []string{"https://evil.example/x", "http://api.github.com/x", "https://user@api.github.com/x"} {
		req, _ := http.NewRequest(http.MethodGet, u, nil)
		resp, err := c.client.Transport.RoundTrip(req)
		if resp != nil {
			_ = resp.Body.Close()
		}
		if !errors.Is(err, ErrHost) {
			t.Errorf("%s: want ErrHost, got %v", u, err)
		}
	}
	if len(rt.hosts) != 0 {
		t.Fatalf("guarded requests reached the transport: %v", rt.hosts)
	}
	if _, err := NewChecker("ftp://x/y", nil); err == nil {
		t.Fatal("ftp endpoint accepted")
	}
}

func TestProductionTransportRefusesPrivateAddresses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"v1.0.0"}`))
	}))
	defer srv.Close()
	// The production transport (any endpoint but a loopback test server)
	// never connects to a loopback or private address.
	c, err := NewChecker(srv.URL, NewTransport("api.github.com"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Latest(context.Background()); err == nil {
		t.Fatal("private address reached")
	}
}

func TestStrictVersion(t *testing.T) {
	for _, v := range []string{"0.5.0", "1.2.3", "10.20.300"} {
		if !StrictVersion(v) {
			t.Errorf("%s refused", v)
		}
	}
	for _, v := range []string{"", "1.2", "1.2.3.4", "v1.2.3", "1.2.3-rc.1", "1.2.3+x", "01.2.3", "1.2.a", "1.2.3\n", "1234567.0.0", " 1.2.3", "1.2.3;id"} {
		if StrictVersion(v) {
			t.Errorf("%q accepted", v)
		}
	}
}
