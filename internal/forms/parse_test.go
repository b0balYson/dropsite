package forms

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
)

const urlencoded = "application/x-www-form-urlencoded"

func wantFormCode(t *testing.T, err error, code string) {
	t.Helper()
	var de *domain.Error
	if !errors.As(err, &de) || de.Code != code {
		t.Fatalf("want %s, got %v", code, err)
	}
}

func TestParseURLEncodedKeepsOrderAndMergesRepeats(t *testing.T) {
	p, err := Parse(urlencoded, strings.NewReader("name=Ann&phone=%2B7+900&topic=a&topic=b&_csrf=x&_next=%2Fthanks&_t=1700000000&_hp="))
	if err != nil {
		t.Fatal(err)
	}
	want := []domain.FormField{{Name: "name", Value: "Ann"}, {Name: "phone", Value: "+7 900"}, {Name: "topic", Value: "a, b"}}
	if len(p.Fields) != len(want) {
		t.Fatalf("fields %v", p.Fields)
	}
	for i, f := range want {
		if p.Fields[i] != f {
			t.Fatalf("field %d: %v, want %v", i, p.Fields[i], f)
		}
	}
	if p.Next != "/thanks" || p.Honeypot || p.RenderedAt == nil || p.RenderedAt.Unix() != 1700000000 {
		t.Fatalf("service fields: %+v", p)
	}
}

func TestParseHoneypot(t *testing.T) {
	p, err := Parse(urlencoded, strings.NewReader("name=x&_hp=http%3A%2F%2Fspam"))
	if err != nil || !p.Honeypot {
		t.Fatalf("%+v %v", p, err)
	}
	// A filled honeypot with no other fields is still a (silent) spam hit.
	p, err = Parse(urlencoded, strings.NewReader("_hp=1"))
	if err != nil || !p.Honeypot {
		t.Fatalf("%+v %v", p, err)
	}
}

func TestParseAltchaFieldIsNotStored(t *testing.T) {
	p, err := Parse(urlencoded, strings.NewReader("name=x&altcha=abc"))
	if err != nil || p.Altcha != "abc" || len(p.Fields) != 1 {
		t.Fatalf("%+v %v", p, err)
	}
}

func TestParseMultipartDropsFiles(t *testing.T) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("name", "Ann")
	fw, _ := mw.CreateFormFile("cv", "cv.pdf")
	_, _ = fw.Write([]byte("%PDF binary"))
	_ = mw.WriteField("_hp", "")
	_ = mw.Close()
	p, err := Parse(mw.FormDataContentType(), &buf)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Fields) != 1 || p.Fields[0].Name != "name" {
		t.Fatalf("fields %v", p.Fields)
	}
}

func TestParseJSON(t *testing.T) {
	p, err := Parse("application/json; charset=utf-8", strings.NewReader(`{"name":"Ann","age":30,"agree":true,"tags":["a","b"],"none":null,"_hp":""}`))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, f := range p.Fields {
		got[f.Name] = f.Value
	}
	if got["name"] != "Ann" || got["age"] != "30" || got["agree"] != "true" || got["tags"] != "a, b" || len(p.Fields) != 5 {
		t.Fatalf("fields %v", p.Fields)
	}
	for _, body := range []string{`[1]`, `{"a":{"b":1}}`, `{"a":[[1]]}`, `{"a":1`, `{"a":1}{}`} {
		if _, err := Parse("application/json", strings.NewReader(body)); err == nil {
			t.Fatalf("%s accepted", body)
		}
	}
}

func TestParseLimits(t *testing.T) {
	big := "a=" + strings.Repeat("x", MaxBodyBytes)
	_, err := Parse(urlencoded, strings.NewReader(big))
	wantFormCode(t, err, domain.CodeFormTooLarge)

	rec := httptest.NewRecorder()
	_, err = Parse(urlencoded, http.MaxBytesReader(rec, io.NopCloser(strings.NewReader("a=12345")), 3))
	wantFormCode(t, err, domain.CodeFormTooLarge)

	var many []string
	for i := range MaxFields + 1 {
		many = append(many, fmt.Sprintf("f%d=1", i))
	}
	_, err = Parse(urlencoded, strings.NewReader(strings.Join(many, "&")))
	wantFormCode(t, err, domain.CodeFormInvalid)

	_, err = Parse(urlencoded, strings.NewReader(strings.Repeat("n", MaxNameLen+1)+"=1"))
	wantFormCode(t, err, domain.CodeFormInvalid)

	_, err = Parse(urlencoded, strings.NewReader("a="+strings.Repeat("x", MaxValueLen+1)))
	wantFormCode(t, err, domain.CodeFormInvalid)

	_, err = Parse(urlencoded, strings.NewReader("_next=/x&_csrf=1"))
	wantFormCode(t, err, domain.CodeFormEmpty)

	_, err = Parse("text/plain", strings.NewReader("a=1"))
	wantFormCode(t, err, domain.CodeFormUnsupported)

	_, err = Parse(urlencoded, strings.NewReader("a=%zz"))
	wantFormCode(t, err, domain.CodeFormInvalid)
}

func TestParseCleansControlCharacters(t *testing.T) {
	p, err := Parse(urlencoded, strings.NewReader("na%0Ame=a%0D%0Ab%00c%E2%80%A8&bad=%FF"))
	if err != nil {
		t.Fatal(err)
	}
	if p.Fields[0].Name != "na me" || p.Fields[0].Value != "a\nbc" || p.Fields[1].Value != "�" {
		t.Fatalf("fields %q", p.Fields)
	}
}

func TestTooFast(t *testing.T) {
	now := time.Unix(1_700_000_100, 0)
	at := func(d time.Duration) *time.Time { v := now.Add(d); return &v }
	cases := []struct {
		at   *time.Time
		want bool
	}{
		{nil, false},
		{at(-time.Second), true},
		{at(-3 * time.Second), false},
		{at(10 * time.Second), true},
		{at(time.Hour), false},
	}
	for i, c := range cases {
		if got := TooFast(c.at, now); got != c.want {
			t.Errorf("case %d: %v", i, got)
		}
	}
	if p := parseRenderTime("1700000000123"); p == nil || p.Unix() != 1700000000 {
		t.Fatalf("millis: %v", p)
	}
	if parseRenderTime("soon") != nil || parseRenderTime("-5") != nil {
		t.Fatal("garbage time accepted")
	}
}

func TestNormalizeFormName(t *testing.T) {
	for name, ok := range map[string]bool{"lead": true, "Lead": true, "a-b_c9": true, "": false, "..": false, "a/b": false, "-x": false, strings.Repeat("a", 65): false} {
		if _, got := NormalizeFormName(name); got != ok {
			t.Errorf("%q: %v", name, got)
		}
	}
}
