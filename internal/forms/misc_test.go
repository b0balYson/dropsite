package forms

import (
	"bytes"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
)

func TestSafeNext(t *testing.T) {
	for in, want := range map[string]string{
		"/thanks.html":                        "/thanks.html",
		"/":                                   "/",
		"/a?b=1#c":                            "/a?b=1#c",
		"":                                    "",
		"thanks":                              "",
		"//evil.example/":                     "",
		"/\\evil.example":                     "",
		"https://evil.example/":               "",
		"/a\\b":                               "",
		"/a\nLocation: x":                     "",
		"/a b":                                "",
		"javascript:alert(1)":                 "",
		"/" + strings.Repeat("a", maxNextLen): "",
	} {
		if got := SafeNext(in); got != want {
			t.Errorf("SafeNext(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWantsHTML(t *testing.T) {
	r := httptest.NewRequest("POST", "/_form/lead", nil)
	r.Header.Set("Sec-Fetch-Mode", "navigate")
	if !WantsHTML(r) {
		t.Fatal("navigation")
	}
	r.Header.Set("Sec-Fetch-Mode", "cors")
	r.Header.Set("Accept", "text/html")
	if WantsHTML(r) {
		t.Fatal("fetch with html accept")
	}
	r.Header.Del("Sec-Fetch-Mode")
	if !WantsHTML(r) {
		t.Fatal("old browser")
	}
	r.Header.Set("Accept", "application/json")
	if WantsHTML(r) {
		t.Fatal("json client")
	}
}

func TestSealer(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secrets.key")
	key, err := LoadOrCreateKey(path)
	if err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("key file mode %v %v", st.Mode(), err)
	}
	again, err := LoadOrCreateKey(path)
	if err != nil || !bytes.Equal(key, again) {
		t.Fatal("key not reused")
	}
	s, _ := NewSealer(key)
	sealed := s.Seal("bot-token", "setting:token")
	if strings.Contains(sealed, "bot-token") || !strings.HasPrefix(sealed, sealedPrefix) {
		t.Fatalf("sealed %q", sealed)
	}
	if v, err := s.Open(sealed, "setting:token"); err != nil || v != "bot-token" {
		t.Fatalf("open: %q %v", v, err)
	}
	if _, err := s.Open(sealed, "channel:1"); !errors.Is(err, ErrUnsealable) {
		t.Fatalf("other context: %v", err)
	}
	other, _ := NewSealer(bytes.Repeat([]byte{1}, KeySize))
	if _, err := other.Open(sealed, "setting:token"); !errors.Is(err, ErrUnsealable) {
		t.Fatalf("other key: %v", err)
	}
	if _, err := s.Open("plain", "x"); !errors.Is(err, ErrUnsealable) {
		t.Fatal("plain accepted")
	}
	if s.Derive("a") == s.Derive("b") || s.Derive("a") == other.Derive("a") {
		t.Fatal("derive")
	}
	if err := os.WriteFile(filepath.Join(dir, "short"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateKey(filepath.Join(dir, "short")); err == nil {
		t.Fatal("short key accepted")
	}
}

func TestCodes(t *testing.T) {
	c := DigitCode()
	if len(c) != DigitCodeLen || strings.Trim(c, digits) != "" {
		t.Fatalf("digit code %q", c)
	}
	if l := LinkCode(); len(l) != LinkCodeLen {
		t.Fatalf("link code %q", l)
	}
	h := HashCode(c)
	if !MatchCode(h, " "+c+" ") || MatchCode(h, "000000x") || MatchCode(nil, c) {
		t.Fatal("match")
	}
	if code, ok := StartCode("/start " + LinkCode()); !ok || len(code) != LinkCodeLen {
		t.Fatal("start code")
	}
	if _, ok := StartCode("/start"); ok {
		t.Fatal("bare start")
	}
	if _, ok := StartCode("hello " + LinkCode()); ok {
		t.Fatal("not a start command")
	}
}

func TestWriteCSVEscapesFormulas(t *testing.T) {
	var buf bytes.Buffer
	at := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	err := WriteCSV(&buf, []domain.FormSubmission{
		{Form: "lead", Host: "a.test", CreatedAt: at, Fields: []domain.FormField{{Name: "name", Value: "=1+1"}, {Name: "note", Value: "a,\"b\"\nc"}}},
		{Form: "call", Host: "a.test", CreatedAt: at, Fields: []domain.FormField{{Name: "phone", Value: "+7 900"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "created_at,form,host,name,note,phone\n" +
		"2026-11-12T10:00:00Z,lead,a.test,'=1+1,\"a,\"\"b\"\"\nc\",\n" +
		"2026-11-12T10:00:00Z,call,a.test,,,'+7 900\n"
	if buf.String() != want {
		t.Fatalf("csv:\n%s", buf.String())
	}
}

func TestMessageText(t *testing.T) {
	m := Message{ID: 7, SiteID: "abc", Host: "promo.test", Form: "lead", CreatedAt: time.Date(2026, 1, 2, 3, 4, 0, 0, time.UTC),
		Fields: []domain.FormField{{Name: "name", Value: "<b>Ann</b>"}, {Name: "msg", Value: "line1\nline2"}, {Name: "x", Value: ""}}, Lang: "en"}
	txt := m.Text()
	for _, s := range []string{"Site: promo.test", "Form: lead", "name: <b>Ann</b>", "msg:\nline1\nline2", "x: (empty)", "2026-01-02 03:04 UTC"} {
		if !strings.Contains(txt, s) {
			t.Errorf("text lacks %q:\n%s", s, txt)
		}
	}
	if strings.ContainsAny(m.Subject(), "\r\n") || !strings.Contains(m.Subject(), "lead") {
		t.Fatalf("subject %q", m.Subject())
	}
	if (Message{Lang: "de"}).texts() != catalog["ru"] {
		t.Fatal("fallback language")
	}
	if zh := (Message{Host: "promo.test", Form: "lead", Lang: "zh"}).Text(); !strings.Contains(zh, "网站: promo.test") {
		t.Fatalf("zh text:\n%s", zh)
	}
	b, err := m.WebhookJSON()
	if err != nil || !strings.Contains(string(b), `"id":"7"`) || !strings.Contains(string(b), `"name":"\u003cb\u003eAnn\u003c/b\u003e"`) {
		t.Fatalf("json %s %v", b, err)
	}
}
