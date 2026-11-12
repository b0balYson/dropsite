//go:build e2e

package e2e

import (
	"fmt"
	"net/http"
	"testing"
)

const browserUA = "Mozilla/5.0 (X11; Linux x86_64; rv:132.0) Gecko/20100101 Firefox/132.0"

// visitStatistics opens pages of a site like a browser and waits until the
// statistics API reports today's views (Caddy writes the access log to the
// logs volume, control reads it every STATS_INTERVAL).
func visitStatistics(t *testing.T, s *stack, owner, stranger *api, siteID, host string) {
	for _, path := range []string{"/", "/about", "/common.txt"} {
		resp, _, err := s.get(host, path, "User-Agent", browserUA, "Referer", "https://news.example.org/item")
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s: %d", path, resp.StatusCode)
		}
	}
	// A robot is not counted.
	if _, _, err := s.get(host, "/", "User-Agent", "Googlebot/2.1"); err != nil {
		t.Fatal(err)
	}
	s.eventually("statistics show today's views", func() error {
		st, body := owner.json(http.MethodGet, "/sites/"+siteID+"/stats?days=1", nil)
		if st != http.StatusOK {
			return fmt.Errorf("stats: %d %v", st, body)
		}
		totals, _ := body["totals"].(map[string]any)
		views, _ := totals["views"].(float64)
		visitors, _ := totals["visitors"].(float64)
		if views < 2 || visitors < 1 {
			return fmt.Errorf("today views %v visitors %v, want at least 2 and 1", views, visitors)
		}
		return nil
	})
	st, body := stranger.json(http.MethodGet, "/sites/"+siteID+"/stats", nil)
	if st != http.StatusNotFound {
		t.Fatalf("another user's stats: %d %v", st, body)
	}
}
