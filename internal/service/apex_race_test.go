package service

import (
	"context"
	"sync"
	"testing"
)

// SetApex runs under the locks of the new and the previous apex site, so
// flag changes of either racing with it never leave the apex hostname with
// markers that do not match the site it serves.
func TestApexMarkersSurviveConcurrentFlagChanges(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	alice := e.user("alice@pp.test")
	a, _ := e.svc.CreateSite(ctx, alice, "A", "")
	b, _ := e.svc.CreateSite(ctx, alice, "B", "")
	ids := []string{a.Site.ID, b.Site.ID}
	for i := range 60 {
		var wg sync.WaitGroup
		wg.Add(3)
		go func() {
			defer wg.Done()
			if _, err := e.svc.SetApex(ctx, e.admin, ids[i%2]); err != nil {
				t.Error(err)
			}
		}()
		for _, id := range ids {
			go func() {
				defer wg.Done()
				if _, err := e.svc.UpdateSite(ctx, alice, id, SiteUpdate{Noindex: bptr(i%3 != 0)}); err != nil {
					t.Error(err)
				}
			}()
		}
		wg.Wait()
		if i%7 == 6 {
			if _, err := e.svc.ClearApex(ctx, e.admin, ids[i%2]); err != nil {
				t.Fatal(err)
			}
			continue
		}
		owner, err := e.svc.apexOwner(ctx)
		if err != nil || owner == "" {
			t.Fatalf("apex owner %q %v", owner, err)
		}
		site, err := e.repo.GetSite(ctx, owner)
		if err != nil {
			t.Fatal(err)
		}
		if got := e.marker("noindex", testBase); got != site.Noindex {
			t.Fatalf("round %d: apex noindex marker %v, site %v", i, got, site.Noindex)
		}
	}
}
