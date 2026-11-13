package ui

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestValidSection(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"access-h", "access-h"},
		{"domains-h", "domains-h"},
		{"proxy-openai-2", "proxy-openai-2"},
		{"site-0a1b2c3d4e", "site-0a1b2c3d4e"},
		{"x", "x"},
		{"a" + strings.Repeat("b", 63), "a" + strings.Repeat("b", 63)},
		// rejected
		{"", ""},
		{"a" + strings.Repeat("b", 64), ""},
		{"Access-h", ""},
		{"1access", ""},
		{"-access", ""},
		{"#access-h", ""},
		{"access h", ""},
		{"access_h", ""},
		{"access.h", ""},
		{"a/b", ""},
		{`a"><script>`, ""},
		{"javascript:alert(1)", ""},
		{"доступ", ""},
		{"access-h\n", ""},
	}
	for _, c := range cases {
		if got := validSection(c.in); got != c.want {
			t.Errorf("validSection(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestWithSection(t *testing.T) {
	cases := []struct {
		name, section, referer, target, want string
	}{
		{"same page", "domains-h", "http://ui.test/sites/a/access", "/sites/a/access", "/sites/a/access#domains-h"},
		{"replaces fragment", "danger-h", "http://ui.test/sites/a/settings#x", "/sites/a/settings#delete-h", "/sites/a/settings#danger-h"},
		{"keeps query", "site-a", "http://ui.test/admin/sites?q=x", "/admin/sites?q=x", "/admin/sites?q=x#site-a"},
		{"no referer", "access-h", "", "/sites/a/access", "/sites/a/access#access-h"},
		{"other page", "templates-h", "http://ui.test/", "/sites/new", "/sites/new"},
		{"no section", "", "http://ui.test/sites/a/access", "/sites/a/access#domains-h", "/sites/a/access#domains-h"},
		{"bad section", "x\"><b>", "http://ui.test/sites/a/access", "/sites/a/access", "/sites/a/access"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			form := url.Values{sectionField: {c.section}}
			r := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(form.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if c.referer != "" {
				r.Header.Set("Referer", c.referer)
			}
			if got := withSection(r, c.target); got != c.want {
				t.Errorf("withSection = %q, want %q", got, c.want)
			}
		})
	}
}

func TestFormSectionSkipsUploads(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader("--b\r\n"))
	r.Header.Set("Content-Type", "multipart/form-data; boundary=b")
	if got := formSection(r); got != "" {
		t.Fatalf("formSection on an upload = %q", got)
	}
	if r.PostForm != nil {
		t.Fatal("formSection parsed an upload body")
	}
	get := httptest.NewRequest(http.MethodGet, "/x?_section=access-h", nil)
	if got := formSection(get); got != "" {
		t.Fatalf("formSection on GET = %q", got)
	}
}

// between reports whether sub occurs in body after the first marker and
// before the second one.
func between(body, sub, after, before string) bool {
	a := strings.Index(body, after)
	s := strings.Index(body, sub)
	b := strings.Index(body, before)
	return a >= 0 && s > a && b > s
}

func TestFormResultShowsInItsSection(t *testing.T) {
	h := newHarness(t)
	ann := h.loginUser("ann@pp.test")
	site := h.createSite(ann, "Promo", "")
	base := "/sites/" + site.Site.ID
	referer := []string{"Referer", h.srv.URL + base + "/access"}

	// An error in the lower section comes back to that section.
	p := h.submit(base+"/access", base+"/domains",
		url.Values{"domain": {"bad..domain"}, sectionField: {"domains-h"}}, referer...)
	wantStatus(t, p, http.StatusSeeOther)
	if p.location() != base+"/access#domains-h" {
		t.Fatalf("location = %q", p.location())
	}
	page := h.follow(p)
	wantContains(t, page.body, `role="alert" tabindex="-1" data-flash="section"`, "не похоже на домен")
	if !between(page.body, `data-flash="section"`, `id="domains-h"`, `class="domain-add"`) {
		t.Error("the error is not right above the domain form")
	}
	wantNotContains(t, page.body, `data-flash="toast"`)

	// A success in the access section, above its form.
	p = h.submit(base+"/access", base+"/access", url.Values{"noindex": {"1"}, sectionField: {"access-h"}}, referer...)
	if p.location() != base+"/access#access-h" {
		t.Fatalf("location = %q", p.location())
	}
	page = h.follow(p)
	wantContains(t, page.body, `role="status" tabindex="-1" data-flash="section"`, "Настройки доступа сохранены")
	if !between(page.body, `data-flash="section"`, `id="access-h"`, `class="stack access"`) {
		t.Error("the result is not right above the access form")
	}

	// A forged section is dropped: the message is a toast and the address
	// keeps the handler's own anchor.
	p = h.submit(base+"/access", base+"/domains",
		url.Values{"domain": {"bad..domain"}, sectionField: {`x"><script>`}}, referer...)
	if p.location() != base+"/access#domains-h" {
		t.Fatalf("location = %q", p.location())
	}
	page = h.follow(p)
	wantContains(t, page.body, `data-flash="toast"`, "не похоже на домен")
	wantNotContains(t, page.body, `data-flash="section"`, "<script>")
}

func TestFlashWithoutSectionIsToast(t *testing.T) {
	h := newHarness(t)
	ann := h.loginUser("ann@pp.test")
	site := h.createSite(ann, "Promo", "")
	base := "/sites/" + site.Site.ID

	// Deleting the site opens the sites list, which has no "danger-h": the
	// message becomes a toast there and the address gets no stray anchor.
	p := h.submit(base+"/settings", base+"/delete", url.Values{"confirm": {"Promo"}, sectionField: {"danger-h"}},
		"Referer", h.srv.URL+base+"/settings")
	if p.location() != "/" {
		t.Fatalf("location = %q", p.location())
	}
	page := h.follow(p)
	wantContains(t, page.body, `class="toast notice notice--ok notice--flash" role="status"`, `data-flash="toast"`,
		`data-toast-close aria-label="Закрыть сообщение"`, "удалён")
	wantNotContains(t, page.body, "data-sticky", `data-flash="section"`)
	// Without JavaScript the toast is a plain notice at the top of the page.
	if !between(page.body, `data-flash="toast"`, `<main id="main"`, `class="page-head"`) {
		t.Error("the toast is not at the top of the page")
	}

	// An error without a section stays until closed.
	site2 := h.createSite(ann, "Second", "")
	base2 := "/sites/" + site2.Site.ID
	page = h.follow(h.submit(base2+"/access", base2+"/domains", url.Values{"domain": {"bad..domain"}}))
	wantContains(t, page.body, `class="toast notice notice--error notice--flash" role="alert"`, "data-sticky")
}

func TestToastCloseLabelIsTranslated(t *testing.T) {
	h := newHarness(t)
	ann := h.loginUser("ann@pp.test")
	for lang, label := range map[string]string{"en": "Close the message", "zh": "关闭消息", "ru": "Закрыть сообщение"} {
		h.submit("/", "/lang", url.Values{"lang": {lang}, "next": {"/"}})
		site := h.createSite(ann, "Promo "+lang, "")
		base := "/sites/" + site.Site.ID
		page := h.follow(h.submit(base+"/access", base+"/domains", url.Values{"domain": {"bad..domain"}}))
		wantContains(t, page.body, `aria-label="`+label+`"`)
	}
}

func TestDeployedFlashKeepsSiteLink(t *testing.T) {
	h := newHarness(t)
	h.loginUser("ann@pp.test")
	p := h.submit("/", "/sites/from-template", url.Values{"template": {"event"}, "title": {"Фест"}, "lang": {"ru"},
		sectionField: {"templates-h"}}, "Referer", h.srv.URL+"/")
	wantStatus(t, p, http.StatusSeeOther)
	if strings.Contains(p.location(), "#") {
		t.Fatalf("location = %q", p.location())
	}
	page := h.follow(p)
	// The new site's page has no template section: a toast that stays, with
	// the link to the published site.
	wantContains(t, page.body, `data-flash="toast" data-sticky`, `class="btn btn--small btn--ok"`, "↗")
}

// The template form kept working after a site was created: a second submit
// reaches the server, and when it is refused (here the address is taken) the
// reason shows in the template section, where the person is.
func TestTemplateFormSecondSubmitShowsResultInSection(t *testing.T) {
	h := newHarness(t)
	h.loginUser("ann@pp.test")
	form := func() url.Values {
		return url.Values{"template": {"event"}, "slug": {"fest"}, "lang": {"ru"}, sectionField: {"templates-h"}}
	}
	referer := []string{"Referer", h.srv.URL + "/"}
	first := h.submit("/", "/sites/from-template", form(), referer...)
	wantStatus(t, first, http.StatusSeeOther)
	h.follow(first)

	list := h.get("/")
	wantContains(t, list.body, `name="_section" value="templates-h"`)
	tpl := list.body[strings.Index(list.body, `action="/sites/from-template"`):]
	tpl = tpl[:strings.Index(tpl, "</form>")]
	wantNotContains(t, tpl, "disabled", "hx-", "data-once")

	second := h.submit("/", "/sites/from-template", form(), referer...)
	wantStatus(t, second, http.StatusSeeOther)
	if second.location() != "/#templates-h" {
		t.Fatalf("location = %q", second.location())
	}
	page := h.follow(second)
	wantContains(t, page.body, "уже занят")
	if !between(page.body, `data-flash="section"`, `id="templates-h"`, `action="/sites/from-template"`) {
		t.Error("the refusal is not in the template section")
	}
}
