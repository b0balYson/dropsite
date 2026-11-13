package ui

import (
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strings"

	"golang.org/x/net/idna"

	"github.com/b0balYson/dropsite/internal/dnscheck"
	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/service"
)

// domainRow is an external domain with its display name and site URL.
type domainRow struct {
	H       domain.Hostname
	Display string // Unicode form of an IDN, "" when equal to the name
	URL     string
}

func (s *server) domainRow(h domain.Hostname) domainRow {
	r := domainRow{H: h, URL: s.svc.SiteURL(h.Hostname)}
	if u, err := idna.Display.ToUnicode(h.Hostname); err == nil && u != h.Hostname {
		r.Display = u
	}
	return r
}

// siteDomains is the "own domains" block of the site page.
type siteDomains struct {
	List    []domainRow
	Count   int
	Setup   service.DomainSetup
	AtLimit bool
}

func (s *server) loadSiteDomains(r *http.Request, rc *reqCtx, v service.SiteView) (siteDomains, error) {
	setup, err := s.svc.DomainSetup(r.Context(), rc.actor, v.Site.ID)
	if err != nil {
		return siteDomains{}, err
	}
	d := siteDomains{Setup: setup}
	for _, h := range v.Hostnames {
		if h.Kind == domain.HostExternal {
			d.List = append(d.List, s.domainRow(h))
		}
	}
	d.Count = len(d.List)
	d.AtLimit = d.Count >= setup.MaxDomains
	return d, nil
}

func (s *server) addDomain(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	ref := r.PathValue("ref")
	h, err := s.svc.AddDomain(r.Context(), rc.actor, ref, r.PostFormValue("domain"))
	if err != nil {
		s.fail(w, r, rc, err, sectionPath(ref, secAccess)+"#domains-h")
		return
	}
	s.setFlash(w, r, flashOK, rc.loc.T("domains.added", "domain", h.Hostname))
	redirect(w, r, sectionPath(h.SiteID, secAccess)+"#domains-h")
}

func (s *server) removeDomain(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	ref := r.PathValue("ref")
	name := strings.TrimSpace(r.PostFormValue("domain"))
	if err := s.svc.RemoveDomain(r.Context(), rc.actor, ref, name); err != nil {
		s.fail(w, r, rc, err, sectionPath(ref, secAccess)+"#domains-h")
		return
	}
	s.setFlash(w, r, flashOK, rc.loc.T("domains.removed", "domain", name))
	redirect(w, r, sectionPath(ref, secAccess)+"#domains-h")
}

// statusTabs are the filters of the admin screen, in display order.
var statusTabs = []string{"pending", "active", "rejected", "all"}

type domainTab struct {
	Key     string
	URL     string
	Current bool
}

type adminDomainRow struct {
	service.DomainView
	Row domainRow
}

type adminDomainsData struct {
	Rows    []adminDomainRow
	Status  string
	Tabs    []domainTab
	Query   string
	Pending int
	Pager   pager
	Back    string
}

func (s *server) adminDomainsPage(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	q := r.URL.Query()
	status := q.Get("status")
	if !slices.Contains(statusTabs, status) {
		status = "pending"
	}
	query := strings.TrimSpace(q.Get("q"))
	n, page := pageParam(r)
	f := service.DomainFilter{Query: query, Page: page}
	if status != "all" {
		f.Status = domain.HostStatus(status)
	}
	rows, total, err := s.svc.ListDomains(r.Context(), rc.actor, f)
	if err != nil {
		s.failPage(w, r, rc, err)
		return
	}
	_, pending, err := s.svc.ListDomains(r.Context(), rc.actor, service.DomainFilter{
		Status: domain.HostPending, Page: service.Page{Limit: 1},
	})
	if err != nil {
		s.failPage(w, r, rc, err)
		return
	}
	data := adminDomainsData{
		Status: status, Query: query, Pending: pending,
		Pager: newPager("/admin/domains", q, n, total), Back: r.URL.RequestURI(),
	}
	for _, row := range rows {
		data.Rows = append(data.Rows, adminDomainRow{DomainView: row, Row: s.domainRow(row.Hostname)})
	}
	for _, key := range statusTabs {
		u := "/admin/domains?status=" + key
		if query != "" {
			u += "&q=" + url.QueryEscape(query)
		}
		data.Tabs = append(data.Tabs, domainTab{Key: key, URL: u, Current: key == status})
	}
	s.page(w, r, rc, http.StatusOK, "admin_domains", "domains", "xdomains.title", data)
}

// domainsBack returns a same-site URL of the admin domains screen.
func domainsBack(r *http.Request) string {
	back := safeNext(r.PostFormValue("back"))
	if back != "/admin/domains" && !strings.HasPrefix(back, "/admin/domains?") {
		return "/admin/domains"
	}
	return back
}

func (s *server) approveDomain(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	back := domainsBack(r)
	d, err := s.svc.ApproveDomain(r.Context(), rc.actor, r.PathValue("host"))
	if err != nil {
		s.fail(w, r, rc, err, back)
		return
	}
	s.setFlash(w, r, flashOK, rc.loc.T("xdomains.approved", "domain", d.Hostname.Hostname))
	redirect(w, r, back)
}

func (s *server) rejectDomain(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	back := domainsBack(r)
	d, err := s.svc.RejectDomain(r.Context(), rc.actor, r.PathValue("host"), r.PostFormValue("reason"))
	if err != nil {
		s.fail(w, r, rc, err, back)
		return
	}
	s.setFlash(w, r, flashOK, rc.loc.T("xdomains.rejected", "domain", d.Hostname.Hostname))
	redirect(w, r, back)
}

// dnsView is the lazily loaded DNS check of one domain.
type dnsView struct {
	Host     string
	Verdict  string
	Found    string
	Expected string
	CNAME    string
	Tone     string // ok, error, warn
}

func joinAddrs(as []netip.Addr) string {
	parts := make([]string, 0, len(as))
	for _, a := range as {
		parts = append(parts, a.String())
	}
	return strings.Join(parts, ", ")
}

func (s *server) domainDNS(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	res, err := s.svc.CheckDomainDNS(r.Context(), rc.actor, r.PathValue("host"))
	if err != nil {
		s.failPage(w, r, rc, err)
		return
	}
	v := dnsView{
		Host: res.Host, Verdict: string(res.Verdict), Found: joinAddrs(res.Addresses),
		Expected: joinAddrs(res.Expected), CNAME: res.CNAME, Tone: "warn",
	}
	switch res.Verdict {
	case dnscheck.PointsHere:
		v.Tone = "ok"
	case dnscheck.PointsElsewhere, dnscheck.NoRecords:
		v.Tone = "error"
	}
	s.fragment(w, r, rc, "dns_check", v)
}
