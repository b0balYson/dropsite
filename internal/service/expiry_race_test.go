package service

import (
	"context"
	"testing"
	"time"

	"github.com/b0balYson/dropsite/internal/store"
)

// listHookRepo runs after once ListExpiredSites has returned, standing in
// for an owner request that lands between the listing and the deletion.
type listHookRepo struct {
	store.Repository
	after func()
}

func (r *listHookRepo) ListExpiredSites(ctx context.Context, now time.Time) ([]string, error) {
	ids, err := r.Repository.ListExpiredSites(ctx, now)
	if r.after != nil {
		f := r.after
		r.after = nil
		f()
	}
	return ids, err
}

func TestExpiryRechecksUnderLock(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	alice := e.user("alice@pp.test")
	v, err := e.svc.CreateSite(ctx, alice, "Promo", "")
	if err != nil {
		t.Fatal(err)
	}
	at := e.clock.Now().Add(time.Minute)
	if _, err := e.svc.UpdateSite(ctx, alice, v.Site.ID, SiteUpdate{Expiry: &Expiry{At: &at}}); err != nil {
		t.Fatal(err)
	}
	e.clock.Advance(2 * time.Minute)
	later := e.clock.Now().Add(time.Hour)
	e.svc.repo = &listHookRepo{Repository: e.repo, after: func() {
		// The owner extends the expiry after the site was listed as expired.
		if _, err := e.svc.UpdateSite(ctx, alice, v.Site.ID, SiteUpdate{Expiry: &Expiry{At: &later}}); err != nil {
			t.Error(err)
		}
	}}
	if err := e.svc.ExpireSites(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.GetSite(ctx, alice, v.Site.ID); err != nil {
		t.Fatalf("site with an extended expiry was deleted: %v", err)
	}
	e.clock.Advance(2 * time.Hour)
	if err := e.svc.ExpireSites(ctx); err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.GetSite(ctx, alice, v.Site.ID)
	wantNotFound(t, err)
}
