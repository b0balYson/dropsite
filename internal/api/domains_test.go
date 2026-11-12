package api

import (
	"net/http"
	"slices"
	"testing"

	"github.com/b0balYson/dropsite/internal/testenv"
)

func TestOwnerDomains(t *testing.T) {
	h := newHarness(t)
	_, owner := h.user("owner@pp.test")
	_, other := h.user("other@pp.test")
	site := h.createSite(owner, "Promo", "promo")
	wantStatus(t, h.deploy(site.ID, owner, testenv.Zip(t, index("v1"))), http.StatusCreated)
	base := "/api/v1/sites/promo/hostnames"

	rec := h.do(http.MethodPost, base, owner, map[string]string{"hostname": "Promo.Example.RU"})
	wantStatus(t, rec, http.StatusCreated)
	added := decodeAs[hostnameDTO](t, rec)
	if added.Hostname != "promo.example.ru" || added.Kind != "external" || added.Status != "pending" ||
		added.Verified || added.Reason != nil || added.ReviewedAt != nil {
		t.Fatalf("added %+v", added)
	}

	hosts := decodeAs[list[hostnameDTO]](t, h.do(http.MethodGet, base, owner, nil))
	if len(hosts.Items) != 3 || hosts.Items[0].Kind != "slug" || hosts.Items[2].Status != "pending" || hosts.Items[0].Status != "active" {
		t.Fatalf("hosts %+v", hosts)
	}
	s := decodeAs[siteDTO](t, h.do(http.MethodGet, "/api/v1/sites/promo", owner, nil))
	if len(s.Hostnames) != 3 || len(s.URLs) != 2 {
		t.Fatalf("site %+v", s)
	}

	otherSite := h.createSite(other, "Other", "")
	wantError(t, h.do(http.MethodPost, "/api/v1/sites/"+otherSite.ID+"/hostnames", other,
		map[string]string{"hostname": "promo.example.ru"}), http.StatusConflict, "domain_taken")
	wantError(t, h.do(http.MethodPost, base, owner, map[string]string{"hostname": "x.pp.test"}),
		http.StatusBadRequest, "domain_forbidden")
	wantError(t, h.do(http.MethodPost, base, owner, map[string]string{"hostname": "203.0.113.7"}),
		http.StatusBadRequest, "domain_invalid")
	wantError(t, h.do(http.MethodPost, base, owner, map[string]string{}), http.StatusBadRequest, "domain_invalid")

	// Only external domains can be removed here.
	wantError(t, h.do(http.MethodDelete, base+"/promo.pp.test", owner, nil), http.StatusNotFound, "not_found")
	wantStatus(t, h.do(http.MethodDelete, base+"/promo.example.ru", owner, nil), http.StatusNoContent)
	wantError(t, h.do(http.MethodDelete, base+"/promo.example.ru", owner, nil), http.StatusNotFound, "not_found")
}

func TestDomainLimitOverAPI(t *testing.T) {
	h := newHarness(t, testenv.WithEnv("MAX_DOMAINS_PER_SITE", "1"))
	_, owner := h.user("owner@pp.test")
	site := h.createSite(owner, "Promo", "")
	path := "/api/v1/sites/" + site.ID + "/hostnames"
	wantStatus(t, h.do(http.MethodPost, path, owner, map[string]string{"hostname": "a.example.ru"}), http.StatusCreated)
	e := wantError(t, h.do(http.MethodPost, path, owner, map[string]string{"hostname": "b.example.ru"}),
		http.StatusForbidden, "domain_limit_reached")
	if e.Details["limit"] != float64(1) {
		t.Fatalf("details %+v", e.Details)
	}
}

func TestAdminDomains(t *testing.T) {
	h := newHarness(t)
	_, owner := h.user("owner@pp.test")
	site := h.createSite(owner, "Promo", "")
	wantStatus(t, h.deploy(site.ID, owner, testenv.Zip(t, index("v1"))), http.StatusCreated)
	for _, d := range []string{"promo.example.ru", "bad.example.ru"} {
		wantStatus(t, h.do(http.MethodPost, "/api/v1/sites/"+site.ID+"/hostnames", owner, map[string]string{"hostname": d}),
			http.StatusCreated)
	}
	h.env.DNS.Set("promo.example.ru", testenv.ServerIP)
	h.env.DNS.Set("bad.example.ru", "198.51.100.1")

	pending := decodeAs[list[domainDTO]](t, h.do(http.MethodGet, "/api/v1/admin/domains?status=pending", h.adminToken, nil))
	if pending.Total != 2 || pending.Items[0].OwnerEmail != "owner@pp.test" || pending.Items[0].SiteID != site.ID ||
		pending.Items[0].SiteTitle != "Promo" {
		t.Fatalf("pending %+v", pending)
	}
	wantError(t, h.do(http.MethodGet, "/api/v1/admin/domains?status=weird", h.adminToken, nil), http.StatusBadRequest, "invalid_input")

	check := decodeAs[dnsCheckDTO](t, h.do(http.MethodPost, "/api/v1/admin/domains/promo.example.ru/dns-check", h.adminToken, nil))
	if check.Verdict != "points_here" || !slices.Equal(check.Addresses, []string{testenv.ServerIP}) ||
		!slices.Equal(check.Expected, []string{testenv.ServerIP}) || check.CNAME != nil {
		t.Fatalf("check %+v", check)
	}
	check = decodeAs[dnsCheckDTO](t, h.do(http.MethodPost, "/api/v1/admin/domains/bad.example.ru/dns-check", h.adminToken, nil))
	if check.Verdict != "points_elsewhere" {
		t.Fatalf("check %+v", check)
	}

	rec := h.do(http.MethodPost, "/api/v1/admin/domains/promo.example.ru/approve", h.adminToken, nil)
	wantStatus(t, rec, http.StatusOK)
	if d := decodeAs[domainDTO](t, rec); d.Status != "active" || !d.Verified || d.ReviewedAt == nil {
		t.Fatalf("approved %+v", d)
	}
	if got, _ := h.env.Served("promo.example.ru", "index.html"); got != "v1" {
		t.Fatalf("served %q", got)
	}

	wantError(t, h.do(http.MethodPost, "/api/v1/admin/domains/bad.example.ru/reject", h.adminToken, map[string]string{}),
		http.StatusBadRequest, "domain_reason_invalid")
	rec = h.do(http.MethodPost, "/api/v1/admin/domains/bad.example.ru/reject", h.adminToken, map[string]string{"reason": "points elsewhere"})
	wantStatus(t, rec, http.StatusOK)
	if d := decodeAs[domainDTO](t, rec); d.Status != "rejected" || d.Reason == nil || *d.Reason != "points elsewhere" {
		t.Fatalf("rejected %+v", d)
	}
	hosts := decodeAs[list[hostnameDTO]](t, h.do(http.MethodGet, "/api/v1/sites/"+site.ID+"/hostnames", owner, nil))
	if bad := hosts.Items[1]; bad.Hostname != "bad.example.ru" || *bad.Reason != "points elsewhere" {
		t.Fatalf("owner sees %+v", hosts.Items)
	}
	all := decodeAs[list[domainDTO]](t, h.do(http.MethodGet, "/api/v1/admin/domains", h.adminToken, nil))
	if all.Total != 2 {
		t.Fatalf("all %+v", all)
	}
	wantError(t, h.do(http.MethodPost, "/api/v1/admin/domains/missing.example.ru/approve", h.adminToken, nil),
		http.StatusNotFound, "not_found")
}
