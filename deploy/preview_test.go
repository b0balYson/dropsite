package deploy

import (
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
)

// previewMatcher reads the Host pattern of the @preview matcher from the
// Caddyfile. Caddy uses Go's regexp package, so the pattern behaves the same
// here.
func previewMatcher(t *testing.T) *regexp.Regexp {
	t.Helper()
	data, err := os.ReadFile("Caddyfile")
	if err != nil {
		t.Fatal(err)
	}
	const prefix = "@preview header_regexp Host "
	for _, line := range strings.Split(string(data), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), prefix); ok {
			return regexp.MustCompile(rest)
		}
	}
	t.Fatal("no @preview matcher in the Caddyfile")
	return nil
}

// TestPreviewMatcherFollowsDomain checks that every preview address gets the
// noindex header and no ordinary site address does, without Docker.
func TestPreviewMatcherFollowsDomain(t *testing.T) {
	re := previewMatcher(t)
	site := domain.NewSiteID()
	release := domain.PreviewLabel(site, domain.NewDeployID(time.Now()))
	draft := domain.DraftPreviewLabel(site)
	if !domain.IsPreviewLabel(release) || !domain.IsDraftPreviewLabel(draft) {
		t.Fatal("labels do not match their own patterns")
	}
	for _, label := range []string{release, draft} {
		if !re.MatchString(label + ".example.com") {
			t.Errorf("%s must be matched as a preview", label)
		}
	}
	for _, host := range []string{site + ".example.com", "p-shop.example.com", "shop.example.com", "example.com",
		"p-" + site + "x.example.com", "www.p-" + site + ".example.com"} {
		if re.MatchString(host) {
			t.Errorf("%s must not be matched as a preview", host)
		}
	}
}
