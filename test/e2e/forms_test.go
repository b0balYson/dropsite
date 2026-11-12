//go:build e2e

package e2e

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// siteForms posts a form through Caddy and checks that the submission reaches
// the webhook receiver of compose.forms.yml with a valid signature, while a
// honeypot post is stored nowhere and GET on /_form stays 404.
func siteForms(t *testing.T, s *stack, owner *api, siteID, host string) {
	st, body := owner.json(http.MethodPost, "/sites/"+siteID+"/forms/channels",
		map[string]string{"kind": "webhook", "target": "http://webhook-sink:9000/hook"})
	must(t, st, http.StatusCreated, body, "create webhook channel")
	secret := str(body, "secret")
	if !strings.HasPrefix(secret, "whsec_") {
		t.Fatalf("channel %v", body)
	}

	marker := fmt.Sprintf("e2e-%d", time.Now().UnixNano())
	post := func(form url.Values, header ...string) (*http.Response, string) {
		req, _ := http.NewRequestWithContext(s.ctx, http.MethodPost, "https://"+host+":"+httpsPort+"/_form/lead",
			strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		for i := 0; i+1 < len(header); i += 2 {
			req.Header.Set(header[i], header[i+1])
		}
		resp, err := s.client.Do(req)
		if err != nil {
			t.Fatalf("post form: %v", err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp, string(b)
	}

	resp, out := post(url.Values{"name": {marker}, "phone": {"+7 900"}, "_hp": {""}}, "Accept", "application/json")
	if resp.StatusCode != http.StatusOK || !strings.Contains(out, `"ok":true`) {
		t.Fatalf("form post: %d %s", resp.StatusCode, out)
	}
	resp, _ = post(url.Values{"name": {"spam-" + marker}, "_hp": {"http://spam"}, "_next": {"/thanks.html"}},
		"Sec-Fetch-Mode", "navigate")
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/thanks.html" {
		t.Fatalf("honeypot post: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}

	if r, _, err := s.get(host, "/_form/lead"); err != nil || r.StatusCode != http.StatusNotFound {
		t.Fatalf("GET /_form/lead: %v %v", statusOf(r), err)
	}
	r, js, err := s.get(host, "/_dropsite/altcha.js")
	if err != nil || r.StatusCode != http.StatusOK || !strings.HasPrefix(r.Header.Get("Content-Type"), "text/javascript") ||
		!strings.Contains(js, "altcha-widget") {
		t.Fatalf("altcha widget: %v %v", statusOf(r), err)
	}
	if r, _, err := s.get(host, "/_dropsite/other.js"); err != nil || r.StatusCode != http.StatusNotFound {
		t.Fatalf("other /_dropsite path: %v %v", statusOf(r), err)
	}

	var delivered string
	s.eventually("submission at the webhook receiver", func() error {
		logs, err := compose(s.ctx, "logs", "--no-color", "--no-log-prefix", "webhook-sink").CombinedOutput()
		if err != nil {
			return err
		}
		for _, line := range strings.Split(string(logs), "\n") {
			if strings.Contains(line, marker) {
				delivered = line
				return nil
			}
		}
		return fmt.Errorf("not delivered yet")
	})
	if strings.Contains(delivered, "spam-") {
		t.Fatal("honeypot submission was delivered")
	}
	var got struct{ Sig, Body string }
	if err := json.Unmarshal([]byte(strings.TrimSpace(delivered)), &got); err != nil {
		t.Fatalf("receiver line %q: %v", delivered, err)
	}
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(got.Body))
	if got.Sig != "sha256="+hex.EncodeToString(m.Sum(nil)) {
		t.Fatalf("signature %q does not match the body", got.Sig)
	}

	st, body = owner.json(http.MethodGet, "/sites/"+siteID+"/forms/submissions", nil)
	must(t, st, http.StatusOK, body, "list submissions")
	raw, _ := json.Marshal(body)
	if strings.Contains(string(raw), "spam-") || !strings.Contains(string(raw), marker) || !strings.Contains(string(raw), `"status":"sent"`) {
		t.Fatalf("submissions %s", raw)
	}
}
