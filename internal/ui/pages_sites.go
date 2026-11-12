package ui

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/service"
)

type siteForm struct {
	Title string
	Slug  string
	Error string
}

type sitesData struct {
	Sites   []service.SiteSummaryView
	Count   int
	Limit   int
	AtLimit bool
	Domain  string
	Form    siteForm
}

func (s *server) sitesPage(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	s.renderSites(w, r, rc, http.StatusOK, siteForm{})
}

func (s *server) renderSites(w http.ResponseWriter, r *http.Request, rc *reqCtx, status int, form siteForm) {
	sites, _, err := s.svc.ListSites(r.Context(), rc.actor, service.Page{Limit: service.MaxPageSize})
	if err != nil {
		s.failPage(w, r, rc, err)
		return
	}
	_, lim, count, err := s.svc.Me(r.Context(), rc.actor)
	if err != nil {
		s.failPage(w, r, rc, err)
		return
	}
	data := sitesData{
		Sites: sites, Count: count, Limit: lim.MaxSitesPerUser, AtLimit: count >= lim.MaxSitesPerUser,
		Domain: s.svc.Config().BaseDomain, Form: form,
	}
	s.page(w, r, rc, status, "sites", "sites", "sites.title", data)
}

func (s *server) createSite(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	form := siteForm{Title: r.PostFormValue("title"), Slug: normSlug(r.PostFormValue("slug"))}
	v, err := s.svc.CreateSite(r.Context(), rc.actor, form.Title, form.Slug)
	if err != nil {
		var status int
		form.Error, status = s.describe(rc, err)
		s.renderSites(w, r, rc, status, form)
		return
	}
	s.setFlash(w, r, flashOK, rc.loc.T("site.created"))
	redirect(w, r, sitePath(v.Site.ID))
}

func normSlug(v string) string { return strings.ToLower(strings.TrimSpace(v)) }

func sitePath(id string) string { return "/sites/" + url.PathEscape(id) }

type slugStatus struct {
	Slug      string
	Host      string
	Available bool
	Message   string
}

func (s *server) slugCheckNew(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	s.slugCheck(w, r, rc, "")
}

func (s *server) slugCheckSite(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	s.slugCheck(w, r, rc, r.PathValue("ref"))
}

func (s *server) slugCheck(w http.ResponseWriter, r *http.Request, rc *reqCtx, ref string) {
	slug := normSlug(r.URL.Query().Get("slug"))
	st := slugStatus{Slug: slug}
	if slug != "" {
		res, err := s.svc.CheckSlugAvailable(r.Context(), rc.actor, ref, slug)
		if err != nil {
			st.Message, _ = s.describe(rc, err)
		} else {
			st.Available = res.Available
			st.Host = slug + "." + s.svc.Config().BaseDomain
			if !res.Available {
				st.Message = rc.loc.T(errorKey(res.Code), "slug", slug)
			}
		}
	}
	s.fragment(w, r, rc, "slug_status", st)
}

type deployRow struct {
	domain.Deploy
	Current    bool
	PreviewURL string // wildcard TLS mode only
}

type siteData struct {
	V        service.SiteView
	Forms    formsSummary
	Deploys  []deployRow
	MaxSize  int64
	Domain   string
	Domains  siteDomains
	Stats    statsView
	Counters service.SiteCounters
}

func (s *server) loadSite(r *http.Request, rc *reqCtx) (siteData, error) {
	ds, v, err := s.svc.ListDeploys(r.Context(), rc.actor, r.PathValue("ref"))
	if err != nil {
		return siteData{}, err
	}
	lim, err := s.svc.Limits(r.Context())
	if err != nil {
		return siteData{}, err
	}
	rows := make([]deployRow, 0, len(ds))
	for _, d := range ds {
		rows = append(rows, deployRow{Deploy: d, Current: v.Current != nil && v.Current.ID == d.ID, PreviewURL: s.svc.PreviewURL(d)})
	}
	doms, err := s.loadSiteDomains(r, rc, v)
	if err != nil {
		return siteData{}, err
	}
	return siteData{V: v, Deploys: rows, MaxSize: lim.MaxSiteBytes, Domain: s.svc.Config().BaseDomain, Domains: doms}, nil
}

func (s *server) sitePage(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	data, err := s.loadSite(r, rc)
	if err == nil {
		data.Stats, err = s.siteStatsView(r, rc, data.V.Site.ID)
	}
	if err == nil {
		data.Counters, err = s.svc.GetSiteCounters(r.Context(), rc.actor, data.V.Site.ID)
	}
	if err == nil {
		data.Forms, err = s.formsSummary(r, rc, data.V.Site.ID)
	}
	if err != nil {
		s.failPage(w, r, rc, err)
		return
	}
	s.page(w, r, rc, http.StatusOK, "site", "sites", "", data)
}

func (s *server) historyFragment(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	data, err := s.loadSite(r, rc)
	if err != nil {
		s.failPage(w, r, rc, err)
		return
	}
	s.fragment(w, r, rc, "history_fragment", data)
}

func (s *server) siteSettings(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	ref := r.PathValue("ref")
	cur, err := s.svc.GetSite(r.Context(), rc.actor, ref)
	if err != nil {
		s.fail(w, r, rc, err, "/")
		return
	}
	// Only changed fields are sent, so an untouched form writes nothing.
	var upd service.SiteUpdate
	if title := r.PostFormValue("title"); strings.TrimSpace(title) != cur.Site.Title {
		upd.Title = &title
	}
	if slug := normSlug(r.PostFormValue("slug")); slug != cur.Slug {
		upd.Slug = &slug
	}
	v, err := s.svc.UpdateSite(r.Context(), rc.actor, cur.Site.ID, upd)
	if err != nil {
		s.fail(w, r, rc, err, sitePath(ref))
		return
	}
	s.setFlash(w, r, flashOK, rc.loc.T("site.saved"))
	redirect(w, r, sitePath(v.Site.ID))
}

func (s *server) rollback(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	ref := r.PathValue("ref")
	res, err := s.svc.Rollback(r.Context(), rc.actor, ref, r.PostFormValue("deploy"))
	if err != nil {
		s.fail(w, r, rc, err, sitePath(ref))
		return
	}
	s.setFlash(w, r, flashOK, rc.loc.T("site.rolledback", "id", res.Deploy.ID))
	redirect(w, r, sitePath(res.Site.Site.ID))
}

func (s *server) deleteSite(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	ref := r.PathValue("ref")
	v, err := s.svc.GetSite(r.Context(), rc.actor, ref)
	if err != nil {
		s.fail(w, r, rc, err, "/")
		return
	}
	if !deleteConfirmed(v, r.PostFormValue("confirm")) {
		s.setFlash(w, r, flashError, rc.loc.T("site.delete.mismatch"))
		redirect(w, r, sitePath(v.Site.ID))
		return
	}
	if err := s.svc.DeleteSite(r.Context(), rc.actor, v.Site.ID); err != nil {
		s.fail(w, r, rc, err, sitePath(v.Site.ID))
		return
	}
	s.setFlash(w, r, flashOK, rc.loc.T("site.deleted", "title", v.Site.Title))
	redirect(w, r, "/")
}

// deleteConfirmed accepts the site title or any of its addresses.
func deleteConfirmed(v service.SiteView, typed string) bool {
	typed = strings.TrimSpace(typed)
	if typed == "" {
		return false
	}
	if typed == v.Site.Title {
		return true
	}
	typed = strings.TrimSuffix(strings.ToLower(displayHost(typed)), "/")
	if typed == v.Site.ID || (v.Slug != "" && typed == v.Slug) {
		return true
	}
	for _, h := range v.Hostnames {
		if typed == h.Hostname {
			return true
		}
	}
	for _, u := range v.URLs {
		if typed == displayHost(u) {
			return true
		}
	}
	return false
}
