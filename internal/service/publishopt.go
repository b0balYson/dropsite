package service

import (
	"context"
	"strings"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/optimize"
)

// OptimizeBudget bounds the time a publication spends optimizing, including
// the wait for the installation-wide optimization slot.
const OptimizeBudget = 20 * time.Second

// optimizeSlot lets one optimization run at a time in the process: image
// encoding is CPU and memory heavy and the server may have 1 GB of RAM.
var optimizeSlot = make(chan struct{}, 1)

// PublishSettingsView is a site's publish settings with the installation
// switch applied.
type PublishSettingsView struct {
	SiteID string
	// Optimize is the site's own setting.
	Optimize bool
	// InstallationEnabled is false when PUBLISH_OPTIMIZE=off.
	InstallationEnabled bool
	// Effective tells whether the next publication is optimized.
	Effective bool
}

func (s *Service) publishView(p domain.PublishSettings) PublishSettingsView {
	return PublishSettingsView{
		SiteID: p.SiteID, Optimize: p.Optimize, InstallationEnabled: s.cfg.PublishOptimize,
		Effective: p.Optimize && s.cfg.PublishOptimize,
	}
}

// GetPublishSettings returns a site's publish settings.
func (s *Service) GetPublishSettings(ctx context.Context, a Actor, ref string) (PublishSettingsView, error) {
	site, err := s.resolveSite(ctx, a, ref)
	if err != nil {
		return PublishSettingsView{}, err
	}
	p, err := s.repo.GetPublishSettings(ctx, site.ID)
	if err != nil {
		return PublishSettingsView{}, err
	}
	return s.publishView(p), nil
}

// SetPublishSettings turns publish-time optimization of a site on or off.
// It applies from the next publication; existing releases stay as they are.
func (s *Service) SetPublishSettings(ctx context.Context, a Actor, ref string, optimizeOn bool) (PublishSettingsView, error) {
	site, err := s.resolveSite(ctx, a, ref)
	if err != nil {
		return PublishSettingsView{}, err
	}
	prev, err := s.repo.GetPublishSettings(ctx, site.ID)
	if err != nil {
		return PublishSettingsView{}, err
	}
	next := domain.PublishSettings{SiteID: site.ID, Optimize: optimizeOn}
	if prev != next {
		if err := s.repo.SetPublishSettings(ctx, next); err != nil {
			return PublishSettingsView{}, err
		}
		s.audit(ctx, a, "site.publish_settings", siteTarget(site.ID), map[string]any{"optimize": optimizeOn})
	}
	return s.publishView(next), nil
}

// optimizeRelease runs publish-time optimization on a staging directory
// when the installation and the site allow it. It never fails: problems
// are logged and the publication goes on. The result is nil when nothing
// ran.
func (s *Service) optimizeRelease(ctx context.Context, site domain.Site, staging string) *optimize.Result {
	if !s.cfg.PublishOptimize {
		return nil
	}
	p, err := s.repo.GetPublishSettings(ctx, site.ID)
	if err != nil {
		s.log.Warn("optimize: load settings", "site", site.ID, "err", err)
		return nil
	}
	if !p.Optimize {
		return nil
	}
	deadline := time.Now().Add(OptimizeBudget)
	wait := time.NewTimer(OptimizeBudget)
	defer wait.Stop()
	select {
	case optimizeSlot <- struct{}{}:
	case <-wait.C:
		s.log.Warn("optimize: skipped, another optimization is running", "site", site.ID)
		return nil
	case <-ctx.Done():
		return nil
	}
	defer func() { <-optimizeSlot }()
	siteURL := ""
	if v, err := s.view(ctx, site); err == nil {
		siteURL = strings.TrimSuffix(v.URL, "/")
	} else {
		s.log.Warn("optimize: site address", "site", site.ID, "err", err)
	}
	res := optimize.Run(ctx, staging, optimize.Options{
		SiteURL: siteURL, SiteID: site.ID, Noindex: site.Noindex, Deadline: deadline, Logger: s.log,
	})
	s.log.Info("release optimized", "site", site.ID, "images", res.Images.Converted,
		"saved_bytes", res.Images.BytesBefore-res.Images.BytesAfter, "generated", res.Generated,
		"timed_out", res.TimedOut, "failures", res.Failures, "ms", res.DurationMS)
	return &res
}
