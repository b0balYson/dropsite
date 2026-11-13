package ui

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	flashCookieName = "ds_flash"
	flashMaxAge     = time.Minute
	flashMaxBytes   = 2048
)

// Flash kinds.
const (
	flashOK       = "ok"
	flashError    = "error"
	flashDeployed = "deployed"
)

// sectionField names the hidden form field that tells which part of the page
// a form lives in; its value is the id of an element on that page.
const sectionField = "_section"

// sectionRe is the shape of a section id: short, lower-case, no markup.
var sectionRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

// flash is a one-shot message shown after a redirect. It lives in a short
// signed cookie, so nothing is stored on the server. Section, when set, is the
// id of the page part whose form produced the message: the message is shown
// there, and as a toast when the page has no such part.
type flash struct {
	Kind    string `json:"k"`
	Msg     string `json:"m"`
	Section string `json:"s,omitempty"`
}

// IsError reports whether the message is an error.
func (f *flash) IsError() bool { return f.Kind == flashError }

// Sticky reports whether a toast with the message stays until closed:
// errors, and the deploy result that carries a link to the site.
func (f *flash) Sticky() bool { return f.Kind == flashError || f.Kind == flashDeployed }

// Tone is the notice tone of the message.
func (f *flash) Tone() string {
	if f.IsError() {
		return "error"
	}
	return "ok"
}

// Role is the ARIA role: errors interrupt, everything else is a status.
func (f *flash) Role() string {
	if f.IsError() {
		return "alert"
	}
	return "status"
}

// validSection returns s when it is a well-formed section id, "" otherwise.
func validSection(s string) string {
	if sectionRe.MatchString(s) {
		return s
	}
	return ""
}

// formSection reads the section of the submitted form. It parses only
// url-encoded bodies (or reads an already parsed form): an upload's body
// belongs to its handler.
func formSection(r *http.Request) string {
	if r.Method != http.MethodPost {
		return ""
	}
	if r.PostForm == nil {
		ct := r.Header.Get("Content-Type")
		if !strings.HasPrefix(ct, "application/x-www-form-urlencoded") {
			return ""
		}
		if err := r.ParseForm(); err != nil {
			return ""
		}
	}
	return validSection(r.PostForm.Get(sectionField))
}

// withSection points target at the section of the submitted form, replacing
// any fragment it had. When the form's page (the referrer) is not target, the
// result opens another page: target is kept and the message shows as a toast.
func withSection(r *http.Request, target string) string {
	sec := formSection(r)
	if sec == "" {
		return target
	}
	base, _, _ := strings.Cut(target, "#")
	if ref := r.Referer(); ref != "" {
		path, _, _ := strings.Cut(base, "?")
		u, err := url.Parse(ref)
		if err != nil || u.Path != path {
			return target
		}
	}
	return base + "#" + sec
}

func (s *server) sign(payload string) string {
	m := hmac.New(sha256.New, s.flashKey)
	m.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// setFlash keeps a message for the next page, in the section of the form
// that was sent (if it names one).
func (s *server) setFlash(w http.ResponseWriter, r *http.Request, kind, msg string) {
	s.setFlashIn(w, r, kind, msg, formSection(r))
}

// setFlashIn keeps a message for the given section of the next page; an
// invalid section id leaves the message without one (a toast).
func (s *server) setFlashIn(w http.ResponseWriter, r *http.Request, kind, msg, section string) {
	if len(msg) > flashMaxBytes {
		msg = msg[:flashMaxBytes]
	}
	raw, err := json.Marshal(flash{Kind: kind, Msg: msg, Section: validSection(section)})
	if err != nil {
		s.log.Error("encode flash", "err", err)
		return
	}
	payload := base64.RawURLEncoding.EncodeToString(raw)
	http.SetCookie(w, s.cookie(r, flashCookieName, payload+"."+s.sign(payload), flashMaxAge))
}

// takeFlash reads and clears the flash cookie; tampered values are ignored.
func (s *server) takeFlash(w http.ResponseWriter, r *http.Request) *flash {
	c, err := s.readCookie(r, flashCookieName)
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
	f.Section = validSection(f.Section)
	return &f
}
