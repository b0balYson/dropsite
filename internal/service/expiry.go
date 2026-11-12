package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
)

// ExpiryCheckInterval is how often expired sites are taken down.
const ExpiryCheckInterval = 30 * time.Second

// ExpireSites soft-deletes every active site whose expiry has passed, exactly
// like a deletion by its owner; purge follows after DeletedSiteTTL.
func (s *Service) ExpireSites(ctx context.Context) error {
	ids, err := s.repo.ListExpiredSites(ctx, s.now())
	if err != nil {
		return fmt.Errorf("list expired sites: %w", err)
	}
	for _, id := range ids {
		if err := s.expireSite(ctx, id); err != nil {
			return fmt.Errorf("expire site %s: %w", id, err)
		}
	}
	return nil
}

// expireSite re-reads the site under its lock and takes it down only if it
// is still expired: the owner may have extended or cleared the expiry since
// the list was made.
func (s *Service) expireSite(ctx context.Context, id string) error {
	unlock := s.siteLocks.lock(id)
	defer unlock()
	site, err := s.repo.GetSite(ctx, id)
	if errors.Is(err, domain.ErrNotFound) {
		return nil // deleted meanwhile
	}
	if err != nil {
		return err
	}
	if site.ExpiresAt == nil || s.now().Before(*site.ExpiresAt) {
		return nil
	}
	if err := s.softDeleteLocked(ctx, Actor{}, site, "site.expire"); err != nil && !errors.Is(err, domain.ErrNotFound) {
		return err
	}
	return nil
}

// RunExpiry runs ExpireSites every interval until ctx is done.
func (s *Service) RunExpiry(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if err := s.ExpireSites(ctx); err != nil {
			s.log.Error("site expiry failed", "err", err)
		}
	}
}
