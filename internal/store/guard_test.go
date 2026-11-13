package store

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
)

func TestProxyClassTemplateAndBudget(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	u := mkUser(t, db, "a@x.test", domain.UserActive)
	mkSite(t, db, "site000001", u, "", t0)

	legacy, err := db.CreateProxyEndpoint(ctx, proxyEndpoint("site000001", "old"))
	if err != nil {
		t.Fatal(err)
	}
	e := proxyEndpoint("site000001", "chat")
	e.Class = domain.ProxyClassAI
	e.Template = &domain.ProxyTemplate{Method: "POST", Path: "/chat", Body: json.RawMessage(`{"q":"{{text}}"}`),
		Fields: map[string]domain.ProxyField{"text": {Type: "string", MaxLength: 100, Required: true}}, Response: "a.b"}
	e.Limits = domain.ProxyLimits{PerMinute: 5, DailyBudget: 2, SessionQuota: 3}
	id, err := db.CreateProxyEndpoint(ctx, e)
	if err != nil {
		t.Fatal(err)
	}
	got, err := db.GetProxyEndpoint(ctx, "site000001", "chat")
	if err != nil || got.Class != domain.ProxyClassAI || got.Limits != e.Limits ||
		!reflect.DeepEqual(got.Template.Fields, e.Template.Fields) || string(got.Template.Body) != `{"q":"{{text}}"}` {
		t.Fatalf("get: %+v %v", got, err)
	}
	old, _ := db.GetProxyEndpoint(ctx, "site000001", "old")
	if old.Class != domain.ProxyClassTransparent || old.Template != nil {
		t.Fatalf("legacy: %+v", old)
	}
	got.Class, got.Template = domain.ProxyClassRead, nil
	if err := db.UpdateProxyEndpoint(ctx, got); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.GetProxyEndpoint(ctx, "site000001", "chat"); got.Class != domain.ProxyClassRead || got.Template != nil {
		t.Fatalf("update: %+v", got)
	}

	for want := 1; want <= 3; want++ {
		if n, err := db.AddProxyDailyCall(ctx, id, "2026-11-13"); err != nil || n != want {
			t.Fatalf("call %d: %d %v", want, n, err)
		}
	}
	if n, _ := db.AddProxyDailyCall(ctx, legacy, "2026-11-12"); n != 1 {
		t.Fatal("other day")
	}
	calls, err := db.ProxyDailyCalls(ctx, "site000001", "2026-11-13")
	if err != nil || calls[id] != 3 || calls[legacy] != 0 {
		t.Fatalf("daily: %v %v", calls, err)
	}
	if ok, err := db.MarkProxyBudgetNotified(ctx, id, "2026-11-13"); !ok || err != nil {
		t.Fatalf("first notify: %v %v", ok, err)
	}
	if ok, _ := db.MarkProxyBudgetNotified(ctx, id, "2026-11-13"); ok {
		t.Fatal("notified twice")
	}
	if err := db.DeleteProxyDailyBefore(ctx, "2026-11-13"); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteProxyEndpoint(ctx, "site000001", id); err != nil {
		t.Fatal(err)
	}
	if calls, _ := db.ProxyDailyCalls(ctx, "site000001", "2026-11-13"); len(calls) != 0 {
		t.Fatalf("usage of a deleted endpoint: %v", calls)
	}
}

func TestBans(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	b := domain.IPBan{Key: "2001:db8::/64", Level: 1, Reason: "rate", Addresses: []string{"2001:db8::1"},
		CreatedAt: t0, ExpiresAt: t0.Add(15 * time.Minute)}
	if err := db.PutBan(ctx, b); err != nil {
		t.Fatal(err)
	}
	b.Level, b.ExpiresAt = 2, t0.Add(time.Hour)
	if err := db.PutBan(ctx, b); err != nil {
		t.Fatal(err)
	}
	if err := db.PutBan(ctx, domain.IPBan{Key: "203.0.113.7", Level: 1, Reason: "manual", CreatedAt: t0,
		ExpiresAt: t0.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetBan(ctx, "2001:db8::/64")
	if err != nil || got.Level != 2 || !reflect.DeepEqual(got.Addresses, []string{"2001:db8::1"}) || !got.ExpiresAt.Equal(t0.Add(time.Hour)) {
		t.Fatalf("get: %+v %v", got, err)
	}
	all, err := db.ListBans(ctx)
	if err != nil || len(all) != 2 {
		t.Fatalf("list: %v %v", all, err)
	}
	if n, err := db.DeleteBansExpiredBefore(ctx, t0.Add(30*time.Minute)); n != 1 || err != nil {
		t.Fatalf("expired: %d %v", n, err)
	}
	if err := db.DeleteBan(ctx, "2001:db8::/64"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetBan(ctx, "2001:db8::/64"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("after delete: %v", err)
	}
	if err := db.DeleteBan(ctx, "nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("delete missing: %v", err)
	}
}
