package ui

import (
	"net/http"
)

// recentReleases is how many releases the overview lists.
const recentReleases = 3

// trimHistory keeps the latest releases for the overview.
func (d *siteData) trimHistory() {
	if len(d.Deploys) > recentReleases {
		d.Deploys, d.More = d.Deploys[:recentReleases], true
	}
}

// sitePage is the overview: addresses, release, publishing and a summary of
// what is attached to the site.
func (s *server) sitePage(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	data, err := s.loadSite(r, rc)
	if err == nil {
		data.Forms, err = s.formsSummary(r, rc, data.V.Site.ID)
	}
	if err == nil {
		_, data.Data, err = s.svc.ListCollections(r.Context(), rc.actor, data.V.Site.ID)
	}
	if err == nil {
		eps, perr := s.svc.ListProxyEndpoints(r.Context(), rc.actor, data.V.Site.ID)
		data.Proxies, err = len(eps), perr
	}
	if err != nil {
		s.failPage(w, r, rc, err)
		return
	}
	data.trimHistory()
	s.sitePageOf(w, r, rc, http.StatusOK, "site", secOverview, "", data.V, data)
}

// historyFragment re-renders the release list after an upload; ?recent=1
// gives the overview's short list.
func (s *server) historyFragment(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	data, err := s.loadSite(r, rc)
	if err != nil {
		s.failPage(w, r, rc, err)
		return
	}
	if r.URL.Query().Get("recent") == "1" {
		data.trimHistory()
	}
	s.fragment(w, r, rc, "history_fragment", data)
}

// versionsPage lists releases with rollback, schedules, copies and screenshots.
func (s *server) versionsPage(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	data, err := s.loadSite(r, rc)
	if err == nil {
		data.Workflow, err = s.workflowView(r, rc, data)
	}
	if err != nil {
		s.failPage(w, r, rc, err)
		return
	}
	s.sitePageOf(w, r, rc, http.StatusOK, "site_versions", secVersions, "site.nav.versions", data.V, data)
}

// statsPage shows visits and the analytics counters.
func (s *server) statsPage(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	v, err := s.svc.GetSite(r.Context(), rc.actor, r.PathValue("ref"))
	data := siteData{V: v}
	if err == nil {
		data.Stats, err = s.siteStatsView(r, rc, v.Site.ID)
	}
	if err == nil {
		data.Counters, err = s.svc.GetSiteCounters(r.Context(), rc.actor, v.Site.ID)
	}
	if err != nil {
		s.failPage(w, r, rc, err)
		return
	}
	s.sitePageOf(w, r, rc, http.StatusOK, "site_stats", secStats, "site.nav.stats", v, data)
}

// accessPage holds the password, indexing, expiry and own domains.
func (s *server) accessPage(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	v, err := s.svc.GetSite(r.Context(), rc.actor, r.PathValue("ref"))
	data := siteData{V: v}
	if err == nil {
		data.Access = s.accessView(v.Site)
		data.Domains, err = s.loadSiteDomains(r, rc, v)
	}
	if err != nil {
		s.failPage(w, r, rc, err)
		return
	}
	s.sitePageOf(w, r, rc, http.StatusOK, "site_access", secAccess, "site.nav.access", v, data)
}

// siteSettingsPage holds the name and address, workspace, publish optimisation
// and deletion.
func (s *server) siteSettingsPage(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	v, err := s.svc.GetSite(r.Context(), rc.actor, r.PathValue("ref"))
	data := siteData{V: v, Domain: s.svc.Config().BaseDomain}
	if err == nil {
		data.Publish, err = s.svc.GetPublishSettings(r.Context(), rc.actor, v.Site.ID)
	}
	if err == nil {
		data.Space, err = s.siteWorkspaceView(r, rc, v)
	}
	if err != nil {
		s.failPage(w, r, rc, err)
		return
	}
	s.sitePageOf(w, r, rc, http.StatusOK, "site_settings", secSettings, "site.nav.settings", v, data)
}
