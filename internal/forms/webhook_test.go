package forms

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

var loopback = []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}

func TestPublicAddr(t *testing.T) {
	for s, want := range map[string]bool{
		"8.8.8.8": true, "2606:4700:4700::1111": true,
		"127.0.0.1": false, "10.1.2.3": false, "172.16.0.1": false, "192.168.1.1": false,
		"169.254.169.254": false, "100.64.0.1": false, "0.0.0.0": false, "224.0.0.1": false,
		"198.18.0.1": false, "::1": false, "fe80::1": false, "fd00::1": false, "::ffff:127.0.0.1": false,
		"::ffff:10.0.0.1": false, "64:ff9b::a00:1": false, "2002:a00:1::": false, "255.255.255.255": false,
	} {
		if got := PublicAddr(netip.MustParseAddr(s), nil); got != want {
			t.Errorf("%s: %v", s, got)
		}
	}
	if !PublicAddr(netip.MustParseAddr("127.0.0.1"), loopback) {
		t.Fatal("allow list ignored")
	}
}

func TestValidateWebhookURL(t *testing.T) {
	ok := []string{"https://hooks.example.com/x?y=1", "https://8.8.8.8/hook"}
	bad := []string{"http://hooks.example.com/", "https://127.0.0.1/", "https://[::1]/", "https://169.254.169.254/latest",
		"https://user:pw@example.com/", "ftp://example.com", "https://", "hooks.example.com", "https://localhost/x",
		"https://a.localhost/", "https://example.com/" + strings.Repeat("a", maxWebhookURLLen)}
	for _, u := range ok {
		if _, err := ValidateWebhookURL(u, nil); err != nil {
			t.Errorf("%s: %v", u, err)
		}
	}
	for _, u := range bad {
		if _, err := ValidateWebhookURL(u, nil); err == nil {
			t.Errorf("%s accepted", u)
		}
	}
	if _, err := ValidateWebhookURL("http://sink:9000/hook", loopback); err != nil {
		t.Fatalf("http with allow list: %v", err)
	}
}

func TestWebhookSignatureAndDelivery(t *testing.T) {
	var gotSig, gotBody, gotUA string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotSig, gotBody, gotUA = r.Header.Get(SignatureHeader), string(b), r.UserAgent()
	}))
	defer srv.Close()
	pool := srv.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs
	c := NewWebhookClient(WebhookOptions{Allow: loopback, RootCAs: pool})
	body := []byte(`{"id":"1"}`)
	if err := c.Post(context.Background(), srv.URL+"/hook", "whsec_test", body); err != nil {
		t.Fatal(err)
	}
	m := hmac.New(sha256.New, []byte("whsec_test"))
	m.Write([]byte(gotBody))
	if gotSig != "sha256="+hex.EncodeToString(m.Sum(nil)) || gotBody != string(body) || gotUA != "dropsite-forms" {
		t.Fatalf("sig %q body %q ua %q", gotSig, gotBody, gotUA)
	}
}

func TestWebhookBlocksPrivateAddressesAtConnect(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	defer srv.Close()
	c := NewWebhookClient(WebhookOptions{})
	// "localhost" resolves to a loopback address: blocked after resolution.
	target := strings.Replace(srv.URL, "127.0.0.1", "localhost", 1)
	for _, u := range []string{srv.URL, target} {
		err := c.Post(context.Background(), u, "s", []byte("{}"))
		if err == nil || !IsPermanent(err) || !strings.Contains(err.Error(), "not allowed") {
			t.Fatalf("%s: %v", u, err)
		}
	}
	plain := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	defer plain.Close()
	if err := c.Post(context.Background(), plain.URL, "s", []byte("{}")); err == nil {
		t.Fatal("plain http to a non-allowed address")
	}
	if hits.Load() != 0 {
		t.Fatalf("blocked server was reached %d times", hits.Load())
	}
}

func TestWebhookDoesNotFollowRedirects(t *testing.T) {
	var internal atomic.Int32
	inner := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { internal.Add(1) }))
	defer inner.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, inner.URL, http.StatusFound)
	}))
	defer srv.Close()
	c := NewWebhookClient(WebhookOptions{Allow: loopback})
	err := c.Post(context.Background(), srv.URL, "s", []byte("{}"))
	if err == nil || !IsPermanent(err) || internal.Load() != 0 {
		t.Fatalf("redirect: %v, inner hits %d", err, internal.Load())
	}
}

func TestWebhookStatusClassesAndTimeout(t *testing.T) {
	status := atomic.Int32{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if status.Load() == 0 {
			time.Sleep(300 * time.Millisecond)
			return
		}
		w.WriteHeader(int(status.Load()))
	}))
	defer srv.Close()
	c := NewWebhookClient(WebhookOptions{Allow: loopback, Timeout: 100 * time.Millisecond})
	if err := c.Post(context.Background(), srv.URL+"/secret-token", "s", nil); err == nil || IsPermanent(err) || strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("timeout: %v", err)
	}
	for code, permanent := range map[int]bool{503: false, 429: false, 408: false, 404: true, 400: true} {
		status.Store(int32(code))
		err := c.Post(context.Background(), srv.URL, "s", nil)
		if err == nil || IsPermanent(err) != permanent {
			t.Errorf("%d: %v", code, err)
		}
	}
}
