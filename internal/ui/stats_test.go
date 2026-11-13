package ui

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/b0balYson/dropsite/internal/config"
	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/testenv"
)

var update = flag.Bool("update", false, "rewrite golden files")

func sampleDays() []domain.DayStat {
	views := []int64{0, 3, 7, 12, 4, 0, 0, 25, 31, 18, 9, 0, 2, 5, 40, 38, 22, 11, 6, 0, 0, 14, 17, 26, 47, 33, 12, 8, 1, 19}
	days := make([]domain.DayStat, 0, len(views))
	for i, v := range views {
		days = append(days, domain.DayStat{Day: testenv.Start.AddDate(0, 0, i-len(views)+1).Format("2006-01-02"), Views: v, Visitors: v / 2})
	}
	return days
}

func TestViewsChartGolden(t *testing.T) {
	cats, err := loadCatalogs()
	if err != nil {
		t.Fatal(err)
	}
	for _, lang := range []string{"ru", "en"} {
		got := string(viewsChart(cats[lang], sampleDays(), "c"))
		golden := filepath.Join("testdata", "chart_"+lang+".svg")
		if *update {
			if err := os.WriteFile(golden, []byte(got+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		want, err := os.ReadFile(golden)
		if err != nil {
			t.Fatalf("%v (run go test ./internal/ui -run Golden -update)", err)
		}
		if got+"\n" != string(want) {
			t.Errorf("%s chart differs from %s; run with -update after checking the change", lang, golden)
		}
	}
}

func TestViewsChartShape(t *testing.T) {
	cats, err := loadCatalogs()
	if err != nil {
		t.Fatal(err)
	}
	svg := string(viewsChart(cats["en"], sampleDays(), "c"))
	if n := strings.Count(svg, `class="chart__bar"`); n != 24 {
		t.Errorf("bars = %d, want one per day with views (24)", n)
	}
	if n := strings.Count(svg, `class="chart__day"`); n != 30 {
		t.Errorf("hit areas = %d, want 30", n)
	}
	for _, want := range []string{`role="img"`, `aria-labelledby="c-title c-desc"`, "Page views over 30 days",
		"12 Nov: 19 page views, 9 visitors", ">50<", ">25<", ">0<", ">47<"} {
		if !strings.Contains(svg, want) {
			t.Errorf("chart lacks %q", want)
		}
	}
	if strings.Contains(svg, "style=") || strings.Contains(svg, "<script") {
		t.Error("the chart must not need inline styles or scripts (CSP)")
	}
	if viewsChart(cats["en"], nil, "c") != "" {
		t.Error("no days, no chart")
	}
}

func TestNiceCeilAndNum(t *testing.T) {
	for in, want := range map[int64]int64{0: 2, 1: 2, 3: 5, 7: 10, 10: 10, 11: 20, 47: 50, 120: 200, 999: 1000} {
		if got := niceCeil(in); got != want {
			t.Errorf("niceCeil(%d) = %d, want %d", in, got, want)
		}
	}
	cats, _ := loadCatalogs()
	if got := cats["en"].Num(1234567); got != "1,234,567" {
		t.Errorf("en Num = %q", got)
	}
	if got := cats["ru"].Num(-12345); got != "-12 345" {
		t.Errorf("ru Num = %q", got)
	}
	if got := cats["ru"].ShortDay("2026-11-12"); got != "12 ноя" {
		t.Errorf("ru ShortDay = %q", got)
	}
	if got := cats["en"].ShortDay("bad"); got != "bad" {
		t.Errorf("bad day = %q", got)
	}
}

func accessLine(host, uri, ref string) string {
	return fmt.Sprintf(`{"ts":%d,"request":{"client_ip":"198.51.100.1","method":"GET","host":%q,"uri":%q,`+
		`"headers":{"User-Agent":["Mozilla/5.0 Firefox/132.0"],"Referer":[%q]}},"status":200,`+
		`"resp_headers":{"Content-Type":["text/html"]}}`+"\n", testenv.Start.Unix(), host, uri, ref)
}

func TestSitePageShowsStats(t *testing.T) {
	logFile := filepath.Join(t.TempDir(), "sites-access.log")
	h := newHarness(t, testenv.WithConfig(func(c *config.Config) { c.StatsLogFile = logFile }))
	alice := h.loginUser("alice@pp.test")
	v := h.createSite(alice, "Promo", "promo")

	empty := h.get("/sites/" + v.Site.ID + "/stats")
	wantStatus(t, empty, http.StatusOK)
	wantContains(t, empty.body, "Посещаемость", "Посещений пока нет")
	wantNotContains(t, empty.body, "chart__svg")

	host := "promo.pp.test"
	lines := accessLine(host, "/", "https://t.me/x") + accessLine(host, "/%3Cscript%3E", "") +
		accessLine(host, "/", "https://evil.example/<b>")
	if err := os.WriteFile(logFile, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := h.env.Svc.CollectStats(context.Background()); err != nil {
		t.Fatal(err)
	}
	p := h.get("/sites/" + v.Site.ID + "/stats")
	wantStatus(t, p, http.StatusOK)
	wantContains(t, p.body, `class="chart__svg"`, "Просмотры страниц за 30 дн.", "Показать данные таблицей",
		"Популярные страницы", "t.<wbr>me", "Прямые заходы", "&lt;script&gt;", "12 ноя")
	wantNotContains(t, p.body, "<script>", "<b>")
}

func TestCountersForm(t *testing.T) {
	h := newHarness(t)
	alice := h.loginUser("alice@pp.test")
	v := h.createSite(alice, "Promo", "")
	path := "/sites/" + v.Site.ID

	bad := h.submit(path, path+"/counters", url.Values{"metrika_id": {`1"><script>`}})
	wantStatus(t, bad, http.StatusSeeOther)
	wantContains(t, h.follow(bad).body, "Неверный идентификатор счётчика")

	ok := h.submit(path, path+"/counters", url.Values{"metrika_id": {"12345678"}, "ga4_id": {"g-abcd1234"}})
	wantStatus(t, ok, http.StatusSeeOther)
	page := h.follow(ok)
	wantContains(t, page.body, "Код появится на сайте со следующей публикацией", `value="12345678"`, `value="G-ABCD1234"`)

	h.deploy(alice, v.Site.ID, map[string]string{"index.html": "<html><head></head><body>x</body></html>"})
	changed := h.submit(path, path+"/counters", url.Values{"metrika_id": {"87654321"}})
	wantContains(t, h.follow(changed).body, "сайт переключён на новый релиз")
	served, err := os.ReadFile(filepath.Join(h.env.Config.SitesDir, "by-host", v.Site.ID+".pp.test", "index.html"))
	if err != nil || !strings.Contains(string(served), "ym(87654321") {
		t.Fatalf("served page lacks the counter: %v\n%s", err, served)
	}

	// Another user's site is not found.
	eve := h.env.User("eve@pp.test")
	other, err := h.env.Svc.CreateSite(context.Background(), eve, "Eve", "")
	if err != nil {
		t.Fatal(err)
	}
	wantStatus(t, h.submit(path, "/sites/"+other.Site.ID+"/counters", url.Values{"metrika_id": {"1234"}}), http.StatusNotFound)
}

func TestAdminSitesViewsAndGlobalCounters(t *testing.T) {
	logFile := filepath.Join(t.TempDir(), "sites-access.log")
	h := newHarness(t, testenv.WithConfig(func(c *config.Config) { c.StatsLogFile = logFile }))
	owner := h.env.User("owner@pp.test")
	v, err := h.env.Svc.CreateSite(context.Background(), owner, "Promo", "promo")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logFile, []byte(strings.Repeat(accessLine("promo.pp.test", "/", ""), 3)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := h.env.Svc.CollectStats(context.Background()); err != nil {
		t.Fatal(err)
	}
	h.loginAdmin()
	list := h.get("/admin/sites")
	wantStatus(t, list, http.StatusOK)
	wantContains(t, list.body, "Просмотры, 7 дн.", v.Site.ID)
	if !regexp.MustCompile(`Просмотры, 7 дн\.</dt><dd class="mono">3</dd>`).MatchString(list.body) {
		t.Errorf("views column missing:\n%s", list.body)
	}

	p := h.submit("/admin/settings", "/admin/settings", url.Values{"counter_metrika_id": {"1234"}, "counter_ga4_id": {""}})
	wantStatus(t, p, http.StatusSeeOther)
	wantContains(t, h.follow(p).body, `value="1234"`, "Счётчики для всех сайтов")
	bad := h.submit("/admin/settings", "/admin/settings", url.Values{"counter_ga4_id": {"UA-1"}})
	wantContains(t, h.follow(bad).body, "Неверный идентификатор счётчика")
}
