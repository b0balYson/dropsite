package ui

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Supported languages; the first is the fallback.
var languages = []string{"ru", "en"}

func supportedLang(l string) bool {
	for _, v := range languages {
		if v == l {
			return true
		}
	}
	return false
}

// catalogs maps a language to its locale.
type catalogs map[string]*locale

func loadCatalogs() (catalogs, error) {
	out := catalogs{}
	for _, lang := range languages {
		raw, err := assets.ReadFile("i18n/" + lang + ".json")
		if err != nil {
			return nil, fmt.Errorf("read catalog %s: %w", lang, err)
		}
		msgs := map[string]string{}
		if err := json.Unmarshal(raw, &msgs); err != nil {
			return nil, fmt.Errorf("parse catalog %s: %w", lang, err)
		}
		out[lang] = &locale{Lang: lang, msgs: msgs}
	}
	return out, nil
}

func (c catalogs) locale(lang string) *locale {
	if l, ok := c[lang]; ok {
		return l
	}
	return c[languages[0]]
}

// pickLang chooses the language: cookie, then the user's stored preference,
// then Accept-Language, then Russian.
func (s *server) pickLang(r *http.Request, rc *reqCtx) string {
	if c, err := r.Cookie(langCookieName); err == nil && supportedLang(c.Value) {
		return c.Value
	}
	if rc.authed && supportedLang(rc.actor.User.Lang) {
		return rc.actor.User.Lang
	}
	if l := acceptLanguage(r.Header.Get("Accept-Language")); l != "" {
		return l
	}
	return languages[0]
}

// acceptLanguage returns the supported language with the highest q-value.
func acceptLanguage(header string) string {
	type pref struct {
		lang string
		q    float64
		pos  int
	}
	var prefs []pref
	for i, part := range strings.Split(header, ",") {
		tag, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		base, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(tag)), "-")
		q := 1.0
		if v, ok := strings.CutPrefix(strings.TrimSpace(params), "q="); ok {
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				q = f
			}
		}
		if supportedLang(base) && q > 0 {
			prefs = append(prefs, pref{base, q, i})
		}
	}
	sort.SliceStable(prefs, func(i, j int) bool { return prefs[i].q > prefs[j].q })
	if len(prefs) == 0 {
		return ""
	}
	return prefs[0].lang
}

// locale translates keys and formats numbers and dates for one language.
type locale struct {
	Lang string
	msgs map[string]string
}

// T returns the message for key with {name} placeholders replaced by the
// name/value pairs in args. Unknown keys render as the key itself.
func (l *locale) T(key string, args ...any) string {
	msg, ok := l.msgs[key]
	if !ok {
		return key
	}
	if len(args) < 2 {
		return msg
	}
	pairs := make([]string, 0, len(args))
	for i := 0; i+1 < len(args); i += 2 {
		pairs = append(pairs, "{"+fmt.Sprint(args[i])+"}", fmt.Sprint(args[i+1]))
	}
	return strings.NewReplacer(pairs...).Replace(msg)
}

// Has reports whether the catalog defines key.
func (l *locale) Has(key string) bool {
	_, ok := l.msgs[key]
	return ok
}

const (
	kib = 1024
	mib = 1024 * kib
	gib = 1024 * mib
)

// Size formats a byte count with binary units and a localised decimal mark.
func (l *locale) Size(n int64) string {
	units := []struct {
		div float64
		key string
	}{{gib, "unit.gb"}, {mib, "unit.mb"}, {kib, "unit.kb"}}
	for _, u := range units {
		if float64(n) >= u.div {
			return l.decimal(float64(n)/u.div) + " " + l.T(u.key)
		}
	}
	return strconv.FormatInt(n, 10) + " " + l.T("unit.b")
}

// MB formats a byte count as megabytes for form fields (no unit).
func (l *locale) MB(n int64) string {
	return strings.ReplaceAll(l.decimal(float64(n)/mib), ",", ".")
}

func (l *locale) decimal(v float64) string {
	s := strconv.FormatFloat(math.Round(v*10)/10, 'f', -1, 64)
	if l.Lang == "ru" {
		s = strings.ReplaceAll(s, ".", ",")
	}
	return s
}

// Date formats a time in UTC with a localised month name.
func (l *locale) Date(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	t = t.UTC()
	month := l.T("month." + strconv.Itoa(int(t.Month())))
	return fmt.Sprintf("%d %s %d, %02d:%02d UTC", t.Day(), month, t.Year(), t.Hour(), t.Minute())
}

// DatePtr formats an optional time.
func (l *locale) DatePtr(t *time.Time) string {
	if t == nil {
		return "—"
	}
	return l.Date(*t)
}
