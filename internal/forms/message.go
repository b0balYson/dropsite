package forms

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/b0balYson/dropsite/internal/domain"
)

// SendError is a delivery failure. Its message never contains credentials
// or full URLs and is shown to the site owner.
type SendError struct {
	Permanent bool
	Msg       string
}

func (e *SendError) Error() string { return e.Msg }

const maxErrorLen = 300

func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n]) + "…"
}

// Temporary returns a retryable SendError.
func Temporary(msg string) error { return &SendError{Msg: truncate(msg, maxErrorLen)} }

// Permanent returns a SendError that must not be retried.
func Permanent(msg string) error { return &SendError{Permanent: true, Msg: truncate(msg, maxErrorLen)} }

// IsPermanent reports whether err must not be retried.
func IsPermanent(err error) bool {
	var se *SendError
	return errors.As(err, &se) && se.Permanent
}

// Message is one submission prepared for delivery.
type Message struct {
	ID        int64
	SiteID    string
	Host      string
	Form      string
	CreatedAt time.Time
	Fields    []domain.FormField
	// Lang selects the language of human-readable texts ("ru" or "en").
	Lang string
}

type texts struct {
	subject, heading, site, form, time, empty string
}

var catalog = map[string]texts{
	"en": {"New submission", "New form submission", "Site", "Form", "Time", "(empty)"},
	"ru": {"Новая заявка", "Новая заявка с формы", "Сайт", "Форма", "Время", "(пусто)"},
}

func (m Message) texts() texts {
	if t, ok := catalog[m.Lang]; ok {
		return t
	}
	return catalog["ru"]
}

// Subject is the e-mail subject line. Host and form name are validated
// names, so it contains no line breaks.
func (m Message) Subject() string {
	return cleanText(m.texts().subject+": "+m.Form+" — "+m.Host, false)
}

// Text renders the submission as plain text for Telegram and e-mail.
func (m Message) Text() string {
	t := m.texts()
	var b strings.Builder
	b.WriteString(t.heading + "\n")
	b.WriteString(t.site + ": " + m.Host + "\n")
	b.WriteString(t.form + ": " + m.Form + "\n")
	b.WriteString(t.time + ": " + m.CreatedAt.UTC().Format("2006-01-02 15:04 UTC") + "\n\n")
	for _, f := range m.Fields {
		v := f.Value
		if strings.TrimSpace(v) == "" {
			v = t.empty
		}
		if strings.Contains(v, "\n") {
			b.WriteString(f.Name + ":\n" + v + "\n")
		} else {
			b.WriteString(f.Name + ": " + v + "\n")
		}
	}
	return b.String()
}

type webhookBody struct {
	ID        string             `json:"id"`
	SiteID    string             `json:"site_id"`
	Host      string             `json:"host"`
	Form      string             `json:"form"`
	CreatedAt string             `json:"created_at"`
	Fields    map[string]string  `json:"fields"`
	FieldList []domain.FormField `json:"field_list"`
}

// WebhookJSON is the body posted to webhooks.
func (m Message) WebhookJSON() ([]byte, error) {
	fields := make(map[string]string, len(m.Fields))
	for _, f := range m.Fields {
		fields[f.Name] = f.Value
	}
	list := m.Fields
	if list == nil {
		list = []domain.FormField{}
	}
	return json.Marshal(webhookBody{
		ID: strconv.FormatInt(m.ID, 10), SiteID: m.SiteID, Host: m.Host, Form: m.Form,
		CreatedAt: m.CreatedAt.UTC().Format(time.RFC3339), Fields: fields, FieldList: list,
	})
}
