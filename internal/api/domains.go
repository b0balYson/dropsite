package api

import (
	"net/http"
	"net/netip"

	"github.com/b0balYson/dropsite/internal/dnscheck"
	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/service"
)

// domainDTO is an external domain with its site and owner (admin endpoints).
type domainDTO struct {
	hostnameDTO
	SiteID     string `json:"site_id"`
	SiteTitle  string `json:"site_title"`
	OwnerID    string `json:"owner_id"`
	OwnerEmail string `json:"owner_email"`
}

type dnsCheckDTO struct {
	Hostname  string   `json:"hostname"`
	Verdict   string   `json:"verdict"`
	Addresses []string `json:"addresses"`
	CNAME     *string  `json:"cname"`
	Expected  []string `json:"expected"`
}

func toDomain(d service.DomainView) domainDTO {
	return domainDTO{
		hostnameDTO: toHostname(d.Hostname), SiteID: d.Hostname.SiteID, SiteTitle: d.SiteTitle,
		OwnerID: idString(d.OwnerID), OwnerEmail: d.OwnerEmail,
	}
}

func toDomains(rows []service.DomainView) []domainDTO {
	out := make([]domainDTO, 0, len(rows))
	for _, r := range rows {
		out = append(out, toDomain(r))
	}
	return out
}

func addrStrings(as []netip.Addr) []string {
	out := make([]string, 0, len(as))
	for _, a := range as {
		out = append(out, a.String())
	}
	return out
}

func toDNSCheck(r dnscheck.Result) dnsCheckDTO {
	return dnsCheckDTO{
		Hostname: r.Host, Verdict: string(r.Verdict), Addresses: addrStrings(r.Addresses),
		CNAME: optional(r.CNAME), Expected: addrStrings(r.Expected),
	}
}

func (a *api) listHostnames(w http.ResponseWriter, r *http.Request) error {
	hosts, err := a.svc.ListHostnames(r.Context(), actorFrom(r.Context()), r.PathValue("ref"))
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, newList(toHostnames(hosts), len(hosts)))
	return nil
}

type addHostnameRequest struct {
	Hostname string `json:"hostname"`
}

func (a *api) addHostname(w http.ResponseWriter, r *http.Request) error {
	var req addHostnameRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return err
	}
	h, err := a.svc.AddDomain(r.Context(), actorFrom(r.Context()), r.PathValue("ref"), req.Hostname)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, toHostname(h))
	return nil
}

func (a *api) removeHostname(w http.ResponseWriter, r *http.Request) error {
	err := a.svc.RemoveDomain(r.Context(), actorFrom(r.Context()), r.PathValue("ref"), r.PathValue("hostname"))
	if err != nil {
		return err
	}
	noContent(w)
	return nil
}

// domainSetupDTO tells the owner which DNS records point a domain at a site.
type domainSetupDTO struct {
	SiteID      string   `json:"site_id"`
	CNAMETarget string   `json:"cname_target"`
	ServerIPs   []string `json:"server_ips"`
	MaxDomains  int      `json:"max_domains"`
}

func (a *api) domainSetup(w http.ResponseWriter, r *http.Request) error {
	ctx, actor := r.Context(), actorFrom(r.Context())
	v, err := a.svc.GetSite(ctx, actor, r.PathValue("ref"))
	if err != nil {
		return err
	}
	setup, err := a.svc.DomainSetup(ctx, actor, v.Site.ID)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, domainSetupDTO{SiteID: v.Site.ID, CNAMETarget: setup.CNAMETarget,
		ServerIPs: addrStrings(setup.ServerIPs), MaxDomains: setup.MaxDomains})
	return nil
}

func (a *api) listDomains(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	p, err := page(q)
	if err != nil {
		return err
	}
	f := service.DomainFilter{Query: q.Get("q"), Page: p}
	switch st := q.Get("status"); st {
	case "", "all":
	case string(domain.HostPending), string(domain.HostActive), string(domain.HostRejected):
		f.Status = domain.HostStatus(st)
	default:
		return invalidField("status", "status must be pending, active, rejected or all")
	}
	rows, total, err := a.svc.ListDomains(r.Context(), actorFrom(r.Context()), f)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, newList(toDomains(rows), total))
	return nil
}

func (a *api) approveDomain(w http.ResponseWriter, r *http.Request) error {
	d, err := a.svc.ApproveDomain(r.Context(), actorFrom(r.Context()), r.PathValue("hostname"))
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, toDomain(d))
	return nil
}

type rejectDomainRequest struct {
	Reason string `json:"reason"`
}

func (a *api) rejectDomain(w http.ResponseWriter, r *http.Request) error {
	var req rejectDomainRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return err
	}
	d, err := a.svc.RejectDomain(r.Context(), actorFrom(r.Context()), r.PathValue("hostname"), req.Reason)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, toDomain(d))
	return nil
}

func (a *api) checkDomainDNS(w http.ResponseWriter, r *http.Request) error {
	res, err := a.svc.CheckDomainDNS(r.Context(), actorFrom(r.Context()), r.PathValue("hostname"))
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, toDNSCheck(res))
	return nil
}
