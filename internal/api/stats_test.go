package api

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/b0balYson/dropsite/internal/config"
	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/testenv"
)

func TestSiteStatsEndpoint(t *testing.T) {
	logFile := filepath.Join(t.TempDir(), "sites-access.log")
	h := newHarness(t, testenv.WithConfig(func(c *config.Config) { c.StatsLogFile = logFile }))
	_, tok := h.user("bob@pp.test")
	_, otherTok := h.user("eve@pp.test")
	site := h.createSite(tok, "Promo", "promo")

	line := fmt.Sprintf(`{"ts":%d,"request":{"client_ip":"198.51.100.1","method":"GET","host":"promo.pp.test",`+
		`"uri":"/","headers":{"User-Agent":["Mozilla/5.0 Firefox/132.0"],"Referer":["https://t.me/x"]}},`+
		`"status":200,"resp_headers":{"Content-Type":["text/html"]}}`+"\n", testenv.Start.Unix())
	if err := os.WriteFile(logFile, []byte(line+line), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := h.env.Svc.CollectStats(context.Background()); err != nil {
		t.Fatal(err)
	}

	rec := h.do(http.MethodGet, "/api/v1/sites/promo/stats?days=7", tok, nil)
	wantStatus(t, rec, http.StatusOK)
	st := decodeAs[siteStatsDTO](t, rec)
	if st.SiteID != site.ID || st.From != "2026-11-06" || st.To != "2026-11-12" || len(st.Days) != 7 {
		t.Fatalf("period %+v", st)
	}
	if last := st.Days[6]; last != (statsDayDTO{Date: "2026-11-12", Views: 2, Visitors: 1}) {
		t.Fatalf("today %+v", last)
	}
	if st.Totals != (statsTotalsDTO{Views: 2, Visitors: 1}) || len(st.TopPages) != 1 ||
		st.TopReferrers[0] != (statsCountDTO{Name: "t.me", Views: 2}) {
		t.Fatalf("stats %+v", st)
	}
	// Default period is 30 days.
	if st := decodeAs[siteStatsDTO](t, h.do(http.MethodGet, "/api/v1/sites/"+site.ID+"/stats", tok, nil)); len(st.Days) != 30 {
		t.Fatalf("default days %d", len(st.Days))
	}
	// An admin sees every site.
	wantStatus(t, h.do(http.MethodGet, "/api/v1/sites/promo/stats", h.adminToken, nil), http.StatusOK)

	for _, bad := range []string{"0", "401", "abc", "-1"} {
		wantError(t, h.do(http.MethodGet, "/api/v1/sites/promo/stats?days="+bad, tok, nil), http.StatusBadRequest, domain.CodeInvalidInput)
	}
	for _, ref := range []string{"promo", site.ID, "missing"} {
		rec := h.do(http.MethodGet, "/api/v1/sites/"+ref+"/stats", otherTok, nil)
		if rec.Code != http.StatusNotFound || rec.Body.String() != notFoundBody {
			t.Errorf("%s: %d %s", ref, rec.Code, rec.Body.String())
		}
	}
}
