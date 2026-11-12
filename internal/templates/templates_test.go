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
