package ui

import (
	"net/http"

	"github.com/b0balYson/dropsite/internal/service"
)

// Site sections: every part of a site's settings has a page of its own.
const (
	secOverview = "overview"
	secVersions = "versions"
	secForms    = "forms"
	secData     = "data"
	secStats    = "stats"
	secAccess   = "access"
	secProxy    = "proxy"
	secSettings = "settings"
)

// siteSection is one entry of a site's sub-navigation.
type siteSection struct {
	Key    string
	Suffix string // path after /sites/{id}
	Label  string // i18n key
}

var siteSections = []siteSection{
	{secOverview, "", "site.nav.overview"},
	{secVersions, "/versions", "site.nav.versions"},
	{secForms, "/forms", "site.nav.forms"},
	{secData, "/data", "site.nav.data"},
	{secStats, "/stats", "site.nav.stats"},
	{secAccess, "/access", "site.nav.access"},
	{secProxy, "/proxy", "site.nav.proxy"},
	{secSettings, "/settings", "site.nav.settings"},
}

// sectionPath is the address of a section of a site.
func sectionPath(siteID, section string) string {
	for _, s := range siteSections {
		if s.Key == section {
			return sitePath(siteID) + s.Suffix
		}
	}
	return sitePath(siteID)
}

// siteNav is the site a page belongs to: it fills the shared site header and
// expands the site's group in the sidebar.
type siteNav struct {
	V       service.SiteView
	Section string
	// Sub is true on a page inside a section (a collection, a quality
	// report): the section link then marks the location, not the page.
	Sub  bool
	Soon bool // expires within ExpiryWarning
}

// navLink is one sidebar link. Current is "page" for the page itself,
// "true" for the section that contains it, "" otherwise.
type navLink struct {
	Href    string
	Label   string
	Current string
}

type mainItem struct {
	Nav, Href, Label string
}

var mainNav = []mainItem{
	{"sites", "/", "nav.sites"},
	{"workspaces", "/workspaces", "nav.workspaces"},
	{"connect", "/connect", "nav.connect"},
}

var adminNav = []mainItem{
	{"users", "/admin/users", "nav.users"},
	{"allsites", "/admin/sites", "nav.allsites"},
	{"domains", "/admin/domains", "nav.domains"},
	{"audit", "/admin/audit", "nav.audit"},
	{"settings", "/admin/settings", "nav.settings"},
}

func (v view) links(items []mainItem) []navLink {
	out := make([]navLink, 0, len(items))
	for _, it := range items {
		l := navLink{Href: it.Href, Label: v.L.T(it.Label)}
		if it.Nav == v.Nav {
			l.Current = "page"
			if it.Nav == "sites" && v.Site != nil {
				l.Current = "true"
			}
		}
		out = append(out, l)
	}
	return out
}

// MainLinks are the sidebar's primary sections.
func (v view) MainLinks() []navLink { return v.links(mainNav) }

// StartLink is the first-run checklist entry; nil once it is dismissed.
func (v view) StartLink() *navLink {
	if !v.ShowStart {
		return nil
	}
	l := navLink{Href: "/start", Label: v.L.T("nav.start")}
	if v.Nav == "start" {
		l.Current = "page"
	}
	return &l
}

// AdminLinks are the administration sections; empty for other users.
func (v view) AdminLinks() []navLink {
	if !v.IsAdmin {
		return nil
	}
	return v.links(adminNav)
}

// SiteLinks are the sections of the current site; empty outside a site.
func (v view) SiteLinks() []navLink {
	if v.Site == nil {
		return nil
	}
	out := make([]navLink, 0, len(siteSections))
	for _, s := range siteSections {
		l := navLink{Href: sitePath(v.Site.V.Site.ID) + s.Suffix, Label: v.L.T(s.Label)}
		if s.Key == v.Site.Section {
			l.Current = "page"
			if v.Site.Sub {
				l.Current = "true"
			}
		}
		out = append(out, l)
	}
	return out
}

// SectionLabel names the current page in the compact top bar.
func (v view) SectionLabel() string {
	if v.Site != nil {
		for _, s := range siteSections {
			if s.Key == v.Site.Section {
				return v.L.T(s.Label)
			}
		}
	}
	if v.Nav == "start" {
		return v.L.T("nav.start")
	}
	for _, it := range append(append([]mainItem{}, mainNav...), adminNav...) {
		if it.Nav == v.Nav {
			return v.L.T(it.Label)
		}
	}
	if v.Title != "" {
		return v.L.T(v.Title)
	}
	return ""
}

// SectionKey is the current site section, "" outside a site.
func (v view) SectionKey() string {
	if v.Site == nil {
		return ""
	}
	return v.Site.Section
}

// sitePageOf renders a page of a site section with the shared site header.
func (s *server) sitePageOf(w http.ResponseWriter, r *http.Request, rc *reqCtx, status int,
	name, section, title string, sv service.SiteView, data any) {
	s.sitePageSub(w, r, rc, status, name, section, title, sv, false, data)
}

// sitePageSub is sitePageOf for pages nested inside a section.
func (s *server) sitePageSub(w http.ResponseWriter, r *http.Request, rc *reqCtx, status int,
	name, section, title string, sv service.SiteView, sub bool, data any) {
	v := s.newView(r, rc, "sites", title, data)
	v.Site = &siteNav{V: sv, Section: section, Sub: sub, Soon: s.accessView(sv.Site).Soon}
	s.layout(w, r, status, name, v)
}
