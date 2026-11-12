package tracking

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
)

func TestNormalize(t *testing.T) {
	good := []domain.Counters{
		{},
		{MetrikaID: " 12345678 "},
		{MetrikaID: "1000", GA4ID: "g-abc123xyz"},
		{GA4ID: "G-ABCDEFGHIJKLMNOP"},
	}
	for _, c := range good {
		if _, err := Normalize(c); err != nil {
			t.Errorf("Normalize(%+v): %v", c, err)
		}
	}
	if c, _ := Normalize(domain.Counters{GA4ID: " g-ab12cd "}); c.GA4ID != "G-AB12CD" {
		t.Errorf("GA4 not normalized: %q", c.GA4ID)
	}
	bad := []struct {
		c     domain.Counters
		field string
	}{
		{domain.Counters{MetrikaID: "0123"}, FieldMetrika},
		{domain.Counters{MetrikaID: "123"}, FieldMetrika},
		{domain.Counters{MetrikaID: "1234567890123"}, FieldMetrika},
		{domain.Counters{MetrikaID: "123);alert(1"}, FieldMetrika},
		{domain.Counters{GA4ID: "UA-1234-1"}, FieldGA4},
		{domain.Counters{GA4ID: `G-1"><script>alert(1)</script>`}, FieldGA4},
		{domain.Counters{GA4ID: "G-AB"}, FieldGA4},
		{domain.Counters{GA4ID: "G-ABCD\n"}, ""},
	}
	for _, b := range bad {
		_, err := Normalize(b.c)
		var de *domain.Error
		if !errors.As(err, &de) || de.Code != domain.CodeCounterInvalid {
			if b.field != "" {
				t.Errorf("Normalize(%+v) = %v, want counter_id_invalid", b.c, err)
			}
			continue
		}
		if b.field != "" && de.Params["field"] != b.field {
			t.Errorf("field = %v, want %s", de.Params["field"], b.field)
		}
	}
}

func TestMergeAndSnippet(t *testing.T) {
	s := Merge(domain.Counters{MetrikaID: "12345678", GA4ID: "G-AAAA1111"},
		domain.Counters{MetrikaID: "12345678", GA4ID: "G-BBBB2222"}, domain.Counters{})
	if len(s.Metrika) != 1 || len(s.GA4) != 2 {
		t.Fatalf("merge = %+v", s)
	}
	sn, err := s.Snippet()
	if err != nil {
		t.Fatal(err)
	}
	text := string(sn)
	for _, want := range []string{beginMarker, endMarker, "mc.yandex.ru/metrika/tag.js", `ym(12345678,"init"`,
		"googletagmanager.com/gtag/js?id=G-AAAA1111", `gtag("config","G-AAAA1111")`, `gtag("config","G-BBBB2222")`} {
		if !strings.Contains(text, want) {
			t.Errorf("snippet lacks %q:\n%s", want, text)
		}
	}
	if strings.Count(text, "gtag/js") != 1 {
		t.Error("one GA loader expected")
	}
	if sn, err := (Set{}).Snippet(); sn != nil || err != nil {
		t.Errorf("empty set: %q, %v", sn, err)
	}
	if _, err := (Set{GA4: []string{"<x>"}}).Snippet(); err == nil {
		t.Error("an unvalidated ID must never reach the snippet")
	}
}

const snip = beginMarker + "\nS\n" + endMarker + "\n"

func TestApply(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"head", "<!doctype html><html><head><title>x</title></head><body></body></html>",
			"<!doctype html><html><head><title>x</title>" + snip + "</head><body></body></html>"},
		{"upper case", "<HTML><HEAD></HEAD ><BODY>", "<HTML><HEAD>" + snip + "</HEAD ><BODY>"},
		{"two heads", "<head></head><svg><head></head></svg>", "<head>" + snip + "</head><svg><head></head></svg>"},
		{"no head", "<!doctype html><body class=a>hi</body>", "<!doctype html>" + snip + "<body class=a>hi</body>"},
		{"header is not head", "<body><header>x</header></body>", snip + "<body><header>x</header></body>"},
		{"fragment", "<p>hello</p>", "<p>hello</p>" + snip},
		{"utf-8", "<head><title>Привет 👋</title></head>", "<head><title>Привет 👋</title>" + snip + "</head>"},
		{"replace old block", "<head>" + beginMarker + "\nOLD\n" + endMarker + "\n</head>", "<head>" + snip + "</head>"},
	}
	for _, c := range cases {
		if got := string(Apply([]byte(c.in), []byte(snip))); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
	// Removing counters strips the block and nothing else.
	orig := "<head><meta charset=utf-8></head>"
	if got := string(Apply(Apply([]byte(orig), []byte(snip)), nil)); got != orig {
		t.Errorf("strip = %q", got)
	}
	// An unterminated marker is left alone instead of eating the page.
	broken := "<head>" + beginMarker + "</head><body>content</body>"
	if got := string(Apply([]byte(broken), nil)); got != broken {
		t.Errorf("broken marker = %q", got)
	}
}

func write(t *testing.T, dir, name, content string, mtime time.Time) string {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestInject(t *testing.T) {
	dir := t.TempDir()
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	index := write(t, dir, "index.html", "<html><head></head><body>Главная</body></html>", old)
	nested := write(t, dir, "docs/page.HTM", "<p>no head</p>", old)
	css := write(t, dir, "app.css", "</head>", old)
	js := write(t, dir, "x.js", "<head></head>", old)
	utf16 := write(t, dir, "u16.html", "\xff\xfe<\x00h\x00", old)
	set := Merge(domain.Counters{MetrikaID: "12345678"})
	res, err := Inject(dir, set, now)
	if err != nil {
		t.Fatal(err)
	}
	if res.Files != 2 || res.DeltaBytes <= 0 {
		t.Fatalf("result %+v", res)
	}
	for _, p := range []string{index, nested} {
		b, _ := os.ReadFile(p)
		if !bytes.Contains(b, []byte("ym(12345678")) {
			t.Errorf("%s lacks the snippet", p)
		}
		fi, _ := os.Stat(p)
		if !fi.ModTime().Equal(now) || fi.Mode().Perm() != 0o644 {
			t.Errorf("%s: mtime %s mode %v", p, fi.ModTime(), fi.Mode())
		}
	}
	if b, _ := os.ReadFile(index); !strings.Contains(string(b), "Главная") {
		t.Error("UTF-8 text damaged")
	}
	for _, p := range []string{css, js, utf16} {
		fi, _ := os.Stat(p)
		if !fi.ModTime().Equal(old) {
			t.Errorf("%s must not be touched", p)
		}
	}
	// Same set again: nothing changes, mtimes stay.
	later := now.Add(time.Hour)
	res, err = Inject(dir, set, later)
	if err != nil || res.Files != 0 {
		t.Fatalf("second inject %+v, %v", res, err)
	}
	// Clearing counters removes the block and restores the original bytes.
	res, err = Inject(dir, Set{}, later)
	if err != nil || res.Files != 2 || res.DeltaBytes >= 0 {
		t.Fatalf("clear %+v, %v", res, err)
	}
	if b, _ := os.ReadFile(index); string(b) != "<html><head></head><body>Главная</body></html>" {
		t.Errorf("after clear: %q", b)
	}
}

func TestInjectMissingDir(t *testing.T) {
	if _, err := Inject(filepath.Join(t.TempDir(), "none"), Set{}, time.Now()); err == nil {
		t.Error("missing directory must fail")
	}
}
