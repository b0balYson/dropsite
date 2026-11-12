package ui

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

const (
	flashCookieName = "pp_flash"
	flashMaxAge     = time.Minute
	flashMaxBytes   = 2048
)

// Flash kinds.
const (
	flashOK       = "ok"
	flashError    = "error"
	flashDeployed = "deployed"
)

// flash is a one-shot message shown after a redirect. It lives in a short
// signed cookie, so nothing is stored on the server.
type flash struct {
	Kind string `json:"k"`
	Msg  string `json:"m"`
}

func (s *server) sign(payload string) string {
	m := hmac.New(sha256.New, s.flashKey)
	m.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func (s *server) setFlash(w http.ResponseWriter, r *http.Request, kind, msg string) {
	if len(msg) > flashMaxBytes {
		msg = msg[:flashMaxBytes]
	}
	raw, err := json.Marshal(flash{Kind: kind, Msg: msg})
	if err != nil {
		s.log.Error("encode flash", "err", err)
		return
	}
	payload := base64.RawURLEncoding.EncodeToString(raw)
	http.SetCookie(w, s.cookie(r, flashCookieName, payload+"."+s.sign(payload), flashMaxAge))
}

// takeFlash reads and clears the flash cookie; tampered values are ignored.
func (s *server) takeFlash(w http.ResponseWriter, r *http.Request) *flash {
	c, err := r.Cookie(flashCookieName)
	if err != nil {
		return nil
	}
	http.SetCookie(w, s.cookie(r, flashCookieName, "", -1))
	payload, sig, ok := strings.Cut(c.Value, ".")
	if !ok || !hmac.Equal([]byte(sig), []byte(s.sign(payload))) {
		return nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return nil
	}
	var f flash
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil
	}
	return &f
}
