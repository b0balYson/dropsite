package ui

import (
	"net/http"
	"strings"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/service"
)

// ExpiryWarning is how long before expiry the interface warns the owner.
const ExpiryWarning = 3 * 24 * time.Hour

// accessView is what the "Access" block of the site screen shows.
type accessView struct {
	ExpiresDay string // value of the date input: the last day the site works
	MinDay     string // earliest selectable day (today, UTC)
	Soon       bool   // expires within ExpiryWarning
}

func (s *server) accessView(site domain.Site) accessView {
	now := s.svc.Now()
	v := accessView{MinDay: now.Format(dateLayout)}
	if site.ExpiresAt != nil {
		// The expiry is the first instant after the chosen day.
		v.ExpiresDay = site.ExpiresAt.UTC().Add(-time.Nanosecond).Format(dateLayout)
		v.Soon = site.ExpiresAt.Sub(now) <= ExpiryWarning
	}
	return v
}

func (s *server) expiringSoon(sites []service.SiteSummaryView) map[string]bool {
	now := s.svc.Now()
	out := map[string]bool{}
	for _, v := range sites {
		if v.ExpiresAt != nil && v.ExpiresAt.Sub(now) <= ExpiryWarning {
			out[v.ID] = true
		}
	}
	return out
}

// parseExpiryDay turns the date input into the expiry instant: the end of
// that day in UTC. An empty value means "no expiry".
func parseExpiryDay(v string) (*time.Time, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil, true
	}
	d, err := time.Parse(dateLayout, v)
	if err != nil {
		return nil, false
	}
	end := d.UTC().Add(24 * time.Hour)
	return &end, true
}

// accessUpdate builds an update containing only what the form changes.
func accessUpdate(r *http.Request, cur domain.Site) (service.SiteUpdate, bool) {
	var u service.SiteUpdate
	if noindex := r.PostFormValue("noindex") == "1"; noindex != cur.Noindex {
		u.Noindex = &noindex
	}
	protect := r.PostFormValue("protected") == "1"
	switch password := r.PostFormValue("password"); {
	case protect && password != "":
		u.Password = &password
	case protect != cur.Protected:
		u.Protected = &protect
	}
	at, ok := parseExpiryDay(r.PostFormValue("expires"))
	if !ok {
		return u, false
	}
	if !sameTime(at, cur.ExpiresAt) {
		u.Expiry = &service.Expiry{At: at}
	}
	return u, true
}

func sameTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}

// siteAccess saves the "Access" block: indexing, password and expiry.
func (s *server) siteAccess(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	ref := r.PathValue("ref")
	cur, err := s.svc.GetSite(r.Context(), rc.actor, ref)
	if err != nil {
		s.fail(w, r, rc, err, "/")
		return
	}
	upd, ok := accessUpdate(r, cur.Site)
	if !ok {
		s.setFlash(w, r, flashError, rc.loc.T("access.expires.bad"))
		redirect(w, r, sectionPath(cur.Site.ID, secAccess))
		return
	}
	v, err := s.svc.UpdateSite(r.Context(), rc.actor, cur.Site.ID, upd)
	if err != nil {
		s.fail(w, r, rc, err, sectionPath(cur.Site.ID, secAccess))
		return
	}
	s.setFlash(w, r, flashOK, rc.loc.T("access.saved"))
	redirect(w, r, sectionPath(v.Site.ID, secAccess))
}
