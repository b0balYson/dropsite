package ui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/service"
)

func TestCatalogsHaveSameKeys(t *testing.T) {
	cats, err := loadCatalogs()
	if err != nil {
		t.Fatal(err)
	}
	ru, en := cats["ru"].msgs, cats["en"].msgs
	for k := range ru {
		if _, ok := en[k]; !ok {
			t.Errorf("en lacks %q", k)
		}
	}
	for k := range en {
		if _, ok := ru[k]; !ok {
			t.Errorf("ru lacks %q", k)
		}
	}
	for k, v := range ru {
		if strings.TrimSpace(v) == "" || strings.TrimSpace(en[k]) == "" {
			t.Errorf("%q has an empty translation", k)
		}
	}
}

// domainCodes parses internal/domain/errors.go so that a new error code
// cannot be added without a translation.
func domainCodes(t *testing.T) []string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "../domain/errors.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var codes []string
	ast.Inspect(f, func(n ast.Node) bool {
		vs, ok := n.(*ast.ValueSpec)
		if !ok {
			return true
		}
		for i, name := range vs.Names {
			if !strings.HasPrefix(name.Name, "Code") || i >= len(vs.Values) {
				continue
			}
			if lit, ok := vs.Values[i].(*ast.BasicLit); ok {
				v, _ := strconv.Unquote(lit.Value)
				codes = append(codes, v)
			}
		}
		return true
	})
	if len(codes) < 30 {
		t.Fatalf("parsed only %d codes", len(codes))
	}
	return codes
}

func TestEveryErrorCodeIsTranslated(t *testing.T) {
	cats, err := loadCatalogs()
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range domainCodes(t) {
		for lang, l := range cats {
			if !l.Has(errorKey(code)) {
				t.Errorf("%s: no translation for error code %q", lang, code)
			}
		}
	}
}

var (
	tmplKeyRe = regexp.MustCompile(`\.T "([a-z0-9_.]+)"`)
	goKeyRe   = regexp.MustCompile(`"([a-z_]+(?:\.[a-z0-9_]+)+)"`)
)

// TestUsedKeysExist checks every literal key used in templates and Go code,
// plus the keys built dynamically from enumerations.
func TestUsedKeysExist(t *testing.T) {
	cats, err := loadCatalogs()
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]bool{}
	_ = fs.WalkDir(assets, "templates", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, _ := assets.ReadFile(p)
		for _, m := range tmplKeyRe.FindAllStringSubmatch(string(b), -1) {
			keys[m[1]] = true
		}
		return nil
	})
	goFiles, _ := os.ReadDir(".")
	for _, f := range goFiles {
		if !strings.HasSuffix(f.Name(), ".go") || strings.HasSuffix(f.Name(), "_test.go") {
			continue
		}
		b, _ := os.ReadFile(f.Name())
		for _, m := range goKeyRe.FindAllStringSubmatch(string(b), -1) {
			if !strings.HasSuffix(m[1], ".html") && !strings.HasSuffix(m[1], ".sh") {
				keys[m[1]] = true
			}
		}
	}
	for _, k := range []string{"role.user", "role.admin", "status.active", "status.invited", "status.blocked"} {
		keys[k] = true
	}
	for _, s := range []domain.DeploySource{domain.SourceUI, domain.SourceAPI, domain.SourceCLI, domain.SourceMCP} {
		keys["source."+string(s)] = true
	}
	for _, s := range []string{"active", "expired", "revoked"} {
		keys["connect.state."+s] = true
	}
	for _, k := range service.SettingKeys {
		keys["settings."+k] = true
		if k != service.SettingReservedSlugs {
			keys["settings."+k+".hint"] = true
		}
	}
	for m := 1; m <= 12; m++ {
		keys["month."+strconv.Itoa(m)] = true
	}
	var missing []string
	for k := range keys {
		for lang, l := range cats {
			if !l.Has(k) {
				missing = append(missing, lang+":"+k)
			}
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("missing keys: %v", missing)
	}
}

func TestAcceptLanguage(t *testing.T) {
	cases := map[string]string{
		"":                           "",
		"en-US,en;q=0.9":             "en",
		"de-DE,de;q=0.9,en;q=0.5":    "en",
		"fr, ru;q=0.4, en;q=0.8":     "en",
		"ru-RU,ru;q=0.9,en-US;q=0.8": "ru",
		"en;q=0":                     "",
	}
	for in, want := range cases {
		if got := acceptLanguage(in); got != want {
			t.Errorf("acceptLanguage(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFormatting(t *testing.T) {
	cats, _ := loadCatalogs()
	ru, en := cats.locale("ru"), cats.locale("en")
	if got := ru.Size(1536); got != "1,5 КБ" {
		t.Errorf("ru size = %q", got)
	}
	if got := en.Size(3 * gib); got != "3 GB" {
		t.Errorf("en size = %q", got)
	}
	if got := en.Size(12); got != "12 B" {
		t.Errorf("bytes = %q", got)
	}
	if got := ru.MB(25 * mib / 2); got != "12.5" {
		t.Errorf("MB = %q", got)
	}
	d := time.Date(2026, 3, 5, 7, 8, 0, 0, time.UTC)
	if got := ru.Date(d); got != "5 мар 2026, 07:08 UTC" {
		t.Errorf("ru date = %q", got)
	}
	if got := en.DatePtr(nil); got != "—" {
		t.Errorf("nil date = %q", got)
	}
	if got := cats.locale("de").Lang; got != "ru" {
		t.Errorf("fallback = %q", got)
	}
	if got := en.T("missing.key"); got != "missing.key" {
		t.Errorf("missing key = %q", got)
	}
}

func TestErrorDescriptionsUseParams(t *testing.T) {
	h := newHarness(t)
	rc := &reqCtx{loc: h.mustLocale("ru")}
	s := &server{log: slog.New(slog.NewTextHandler(h.log, nil))}
	msg, status := s.describe(rc, domain.E(domain.KindTooLarge, domain.CodeArchiveFileTooBig, "x").
		WithParams(map[string]any{"limit": int64(5 * mib), "path": "img/big.png"}))
	if status != http.StatusRequestEntityTooLarge || !strings.Contains(msg, "img/big.png") || !strings.Contains(msg, "5 МБ") {
		t.Errorf("describe = %q %d", msg, status)
	}
	msg, status = s.describe(rc, domain.ErrRateLimited.WithRetryAfter(90*time.Second))
	if status != http.StatusTooManyRequests || !strings.Contains(msg, "2 мин") {
		t.Errorf("rate limited = %q", msg)
	}
	msg, _ = s.describe(rc, domain.E(domain.KindInvalid, domain.CodeSettingInvalid, "x").WithParams(map[string]any{"key": "max_files"}))
	if !strings.Contains(msg, "Число файлов") {
		t.Errorf("setting invalid = %q", msg)
	}
	msg, status = s.describe(rc, os.ErrPermission)
	if status != http.StatusInternalServerError || strings.Contains(msg, "permission") {
		t.Errorf("internal = %q %d", msg, status)
	}
	if !strings.Contains(h.log.String(), "permission denied") {
		t.Error("internal error was not logged")
	}
}

func (h *harness) mustLocale(lang string) *locale {
	cats, err := loadCatalogs()
	if err != nil {
		h.t.Fatal(err)
	}
	return cats.locale(lang)
}

func TestLanguageSwitch(t *testing.T) {
	h := newHarness(t)
	alice := h.loginUser("alice@pp.test")
	ru := h.get("/")
	wantContains(t, ru.body, `<html lang="ru">`, "Мои сайты")

	p := h.submit("/", "/lang", url.Values{"lang": {"en"}, "next": {"/connect"}})
	wantStatus(t, p, http.StatusSeeOther)
	if p.location() != "/connect" {
		t.Fatalf("lang redirect = %q", p.location())
	}
	en := h.get("/connect")
	wantContains(t, en.body, `<html lang="en">`, "Connect AI", "Issue key")
	wantNotContains(t, en.body, "Мои сайты")

	// The choice is stored on the user: a new browser without the cookie
	// still gets English after signing in.
	fresh := newHarnessSharing(t, h)
	fresh.login(alice.User.Email, "user-password-1")
	wantContains(t, fresh.get("/").body, "My sites")

	// Accept-Language decides for anonymous visitors without a cookie.
	anon := newHarnessSharing(t, h)
	wantContains(t, anon.get("/login", "Accept-Language", "en-GB,en;q=0.9").body, "Sign in")
	wantContains(t, anon.get("/login").body, "Вход")

	// Unsupported values are ignored; foreign next targets are not followed.
	bad := h.submit("/", "/lang", url.Values{"lang": {"de"}, "next": {"https://evil.example/"}})
	if bad.location() != "/" {
		t.Fatalf("lang redirect = %q", bad.location())
	}
	wantContains(t, h.get("/").body, "My sites")
}

func TestBreakableEscapesAndAddsBreaks(t *testing.T) {
	got := string(breakable(`a.b@c.d:8443/<script>"`))
	want := `a.<wbr>b@<wbr>c.<wbr>d<wbr>:8443/<wbr>&lt;script&gt;&#34;`
	if got != want {
		t.Fatalf("breakable = %q, want %q", got, want)
	}
}
