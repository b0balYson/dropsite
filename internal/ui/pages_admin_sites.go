package ui

import (
	"net/http"
	"strings"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/service"
)

type adminSiteRow struct {
	service.SiteSummaryView
	HasApex bool
}

type allSitesData struct {
	Sites  []adminSiteRow
	Query  string
	Pager  pager
	Domain string
	Emails []string
	Back   string
}

func (s *server) allSitesPage(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	n, page := pageParam(r)
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	sites, total, err := s.svc.ListAllSites(r.Context(), rc.actor, q, page)
	if err != nil {
		s.failPage(w, r, rc, err)
		return
	}
	users, _, err := s.svc.ListUsers(r.Context(), rc.actor, service.UserFilter{
		Status: domain.UserActive, Page: service.Page{Limit: service.MaxPageSize},
	})
	if err != nil {
		s.failPage(w, r, rc, err)
		return
	}
	data := allSitesData{
		Query: q, Pager: newPager("/admin/sites", r.URL.Query(), n, total),
		Domain: s.svc.Config().BaseDomain, Back: r.URL.RequestURI(),
	}
	for _, v := range sites {
		row := adminSiteRow{SiteSummaryView: v}
		for _, h := range v.Hostnames {
			row.HasApex = row.HasApex || h.Kind == domain.HostApex
		}
		data.Sites = append(data.Sites, row)
	}
	for _, u := range users {
		data.Emails = append(data.Emails, u.Email)
	}
	s.page(w, r, rc, http.StatusOK, "admin_sites", "allsites", "allsites.title", data)
}

func adminBack(r *http.Request) string {
	back := safeNext(r.PostFormValue("back"))
	if !strings.HasPrefix(back, "/admin/sites") {
		return "/admin/sites"
	}
	return back
}

func (s *server) setApex(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	back := adminBack(r)
	if r.PostFormValue("confirm") != "yes" {
		s.setFlash(w, r, flashError, rc.loc.T("allsites.apex.unconfirmed"))
		redirect(w, r, back)
		return
	}
	v, err := s.svc.SetApex(r.Context(), rc.actor, r.PathValue("ref"))
	if err != nil {
		s.fail(w, r, rc, err, back)
		return
	}
	s.setFlash(w, r, flashOK, rc.loc.T("allsites.apex.done", "title", v.Site.Title, "domain", s.svc.Config().BaseDomain))
	redirect(w, r, back)
}

func (s *server) clearApex(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	back := adminBack(r)
	if r.PostFormValue("confirm") != "yes" {
		s.setFlash(w, r, flashError, rc.loc.T("allsites.apex.unconfirmed"))
		redirect(w, r, back)
		return
	}
	v, err := s.svc.ClearApex(r.Context(), rc.actor, r.PathValue("ref"))
	if err != nil {
		s.fail(w, r, rc, err, back)
		return
	}
	s.setFlash(w, r, flashOK, rc.loc.T("allsites.apex.cleared", "title", v.Site.Title))
	redirect(w, r, back)
}

func (s *server) transferSite(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	back := adminBack(r)
	email := strings.TrimSpace(r.PostFormValue("email"))
	owner, ok, err := s.findActiveUser(r, rc, email)
	if err != nil {
		s.fail(w, r, rc, err, back)
		return
	}
	if !ok {
		s.setFlash(w, r, flashError, rc.loc.T("allsites.transfer.nouser", "email", email))
		redirect(w, r, back)
		return
	}
	v, err := s.svc.TransferSite(r.Context(), rc.actor, r.PathValue("ref"), owner.ID)
	if err != nil {
		s.fail(w, r, rc, err, back)
		return
	}
	s.setFlash(w, r, flashOK, rc.loc.T("allsites.transfer.done", "title", v.Site.Title, "email", owner.Email))
	redirect(w, r, back)
}

// findActiveUser looks up an active user by exact (case-insensitive) email.
func (s *server) findActiveUser(r *http.Request, rc *reqCtx, email string) (domain.User, bool, error) {
	if email == "" {
		return domain.User{}, false, nil
	}
	users, _, err := s.svc.ListUsers(r.Context(), rc.actor, service.UserFilter{
		Query: email, Status: domain.UserActive, Page: service.Page{Limit: service.MaxPageSize},
	})
	if err != nil {
		return domain.User{}, false, err
	}
	for _, u := range users {
		if strings.EqualFold(u.Email, email) {
			return u.User, true, nil
		}
	}
	return domain.User{}, false, nil
}
