package api

import (
	"net/http"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/service"
)

type counterIDsDTO struct {
	YandexMetricaID   string `json:"yandex_metrica_id"`
	GoogleAnalyticsID string `json:"google_analytics_id"`
}

type countersDTO struct {
	SiteID string        `json:"site_id"`
	Own    counterIDsDTO `json:"own"`
	Global counterIDsDTO `json:"global"`
	// NewDeployID is the release created to apply changed counters to a
	// published site; null when none was needed.
	NewDeployID *string `json:"new_deploy_id"`
}

func toCounterIDs(c domain.Counters) counterIDsDTO {
	return counterIDsDTO{YandexMetricaID: c.MetrikaID, GoogleAnalyticsID: c.GA4ID}
}

func toCounters(siteID string, c service.SiteCounters) countersDTO {
	return countersDTO{SiteID: siteID, Own: toCounterIDs(c.Own), Global: toCounterIDs(c.Global)}
}

func (a *api) getCounters(w http.ResponseWriter, r *http.Request) error {
	ctx, actor := r.Context(), actorFrom(r.Context())
	v, err := a.svc.GetSite(ctx, actor, r.PathValue("ref"))
	if err != nil {
		return err
	}
	c, err := a.svc.GetSiteCounters(ctx, actor, v.Site.ID)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, toCounters(v.Site.ID, c))
	return nil
}

// updateCountersRequest changes only the fields present; "" removes a counter.
type updateCountersRequest struct {
	YandexMetricaID   *string `json:"yandex_metrica_id"`
	GoogleAnalyticsID *string `json:"google_analytics_id"`
}

func (a *api) updateCounters(w http.ResponseWriter, r *http.Request) error {
	var req updateCountersRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return err
	}
	if req.YandexMetricaID == nil && req.GoogleAnalyticsID == nil {
		return invalid("nothing to change: pass yandex_metrica_id and/or google_analytics_id")
	}
	ctx, actor := r.Context(), actorFrom(r.Context())
	v, err := a.svc.GetSite(ctx, actor, r.PathValue("ref"))
	if err != nil {
		return err
	}
	cur, err := a.svc.GetSiteCounters(ctx, actor, v.Site.ID)
	if err != nil {
		return err
	}
	next := cur.Own
	if req.YandexMetricaID != nil {
		next.MetrikaID = *req.YandexMetricaID
	}
	if req.GoogleAnalyticsID != nil {
		next.GA4ID = *req.GoogleAnalyticsID
	}
	d, err := a.svc.SetSiteCounters(ctx, actor, v.Site.ID, next, deploySource(r))
	if err != nil {
		return err
	}
	saved, err := a.svc.GetSiteCounters(ctx, actor, v.Site.ID)
	if err != nil {
		return err
	}
	out := toCounters(v.Site.ID, saved)
	if d != nil {
		out.NewDeployID = &d.ID
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}
