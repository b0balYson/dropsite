// Package forms holds the building blocks of site form intake and delivery
// that do not depend on the database: request parsing and anti-spam fields,
// the proof-of-work check, delivery senders (Telegram, SMTP, webhook), SSRF
// protection, secret sealing, confirmation codes and CSV export.
package forms

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/b0balYson/dropsite/internal/domain"
)

// Intake limits.
const (
	MaxBodyBytes = 64 << 10
	MaxFields    = 50
	MaxNameLen   = 64
	MaxValueLen  = 10000
	// MinFillTime is the shortest plausible time between rendering a form
	// (the _t field) and submitting it.
	MinFillTime = 2 * time.Second
	// maxParts bounds the work spent on bodies with many tiny parts.
	maxParts = 4 * MaxFields
)

// Reserved field names. Every field whose name starts with "_" is dropped;
// AltchaField is dropped too because it is the widget's payload.
const (
	HoneypotField = "_hp"
	TimeField     = "_t"
	NextField     = "_next"
	AltchaField   = "altcha"
)

var formNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// NormalizeFormName lower-cases a form name and reports whether it is valid.
func NormalizeFormName(name string) (string, bool) {
	n := strings.ToLower(name)
	return n, formNameRe.MatchString(n)
}

// Parsed is a decoded form post.
type Parsed struct {
	Fields     []domain.FormField
	Honeypot   bool
	RenderedAt *time.Time
	Next       string
	Altcha     string
}

var (
	errTooLarge    = domain.E(domain.KindTooLarge, domain.CodeFormTooLarge, "form data is larger than 64 KB")
	errUnsupported = domain.E(domain.KindInvalid, domain.CodeFormUnsupported,
		"send the form as application/x-www-form-urlencoded, multipart/form-data or application/json")
	errEmpty = domain.E(domain.KindInvalid, domain.CodeFormEmpty, "the form has no fields")
)

func invalid(msg string) error {
	return domain.E(domain.KindInvalid, domain.CodeFormInvalid, msg)
}

// limited fails with errTooLarge once more than MaxBodyBytes are read.
type limited struct {
	r io.Reader
	n int64
}

func (l *limited) Read(p []byte) (int, error) {
	n, err := l.r.Read(p)
	l.n += int64(n)
	if l.n > MaxBodyBytes {
		return n, errTooLarge
	}
	return n, err
}

// Parse decodes a form body of the given Content-Type. The body may already
// be wrapped in http.MaxBytesReader; Parse enforces MaxBodyBytes itself too.
func Parse(contentType string, body io.Reader) (Parsed, error) {
	mt, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return Parsed{}, errUnsupported
	}
	lr := &limited{r: body}
	var raw []domain.FormField
	switch mt {
	case "application/x-www-form-urlencoded":
		raw, err = parseURLEncoded(lr)
	case "multipart/form-data":
		raw, err = parseMultipart(lr, params["boundary"])
	case "application/json":
		raw, err = parseJSON(lr)
	default:
		return Parsed{}, errUnsupported
	}
	if err != nil {
		return Parsed{}, mapReadError(err)
	}
	return collect(raw)
}

func mapReadError(err error) error {
	var de *domain.Error
	if errors.As(err, &de) {
		return de
	}
	var mbe *http.MaxBytesError
	if errors.As(err, &mbe) {
		return errTooLarge
	}
	return invalid("the form data could not be read")
}

// parseURLEncoded keeps the order of fields, unlike url.ParseQuery.
func parseURLEncoded(r io.Reader) ([]domain.FormField, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	var out []domain.FormField
	for pair := range strings.SplitSeq(string(b), "&") {
		if pair == "" {
			continue
		}
		if len(out) >= maxParts {
			return nil, invalid("the form has too many fields")
		}
		k, v, _ := strings.Cut(pair, "=")
		name, err1 := url.QueryUnescape(k)
		value, err2 := url.QueryUnescape(v)
		if err1 != nil || err2 != nil {
			return nil, invalid("the form data is malformed")
		}
		out = append(out, domain.FormField{Name: name, Value: value})
	}
	return out, nil
}

// parseMultipart reads text parts and discards file parts (they still count
// towards the body limit).
func parseMultipart(r io.Reader, boundary string) ([]domain.FormField, error) {
	if boundary == "" {
		return nil, invalid("the form data is malformed")
	}
	mr := multipart.NewReader(r, boundary)
	var out []domain.FormField
	for parts := 0; ; parts++ {
		p, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		if parts >= maxParts {
			return nil, invalid("the form has too many fields")
		}
		if p.FileName() != "" {
			if _, err := io.Copy(io.Discard, p); err != nil {
				return nil, err
			}
			continue
		}
		b, err := io.ReadAll(p)
		if err != nil {
			return nil, err
		}
		out = append(out, domain.FormField{Name: p.FormName(), Value: string(b)})
	}
}

// parseJSON accepts one object of scalars or arrays of scalars.
func parseJSON(r io.Reader) ([]domain.FormField, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil || tok != json.Delim('{') {
		return nil, invalid("the JSON body must be an object")
	}
	var out []domain.FormField
	for dec.More() {
		if len(out) >= maxParts {
			return nil, invalid("the form has too many fields")
		}
		kt, err := dec.Token()
		if err != nil {
			return nil, invalid("the JSON body is malformed")
		}
		name, _ := kt.(string)
		var v any
		if err := dec.Decode(&v); err != nil {
			return nil, invalid("the JSON body is malformed")
		}
		value, ok := jsonScalar(v)
		if !ok {
			return nil, invalid("JSON field values must be strings, numbers, booleans or lists of them")
		}
		out = append(out, domain.FormField{Name: name, Value: value})
	}
	if _, err := dec.Token(); err != nil {
		return nil, invalid("the JSON body is malformed")
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, invalid("the JSON body is malformed")
	}
	return out, nil
}

func jsonScalar(v any) (string, bool) {
	switch x := v.(type) {
	case nil:
		return "", true
	case string:
		return x, true
	case json.Number:
		return x.String(), true
	case bool:
		return strconv.FormatBool(x), true
	case []any:
		parts := make([]string, 0, len(x))
		for _, e := range x {
			s, ok := jsonScalar(e)
			if !ok {
				return "", false
			}
			if _, isList := e.([]any); isList {
				return "", false
			}
			parts = append(parts, s)
		}
		return strings.Join(parts, ", "), true
	}
	return "", false
}

// collect separates service fields, cleans names and values and merges
// repeated names in first-seen order.
func collect(raw []domain.FormField) (Parsed, error) {
	var p Parsed
	index := map[string]int{}
	for _, f := range raw {
		name := strings.TrimSpace(cleanText(f.Name, false))
		value := cleanText(f.Value, true)
		switch {
		case name == HoneypotField:
			if strings.TrimSpace(value) != "" {
				p.Honeypot = true
			}
			continue
		case name == TimeField:
			p.RenderedAt = parseRenderTime(value)
			continue
		case name == NextField:
			p.Next = strings.TrimSpace(f.Value)
			continue
		case name == AltchaField:
			p.Altcha = strings.TrimSpace(f.Value)
			continue
		case name == "" || strings.HasPrefix(name, "_"):
			continue
		}
		if utf8.RuneCountInString(name) > MaxNameLen {
			return Parsed{}, invalid("a field name is longer than 64 characters")
		}
		if i, ok := index[name]; ok {
			if value != "" {
				if p.Fields[i].Value != "" {
					value = p.Fields[i].Value + ", " + value
				}
				p.Fields[i].Value = value
			}
		} else {
			if len(p.Fields) >= MaxFields {
				return Parsed{}, invalid("the form has more than 50 fields")
			}
			index[name] = len(p.Fields)
			p.Fields = append(p.Fields, domain.FormField{Name: name, Value: value})
		}
		if utf8.RuneCountInString(p.Fields[index[name]].Value) > MaxValueLen {
			return Parsed{}, invalid("a field value is longer than 10000 characters")
		}
	}
	if !p.Honeypot && !hasContent(p.Fields) {
		return Parsed{}, errEmpty
	}
	return p, nil
}

func hasContent(fields []domain.FormField) bool {
	for _, f := range fields {
		if strings.TrimSpace(f.Value) != "" {
			return true
		}
	}
	return false
}

// cleanText replaces invalid UTF-8, normalises line breaks and drops control
// characters (keeping newlines and tabs in values).
func cleanText(s string, multiline bool) string {
	s = strings.ToValidUTF8(s, "�")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\t':
			if multiline {
				return r
			}
			return ' '
		case r == '\r':
			if multiline {
				return '\n'
			}
			return ' '
		case unicode.IsControl(r) || r == ' ' || r == ' ':
			return -1
		}
		return r
	}, s)
}

// parseRenderTime accepts Unix seconds or milliseconds.
func parseRenderTime(v string) *time.Time {
	n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
	if err != nil || n <= 0 {
		return nil
	}
	var t time.Time
	if n > 1e12 {
		t = time.UnixMilli(n)
	} else {
		t = time.Unix(n, 0)
	}
	t = t.UTC()
	return &t
}

// TooFast reports whether a form rendered at renderedAt and posted at now was
// filled in implausibly fast. A render time slightly in the future counts as
// too fast; one more than a minute ahead is treated as clock skew and ignored.
func TooFast(renderedAt *time.Time, now time.Time) bool {
	if renderedAt == nil {
		return false
	}
	d := now.Sub(*renderedAt)
	return d >= -time.Minute && d < MinFillTime
}
