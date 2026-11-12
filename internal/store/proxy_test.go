package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
)

func proxyEndpoint(site, name string) domain.ProxyEndpoint {
	return domain.ProxyEndpoint{
		SiteID: site, Name: name,
		ProxyConfig: domain.ProxyConfig{
			UpstreamURL: "https://api.example.com/v1", AllowedHosts: []string{"api.example.com"},
			Methods: []string{"GET", "POST"}, Paths: []string{"/chat"}, Auth: domain.ProxyAuthBearer, Enabled: true,
		},
		CreatedAt: t0, UpdatedAt: t0,
	}
}

func TestProxyEndpointsCRUD(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	u := mkUser(t, db, "a@x.test", domain.UserActive)
	mkSite(t, db, "site000001", u, "", t0)
	mkSite(t, db, "site000002", u, "", t0)

	id, err := db.CreateProxyEndpoint(ctx, proxyEndpoint("site000001", "ai"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateProxyEndpoint(ctx, proxyEndpoint("site000001", "ai")); !errors.Is(err, errProxyTaken) {
		t.Fatalf("duplicate name: %v", err)
	}
	if _, err := db.CreateProxyEndpoint(ctx, proxyEndpoint("site000002", "ai")); err != nil {
		t.Fatalf("same name on another site: %v", err)
	}
	if _, err := db.CreateProxyEndpoint(ctx, proxyEndpoint("nosuchsite", "ai")); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown site: %v", err)
	}

	got, err := db.GetProxyEndpoint(ctx, "site000001", "ai")
	if err != nil || got.ID != id || !reflect.DeepEqual(got.Methods, []string{"GET", "POST"}) ||
		got.Auth != domain.ProxyAuthBearer || got.SecretSet() || !got.Enabled {
		t.Fatalf("get: %+v %v", got, err)
	}
	if _, err := db.GetProxyEndpoint(ctx, "site000002", "nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}

	at := t0.Add(time.Hour)
	got.SecretEnc, got.SecretUpdatedAt, got.UpdatedAt = "enc:v1:x", &at, at
	got.Paths = nil
	if err := db.UpdateProxyEndpoint(ctx, got); err != nil {
		t.Fatal(err)
	}
	foreign := got
	foreign.SiteID = "site000002"
	if err := db.UpdateProxyEndpoint(ctx, foreign); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("update through another site: %v", err)
	}

	if err := db.RecordProxyUse(ctx, id, ProxyForwarded, 200, at); err != nil {
		t.Fatal(err)
	}
	if err := db.RecordProxyUse(ctx, id, ProxyFailed, 502, at); err != nil {
		t.Fatal(err)
	}
	if err := db.RecordProxyUse(ctx, id, ProxyRejected, 403, at.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	list, err := db.ListProxyEndpoints(ctx, "site000001")
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v %v", list, err)
	}
	e := list[0]
	if e.SecretEnc != "enc:v1:x" || e.SecretUpdatedAt == nil || len(e.Paths) != 0 || e.Usage.Requests != 2 ||
		e.Usage.Errors != 1 || e.Usage.Rejected != 1 || e.Usage.LastStatus != 403 ||
		!e.Usage.LastUsedAt.Equal(at.Add(time.Minute)) {
		t.Fatalf("after update: %+v", e)
	}
	if n, _ := db.CountProxyEndpoints(ctx, "site000001"); n != 1 {
		t.Fatalf("count %d", n)
	}

	if err := db.DeleteProxyEndpoint(ctx, "site000002", id); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("delete through another site: %v", err)
	}
	if err := db.DeleteProxyEndpoint(ctx, "site000001", id); err != nil {
		t.Fatal(err)
	}
}

func TestPurgeProxyEndpoints(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	u := mkUser(t, db, "a@x.test", domain.UserActive)
	mkSite(t, db, "site000001", u, "", t0)
	mkSite(t, db, "site000002", u, "", t0)
	for _, s := range []string{"site000001", "site000002"} {
		if _, err := db.CreateProxyEndpoint(ctx, proxyEndpoint(s, "ai")); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.SoftDeleteSite(ctx, "site000001", t0); err != nil {
		t.Fatal(err)
	}
	if n, err := db.PurgeProxyEndpoints(ctx); err != nil || n != 1 {
		t.Fatalf("purge: %d %v", n, err)
	}
	if n, _ := db.CountProxyEndpoints(ctx, "site000002"); n != 1 {
		t.Fatal("active site lost its endpoint")
	}
}
