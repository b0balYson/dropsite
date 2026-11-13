package api

import (
	"bytes"
	"crypto/sha256"
	_ "embed" // session.js is served from the binary
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/forms"
)

// Paths of the invisible visitor session of the secret proxy. Caddy
// proxies them with the rest of /_dropsite/* before the access check of
// protected sites, like the Altcha script.
const (
	sessionScriptPath    = "/_dropsite/session.js"
	sessionChallengePath = "/_dropsite/session/challenge"
	sessionPath          = "/_dropsite/session"
	maxSessionBody       = 16 << 10
)

//go:embed session.js
var sessionSource string

// SessionScript is session.js without indentation (it has no template
// literals, so leading whitespace never matters).
var SessionScript = func() []byte {
	var b strings.Builder
	for _, line := range strings.Split(sessionSource, "\n") {
		if l := strings.TrimSpace(line); l != "" {
			b.WriteString(l)
			b.WriteByte('\n')
		}
	}
	return []byte(b.String())
}()

var sessionETag = func() string {
	sum := sha256.Sum256(SessionScript)
	return `"` + hex.EncodeToString(sum[:8]) + `"`
}()

func (a *api) registerSessionPaths(mux *http.ServeMux) {
	mux.Handle("GET "+sessionScriptPath, a.wrap("GET "+sessionScriptPath, a.sessionScript))
	mux.Handle("GET "+sessionChallengePath, a.wrap("GET "+sessionChallengePath, a.sessionChallenge))
	mux.Handle("POST "+sessionPath, a.wrap("POST "+sessionPath, a.issueSession))
}

func (a *api) sessionScript(w http.ResponseWriter, r *http.Request) error {
	h := w.Header()
	h.Set("Content-Type", "text/javascript; charset=utf-8")
	h.Set("Cache-Control", "public, max-age=3600")
	h.Set("ETag", sessionETag)
	http.ServeContent(w, r, "session.js", time.Time{}, bytes.NewReader(SessionScript))
	return nil
}

func (a *api) sessionChallenge(w http.ResponseWriter, r *http.Request) error {
	purpose := r.URL.Query().Get("purpose")
	if purpose == "" {
		purpose = forms.PurposeSession
	}
	ch, err := a.svc.SessionChallenge(r.Context(), r.Host, infoFrom(r.Context()).ip, purpose)
	if err != nil {
		a.proxyError(w, r, err)
		return nil
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, ch)
	return nil
}

type sessionRequest struct {
	Payload string `json:"payload"`
}

func (a *api) issueSession(w http.ResponseWriter, r *http.Request) error {
	var req sessionRequest
	r.Body = http.MaxBytesReader(w, r.Body, maxSessionBody)
	if err := decodeJSON(w, r, &req); err != nil {
		a.proxyError(w, r, domain.E(domain.KindInvalid, domain.CodeProxySession, "send {\"payload\": \"<solution>\"}").
			WithCause(err))
		return nil
	}
	g, err := a.svc.IssueSession(r.Context(), r.Host, infoFrom(r.Context()).ip, req.Payload)
	if err != nil {
		a.proxyError(w, r, err)
		return nil
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, g)
	return nil
}
