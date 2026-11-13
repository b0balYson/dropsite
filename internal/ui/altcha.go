package ui

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"math"
	"net/http"
	"strconv"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/forms"
)

// Altcha proof of work on the sign-in and invitation forms (both
// listeners). The widget is the one site forms use (forms.AltchaWidget),
// served from the interface's own static files; it solves the challenge
// when the form is submitted (auto="onsubmit"), so nobody has to tick a box.
const (
	altchaChallengePath = "/login/challenge"
	altchaField         = "altcha"
	altchaAsset         = "altcha.js"
)

// addAltchaWidget serves the widget as a versioned static asset.
func addAltchaWidget(files map[string]staticFile, urls map[string]string) {
	sum := sha256.Sum256(forms.AltchaWidget)
	version := hex.EncodeToString(sum[:6])
	files[altchaAsset] = staticFile{body: forms.AltchaWidget, ctype: "text/javascript; charset=utf-8", etag: `"` + version + `"`}
	urls[altchaAsset] = "/static/" + altchaAsset + "?v=" + version
}

// altchaView is what the layout needs to load the widget.
type altchaView struct {
	Challenge string
	// Nonce allows the one <style> element the widget injects; the widget
	// reads it from <meta name="csp-nonce">.
	Nonce string
}

// altchaCSP is csp plus what the widget needs: its proof-of-work workers
// start from blob: URLs, and its injected style carries the nonce.
func altchaCSP(nonce string) string {
	return "default-src 'self'; script-src 'self'; style-src 'self' 'nonce-" + nonce + "'; img-src 'self' data:; " +
		"font-src 'self'; connect-src 'self'; worker-src blob:; frame-ancestors 'none'; form-action 'self'; base-uri 'none'"
}

// authPage renders a page carrying the Altcha widget (sign-in, invitation).
func (s *server) authPage(w http.ResponseWriter, r *http.Request, rc *reqCtx, status int, name, title string, data any) {
	b := make([]byte, 18)
	if _, err := rand.Read(b); err != nil {
		s.log.Error("generate style nonce", "err", err)
		http.Error(w, "service temporarily unavailable", http.StatusServiceUnavailable)
		return
	}
	nonce := base64.RawURLEncoding.EncodeToString(b)
	v := s.newView(r, rc, "", title, data)
	v.Altcha = &altchaView{Challenge: altchaChallengePath, Nonce: nonce}
	w.Header().Set("Content-Security-Policy", altchaCSP(nonce))
	w.Header().Set("Cache-Control", "no-store")
	s.layout(w, r, status, name, v)
}

// altchaChallenge issues a challenge bound to the request host.
func (s *server) altchaChallenge(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	ch, err := s.svc.AdminAltchaChallenge(r.Context(), r.Host, s.clientIP(r))
	if err != nil {
		if de := domain.AsError(err); de.Kind == domain.KindRateLimited {
			w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(de.RetryAfter.Seconds()))))
			http.Error(w, "too many requests", http.StatusTooManyRequests)
			return
		}
		s.log.Error("admin altcha challenge", "err", err)
		http.Error(w, "service temporarily unavailable", http.StatusServiceUnavailable)
		return
	}
	body, err := json.Marshal(ch)
	if err != nil {
		http.Error(w, "service temporarily unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write(body)
}
