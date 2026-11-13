package deploy

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// TestUploadLinkRedaction checks the access log filter of the API host: the
// token of an upload link never reaches the log. Caddy uses Go's regexp
// package, so the pattern and replacement behave the same here.
func TestUploadLinkRedaction(t *testing.T) {
	data, err := os.ReadFile("Caddyfile")
	if err != nil {
		t.Fatal(err)
	}
	const prefix = "request>uri regexp "
	var re *regexp.Regexp
	var repl string
	for _, line := range strings.Split(string(data), "\n") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), prefix)
		if !ok || !strings.Contains(rest, "/u") {
			continue
		}
		pat, tail, _ := strings.Cut(strings.TrimPrefix(rest, `"`), `" "`)
		raw, err := strconv.Unquote(`"` + strings.TrimSuffix(tail, `"`) + `"`)
		if err != nil {
			t.Fatal(err)
		}
		unq, err := strconv.Unquote(`"` + pat + `"`)
		if err != nil {
			t.Fatal(err)
		}
		re, repl = regexp.MustCompile(unq), raw
	}
	if re == nil {
		t.Fatal("no upload link filter in the Caddyfile")
	}
	const token = "q3J8x_Zk-0aBcDeFgHiJkLmNoPqRsTuVwXyZ0123456"
	for in, want := range map[string]string{
		"/u/" + token:                   "/u/REDACTED",
		"/u/" + token + "/img/a.png":    "/u/REDACTED/img/a.png",
		"/u/" + token + "?ui_lang=en":   "/u/REDACTED?ui_lang=en",
		"/u%2F" + token + "/index.html": "/u%2FREDACTED/index.html",
		"/api/v1/uploads/up_1/files":    "/api/v1/uploads/up_1/files",
		"/mcp":                          "/mcp",
	} {
		if got := re.ReplaceAllString(in, repl); got != want || strings.Contains(got, token) {
			t.Errorf("%s -> %s, want %s", in, got, want)
		}
	}
	if !strings.Contains(string(data), "request_body /u/* {") {
		t.Error("no body cap for upload links")
	}
}
