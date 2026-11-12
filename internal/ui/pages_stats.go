package ui

import (
	"html/template"
	"net/http"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/service"
)

// statsView is the statistics block of the site screen.
type statsView struct {
	service.SiteStats
	Chart template.HTML
	Empty bool
}

func (s *server) siteStatsView(r *http.Request, rc *reqCtx, siteID string) (statsView, error) {
	st, err := s.svc.GetSiteStats(r.Context(), rc.actor, siteID, service.StatsDefaultDays)
	if err != nil {
		return statsView{}, err
	}
	v := statsView{SiteStats: st, Empty: st.Views == 0}
	if !v.Empty {
		v.Chart = viewsChart(rc.loc, st.Days, "views-chart")
	}
	return v, nil
}

// saveCounters stores the site's analytics counter IDs. For a published site
// this creates a new release with the updated snippet.
func (s *server) saveCounters(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	ref := r.PathValue("ref")
	c := domain.Counters{MetrikaID: r.PostFormValue("metrika_id"), GA4ID: r.PostFormValue("ga4_id")}
	d, err := s.svc.SetSiteCounters(r.Context(), rc.actor, ref, c, domain.SourceUI)
	if err != nil {
		s.fail(w, r, rc, err, sitePath(ref))
		return
	}
	msg := rc.loc.T("counters.saved")
	if d != nil {
		msg = rc.loc.T("counters.released", "id", d.ID)
	}
	s.setFlash(w, r, flashOK, msg)
	redirect(w, r, sitePath(ref))
}
