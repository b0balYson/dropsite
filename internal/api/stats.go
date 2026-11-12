package api

import (
	"net/http"
	"strconv"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/service"
)

type statsDayDTO struct {
	Date     string `json:"date"`
	Views    int64  `json:"views"`
	Visitors int64  `json:"visitors"`
}

type statsCountDTO struct {
	Name  string `json:"name"`
	Views int64  `json:"views"`
}

type statsTotalsDTO struct {
	Views    int64 `json:"views"`
	Visitors int64 `json:"visitors"`
}

type siteStatsDTO struct {
	SiteID       string          `json:"site_id"`
	From         string          `json:"from"`
	To           string          `json:"to"`
	Days         []statsDayDTO   `json:"days"`
	Totals       statsTotalsDTO  `json:"totals"`
	TopPages     []statsCountDTO `json:"top_pages"`
	TopReferrers []statsCountDTO `json:"top_referrers"`
}

func toCounts(in []domain.NamedCount) []statsCountDTO {
	out := make([]statsCountDTO, 0, len(in))
	for _, c := range in {
		out = append(out, statsCountDTO{Name: c.Name, Views: c.Views})
	}
	return out
}

func toSiteStats(st service.SiteStats) siteStatsDTO {
	days := make([]statsDayDTO, 0, len(st.Days))
	for _, d := range st.Days {
		days = append(days, statsDayDTO{Date: d.Day, Views: d.Views, Visitors: d.Visitors})
	}
	return siteStatsDTO{
		SiteID: st.SiteID, From: st.From, To: st.To, Days: days,
		Totals:       statsTotalsDTO{Views: st.Views, Visitors: st.Visitors},
		TopPages:     toCounts(st.TopPages),
		TopReferrers: toCounts(st.TopReferrers),
	}
}

func (a *api) siteStats(w http.ResponseWriter, r *http.Request) error {
	days := service.StatsDefaultDays
	if v := r.URL.Query().Get("days"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > service.StatsMaxDays {
			return invalidField("days", "days must be an integer from 1 to "+strconv.Itoa(service.StatsMaxDays))
		}
		days = n
	}
	st, err := a.svc.GetSiteStats(r.Context(), actorFrom(r.Context()), r.PathValue("ref"), days)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, toSiteStats(st))
	return nil
}
