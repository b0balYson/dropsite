package stats

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"
)

// line builds an access-log line in the shape Caddy 2.11 writes.
func line(host, method, uri string, status int, ctype, ua, ref string) string {
	headers := map[string]string{}
	if ua != "" {
		headers["User-Agent"] = ua
	}
	if ref != "" {
		headers["Referer"] = ref
	}
	var hb []string
	for k, v := range headers {
		hb = append(hb, fmt.Sprintf("%q:[%q]", k, v))
	}
	resp := `"Server":["Caddy"]`
	if ctype != "" {
		resp += fmt.Sprintf(`,"Content-Type":[%q]`, ctype)
	}
	return fmt.Sprintf(`{"level":"info","ts":1794493307.782514,"logger":"http.log.access.sites","msg":"handled request",`+
		`"request":{"remote_ip":"172.17.0.1","remote_port":"33806","client_ip":"203.0.113.7","proto":"HTTP/2.0",`+
		`"method":%q,"host":%q,"uri":%q,"headers":{%s}},"bytes_read":0,"user_id":"","duration":0.004,"size":42,`+
		`"status":%d,"resp_headers":{%s}}`, method, host, uri, strings.Join(hb, ","), status, resp)
}

const browser = "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_0) AppleWebKit/605.1.15 Safari/605.1.15"

func TestParseLine(t *testing.T) {
	h, err := ParseLine([]byte(line("Promo.Example.com:8443", "GET", "/about/?utm=1", 200,
		"text/html; charset=utf-8", browser, "https://www.google.com/search?q=x")))
	if err != nil {
		t.Fatal(err)
	}
	if h.Host != "promo.example.com" || h.Status != 200 || h.ClientIP != "203.0.113.7" ||
		h.UserAgent != browser || h.Path() != "/about/" || h.RefererHost() != "www.google.com" {
		t.Errorf("unexpected hit: %+v", h)
	}
	if got := h.Time.Format(time.RFC3339); got != "2026-11-12T14:21:47Z" {
		t.Errorf("time = %s", got)
	}
	if h.Day() != "2026-11-12" {
		t.Errorf("day = %s", h.Day())
	}
}

func TestParseLineRejects(t *testing.T) {
	for _, in := range []string{"", "not json", "{}", `{"ts":1,"request":{}}`, `{"level":"info","msg":"server running"}`} {
		if _, err := ParseLine([]byte(in)); err == nil {
			t.Errorf("ParseLine(%q) accepted", in)
		}
	}
}

func TestIsPageView(t *testing.T) {
	cases := []struct {
		name   string
		method string
		uri    string
		status int
		ctype  string
		ua     string
		want   bool
	}{
		{"root", "GET", "/", 200, "text/html; charset=utf-8", browser, true},
		{"html file", "GET", "/a/page.html", 200, "text/html", browser, true},
		{"htm upper", "GET", "/A.HTM", 200, "text/html", browser, true},
		{"clean url", "GET", "/pricing", 200, "text/html", browser, true},
		{"dir", "GET", "/docs/", 200, "text/html", browser, true},
		{"revalidated", "GET", "/", 304, "", browser, true},
		{"css", "GET", "/app.css", 200, "text/css", browser, false},
		{"png by path only", "GET", "/logo.png", 304, "", browser, false},
		{"json without extension", "GET", "/api", 200, "application/json", browser, false},
		{"not found", "GET", "/missing", 404, "text/html", browser, false},
		{"server error", "GET", "/", 500, "text/html", browser, false},
		{"head", "HEAD", "/", 200, "text/html", browser, false},
		{"post", "POST", "/", 200, "text/html", browser, false},
		{"bot", "GET", "/", 200, "text/html", "Mozilla/5.0 (compatible; Googlebot/2.1)", false},
		{"no ua", "GET", "/", 200, "text/html", "", false},
		{"curl", "GET", "/", 200, "text/html", "curl/8.7.1", false},
		{"bad content type", "GET", "/", 200, ";;;", browser, false},
		{"relative uri", "GET", "*", 200, "text/html", browser, false},
	}
	for _, c := range cases {
		h, err := ParseLine([]byte(line("a.example.com", c.method, c.uri, c.status, c.ctype, c.ua, "")))
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got := h.IsPageView(); got != c.want {
			t.Errorf("%s: IsPageView = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestIsBot(t *testing.T) {
	bots := []string{"", "  ", "Googlebot/2.1", "Mozilla/5.0 (compatible; YandexBot/3.0; +http://yandex.com/bots)",
		"Wget/1.21", "python-requests/2.31", "Go-http-client/2.0", "Mozilla/5.0 HeadlessChrome/120",
		"facebookexternalhit/1.1", "TelegramBot (like TwitterBot)", "Chrome-Lighthouse", "UptimeRobot/2.0"}
	for _, ua := range bots {
		if !IsBot(ua) {
			t.Errorf("IsBot(%q) = false", ua)
		}
	}
	people := []string{browser,
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Safari/537.36",
		"Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 Mobile/15E148",
		"Mozilla/5.0 (X11; Linux x86_64; rv:132.0) Gecko/20100101 Firefox/132.0 YaBrowser/24.10"}
	for _, ua := range people {
		if IsBot(ua) {
			t.Errorf("IsBot(%q) = true", ua)
		}
	}
}

func TestPagePath(t *testing.T) {
	cases := map[string]string{
		"/":                                     "/",
		"/a?b=c":                                "/a",
		"/%D0%BF%D1%80%D0%B8%D0%B2":             "/прив",
		"/" + strings.Repeat("a", MaxPathBytes): Other,
		"/bad%FF":                               Other,
		"/ctl%0A":                               Other,
	}
	for uri, want := range cases {
		h := Hit{URI: uri}
		if got := h.PagePath(); got != want {
			t.Errorf("PagePath(%q) = %q, want %q", uri, got, want)
		}
	}
}

func TestRefererHost(t *testing.T) {
	cases := map[string]string{
		"":                              "",
		"https://Yandex.RU/search/?q=1": "yandex.ru",
		"http://t.me:443/x":             "t.me",
		"android-app://com.google":      "",
		"javascript:alert(1)":           "",
		"https://<x>.com/":              "",
		"::::":                          "",
	}
	for ref, want := range cases {
		if got := (Hit{Referer: ref}).RefererHost(); got != want {
			t.Errorf("RefererHost(%q) = %q, want %q", ref, got, want)
		}
	}
}

func TestVisitorHash(t *testing.T) {
	s1 := bytes.Repeat([]byte{1}, SaltBytes)
	s2 := bytes.Repeat([]byte{2}, SaltBytes)
	a := VisitorHash(s1, "203.0.113.7", browser)
	if len(a) != VisitorHashBytes {
		t.Fatalf("len = %d", len(a))
	}
	if !bytes.Equal(a, VisitorHash(s1, "203.0.113.7", browser)) {
		t.Error("same input must give the same hash")
	}
	if bytes.Equal(a, VisitorHash(s2, "203.0.113.7", browser)) {
		t.Error("another salt must give another hash")
	}
	if bytes.Equal(a, VisitorHash(s1, "203.0.113.8", browser)) {
		t.Error("another IP must give another hash")
	}
	if bytes.Equal(VisitorHash(s1, "1.2.3.4", "5"), VisitorHash(s1, "1.2.3.", "45")) {
		t.Error("IP and UA must be separated")
	}
	salt, err := NewSalt()
	if err != nil || len(salt) != SaltBytes || bytes.Equal(salt, make([]byte, SaltBytes)) {
		t.Errorf("NewSalt = %x, %v", salt, err)
	}
}

func TestBatchAddLine(t *testing.T) {
	sites := map[string]string{"a.example.com": "site1", "promo.example.com": "site1", "b.example.com": "site2"}
	site := func(h string) (string, bool) { id, ok := sites[h]; return id, ok }
	salt := bytes.Repeat([]byte{7}, SaltBytes)
	salts := func(day string) ([]byte, error) {
		if day == "2026-11-12" {
			return salt, nil
		}
		return nil, nil
	}
	b := NewBatch()
	lines := []string{
		line("a.example.com", "GET", "/", 200, "text/html", browser, ""),
		line("a.example.com", "GET", "/about", 200, "text/html", browser, "https://promo.example.com/"),
		line("promo.example.com", "GET", "/", 304, "", browser, "https://news.ycombinator.com/item"),
		line("a.example.com", "GET", "/app.css", 200, "text/css", browser, ""),
		line("unknown.example.com", "GET", "/", 200, "text/html", browser, ""),
		line("b.example.com", "GET", "/", 200, "text/html", "Googlebot", ""),
		"garbage",
	}
	for _, l := range lines {
		if err := b.AddLine([]byte(l), site, salts); err != nil {
			t.Fatal(err)
		}
	}
	k := Key{SiteID: "site1", Day: "2026-11-12"}
	if b.Views[k] != 3 || len(b.Views) != 1 {
		t.Errorf("views = %v", b.Views)
	}
	if b.Pages[k]["/"] != 2 || b.Pages[k]["/about"] != 1 {
		t.Errorf("pages = %v", b.Pages[k])
	}
	if b.Referrers[k][""] != 1 || b.Referrers[k]["news.ycombinator.com"] != 1 || len(b.Referrers[k]) != 2 {
		t.Errorf("referrers = %v (self-referrals must not count)", b.Referrers[k])
	}
	if len(b.Visitors[k]) != 1 {
		t.Errorf("one visitor expected, got %d", len(b.Visitors[k]))
	}
	if b.Malformed != 1 {
		t.Errorf("malformed = %d", b.Malformed)
	}
	if b.Empty() {
		t.Error("batch must not be empty")
	}
}

func TestBatchClosedDayCountsViewsOnly(t *testing.T) {
	b := NewBatch()
	site := func(string) (string, bool) { return "s", true }
	none := func(string) ([]byte, error) { return nil, nil }
	if err := b.AddLine([]byte(line("x.example.com", "GET", "/", 200, "text/html", browser, "")), site, none); err != nil {
		t.Fatal(err)
	}
	k := Key{SiteID: "s", Day: "2026-11-12"}
	if b.Views[k] != 1 || len(b.Visitors) != 0 {
		t.Errorf("views=%v visitors=%v", b.Views, b.Visitors)
	}
}

func TestBatchSkipHost(t *testing.T) {
	site := func(h string) (string, bool) { return "s", h == "a.example.com" || h == "p-x.example.com" }
	none := func(string) ([]byte, error) { return nil, nil }
	b := NewBatch()
	b.SkipHost = func(h string) bool { return h == "p-x.example.com" }
	for _, l := range []string{
		line("p-x.example.com", "GET", "/", 200, "text/html", browser, ""),
		line("a.example.com", "GET", "/", 200, "text/html", browser, "https://p-x.example.com/"),
	} {
		if err := b.AddLine([]byte(l), site, none); err != nil {
			t.Fatal(err)
		}
	}
	k := Key{SiteID: "s", Day: "2026-11-12"}
	if b.Views[k] != 1 || len(b.Referrers[k]) != 0 {
		t.Fatalf("views %v referrers %v", b.Views, b.Referrers)
	}
}
