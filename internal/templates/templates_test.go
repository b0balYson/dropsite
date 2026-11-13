package templates

import (
	"errors"
	"regexp"
	"strings"
	"testing"
)

// maxTemplateBytes is the size budget of one rendered template.
const maxTemplateBytes = 64 << 10

var (
	// externalRef finds resources the page would load from another server.
	// Plain <a href> links are allowed and not matched.
	externalRef = regexp.MustCompile(`(?i)(<(?:script|img|link|iframe|source|video|audio)[^>]+(?:src|href)=["']?(?:https?:)?//)|url\(\s*["']?(?:https?:)?//|@import`)
	h1          = regexp.MustCompile(`(?i)<h1[\s>]`)
)

func TestListHasFiveTemplatesInBothLanguages(t *testing.T) {
	want := []string{"cafe", "services", "event", "portfolio", "links"}
	for _, lang := range Languages {
		got := List(lang)
		if len(got) != len(want) {
			t.Fatalf("%s: %d templates, want %d", lang, len(got), len(want))
		}
		for i, info := range got {
			if info.Name != want[i] || info.Title == "" || info.Description == "" || info.SiteTitle == "" {
				t.Errorf("%s: template %d = %+v", lang, i, info)
			}
		}
	}
}

func TestRenderEveryTemplate(t *testing.T) {
	for _, info := range List("ru") {
		for _, lang := range Languages {
			t.Run(info.Name+"/"+lang, func(t *testing.T) {
				files, err := Render(info.Name, lang, "")
				if err != nil {
					t.Fatal(err)
				}
				byPath := map[string]string{}
				total := 0
				for _, f := range files {
					byPath[f.Path] = string(f.Data)
					total += len(f.Data)
				}
				if total > maxTemplateBytes {
					t.Errorf("template is %d bytes, budget %d", total, maxTemplateBytes)
				}
				for _, p := range []string{"index.html", "404.html", "favicon.svg", "base.css", "style.css"} {
					if _, ok := byPath[p]; !ok {
						t.Errorf("missing %s", p)
					}
				}
				for p, body := range byPath {
					if externalRef.MatchString(body) {
						t.Errorf("%s loads an external resource: %q", p, externalRef.FindString(body))
					}
					if !strings.HasSuffix(p, ".html") {
						continue
					}
					checkPage(t, p, body, lang)
				}
			})
		}
	}
}

func checkPage(t *testing.T, p, body, lang string) {
	t.Helper()
	for _, want := range []string{
		`<html lang="` + lang + `">`, `name="viewport"`, `<title>`, `name="description"`, `rel="icon"`, `id="main"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("%s: missing %s", p, want)
		}
	}
	if n := len(h1.FindAllString(body, -1)); n != 1 {
		t.Errorf("%s: %d <h1>, want 1", p, n)
	}
	if strings.Contains(body, "<no value>") || strings.Contains(body, "{{") {
		t.Errorf("%s: unrendered template text", p)
	}
}

// formNames is the form every template posts to (docs/forms.md).
var formNames = map[string]string{
	"cafe": "booking", "services": "lead", "event": "signup", "portfolio": "contact", "links": "booking",
}

var (
	formAction = regexp.MustCompile(`<form [^>]*action="/_form/([a-z0-9_-]+)" method="post"`)
	nextField  = regexp.MustCompile(`<input type="hidden" name="_next" value="([^"]*)">`)
	fieldID    = regexp.MustCompile(`<(?:input|select|textarea) id="([^"]+)" name="([^"_][^"]*)"`)
	firstBtn   = regexp.MustCompile(`<a class="btn" href="([^"]*)"`)
)

// Every template collects requests through the platform's forms: one form
// with the honeypot, the fill time, the Altcha widget and a same-site
// _next page that exists, every field labelled, and the main button
// leading to the form instead of mailto: or tel:.
func TestEveryTemplateHasAWorkingForm(t *testing.T) {
	for _, info := range List("ru") {
		for _, lang := range Languages {
			t.Run(info.Name+"/"+lang, func(t *testing.T) {
				files, err := Render(info.Name, lang, "")
				if err != nil {
					t.Fatal(err)
				}
				i := indexOf(files, "index.html")
				if i < 0 {
					t.Fatal("no index.html")
				}
				body := string(files[i].Data)
				checkForm(t, info.Name, body)
				m := nextField.FindStringSubmatch(body)
				if m == nil || !strings.HasPrefix(m[1], "/") || strings.HasPrefix(m[1], "//") {
					t.Fatalf("_next %v is not a same-site path", m)
				}
				thanks := indexOf(files, strings.TrimPrefix(m[1], "/"))
				if thanks < 0 {
					t.Fatalf("_next points to %s, which the template does not have", m[1])
				}
				page := string(files[thanks].Data)
				if !strings.Contains(page, `href="/`) || !strings.Contains(page, `name="robots" content="noindex"`) ||
					strings.Contains(page, "<form") {
					t.Errorf("thanks page: no link back, indexable or has a form")
				}
				if b := firstBtn.FindStringSubmatch(body); b == nil || !strings.HasPrefix(b[1], "#") {
					if info.Name != "links" {
						t.Errorf("main button %v does not lead to the page", b)
					}
				}
			})
		}
	}
}

func checkForm(t *testing.T, name, body string) {
	t.Helper()
	acts := formAction.FindAllStringSubmatch(body, -1)
	if len(acts) != 1 || acts[0][1] != formNames[name] {
		t.Fatalf("forms %v, want one /_form/%s", acts, formNames[name])
	}
	if s := sectionOf(body); s == "" || !strings.Contains(body, `href="#`+s+`"`) {
		t.Errorf("nothing links to the form section")
	}
	for _, want := range []string{
		`name="_hp" tabindex="-1" autocomplete="off"`, `class="hp" aria-hidden="true"`, `name="_t"`,
		`<altcha-widget challenge="/_form/challenge"></altcha-widget>`,
		`<script async defer src="/_dropsite/altcha.js" type="module"></script>`,
		`input[name="_t"]`, `Date.now()`, `<button class="btn" type="submit">`, " required",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("form: missing %s", want)
		}
	}
	if strings.Contains(body, `type="hidden" name="_hp"`) {
		t.Error("honeypot must not be type=hidden")
	}
	fields := fieldID.FindAllStringSubmatch(body, -1)
	if len(fields) < 3 {
		t.Fatalf("form has %d fields", len(fields))
	}
	for _, f := range fields {
		if !strings.Contains(body, `<label for="`+f[1]+`">`) {
			t.Errorf("field %s has no label", f[2])
		}
	}
}

// sectionOf returns the id of the section that holds the form.
func sectionOf(body string) string {
	i := strings.Index(body, "<form ")
	j := strings.LastIndex(body[:i], `<section class="`)
	if j < 0 {
		return ""
	}
	m := regexp.MustCompile(`id="([^"]+)"`).FindStringSubmatch(body[j:i])
	if m == nil {
		return ""
	}
	return m[1]
}

func TestRenderEscapesTitle(t *testing.T) {
	files, err := Render("cafe", "en", `<script>alert(1)</script> & Co`)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if f.Path != "index.html" {
			continue
		}
		body := string(f.Data)
		if strings.Contains(body, "<script>alert") {
			t.Fatal("title rendered as markup")
		}
		if !strings.Contains(body, "&lt;script&gt;alert(1)&lt;/script&gt; &amp; Co") {
			t.Fatal("escaped title missing")
		}
		return
	}
	t.Fatal("no index.html")
}

func TestRenderUsesTitleAndLanguage(t *testing.T) {
	files, err := Render("cafe", "ru", "Кофе у реки")
	if err != nil {
		t.Fatal(err)
	}
	if body := string(files[indexOf(files, "index.html")].Data); !strings.Contains(body, "Кофе у реки") || !strings.Contains(body, "Меню") {
		t.Fatal("title or Russian text missing")
	}
	if _, err := Render("shop", "ru", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown template: %v", err)
	}
	if Lang("de") != "ru" || Lang(" EN ") != "en" {
		t.Fatal("language fallback")
	}
}

func indexOf(files []File, p string) int {
	for i, f := range files {
		if f.Path == p {
			return i
		}
	}
	return -1
}
