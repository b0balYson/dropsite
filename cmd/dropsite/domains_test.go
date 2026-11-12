package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDomainsAddListRemove(t *testing.T) {
	e := newTestEnv(t)
	e.login()
	s := e.srv.addSite("Promo", "promo-may", 1)

	r := e.run("domains", "add", "promo-may", "Promo.Example.RU")
	expectCode(t, r, 0)
	expectContains(t, "stdout", r.stdout, "promo.example.ru", "pending")
	expectContains(t, "stderr", r.stderr, "CNAME", s.ID+".sites.test", "administrator")

	s.Domains = append(s.Domains, map[string]any{
		"hostname": "old.example.org", "kind": "external", "status": "rejected", "reason": "not\tours",
	})
	r = e.run("domains", "promo-may")
	expectCode(t, r, 0)
	expectContains(t, "stdout", r.stdout, "HOSTNAME", "promo-may.sites.test", "promo.example.ru", "pending", "rejected: not ours")

	r = e.run("domains", "promo-may", "--json")
	expectCode(t, r, 0)
	var items []map[string]any
	if err := json.Unmarshal([]byte(r.stdout), &items); err != nil || len(items) != 4 || items[2]["status"] != "pending" {
		t.Fatalf("json: %v\n%s", err, r.stdout)
	}

	r = e.run("domains", "remove", "promo-may", "promo.example.ru")
	expectCode(t, r, 0)
	expectContains(t, "stdout", r.stdout, "Removed promo.example.ru")
	r = e.run("domains", "remove", "promo-may", "promo.example.ru")
	expectCode(t, r, 1)

	r = e.run("domains", "add", "promo-may", "nodot", "--json")
	expectCode(t, r, 1)
	expectContains(t, "stderr", r.stderr, "domain_invalid")
}

func TestDomainsJSONAdd(t *testing.T) {
	e := newTestEnv(t)
	e.login()
	e.srv.addSite("Promo", "promo-may", 0)
	r := e.run("domains", "add", "promo-may", "promo.example.ru", "--json")
	expectCode(t, r, 0)
	var h map[string]any
	if err := json.Unmarshal([]byte(r.stdout), &h); err != nil || h["status"] != "pending" {
		t.Fatalf("json: %v\n%s", err, r.stdout)
	}
	if strings.Contains(r.stdout, "CNAME") {
		t.Fatal("JSON output must not contain hints")
	}
}

// A site whose slug is "add" stays reachable: one argument always names a site.
func TestDomainsSiteNamedLikeSubcommand(t *testing.T) {
	e := newTestEnv(t)
	e.login()
	e.srv.addSite("Add", "add", 0)
	r := e.run("domains", "add")
	expectCode(t, r, 0)
	expectContains(t, "stdout", r.stdout, "add.sites.test")
	expectCode(t, e.run("domains"), 2)
	expectCode(t, e.run("domains", "remove", "add"), 2)
	expectCode(t, e.run("domains", "drop", "add", "x.example.ru"), 2)
}
