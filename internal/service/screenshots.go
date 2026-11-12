package service

import (
	"context"
	"crypto/rand"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/ratelimit"
	"github.com/b0balYson/dropsite/internal/render"
	"github.com/b0balYson/dropsite/internal/visualdiff"
)

// Screenshots of releases through the optional renderer (change
// chat-workflow, docs/screenshots.md).
const (
	// ScreenshotsPerMinute caps renderer calls per user.
	ScreenshotsPerMinute = 30
	// RenderTokenTTL bounds how long the renderer may fetch a release.
	RenderTokenTTL = 2 * time.Minute
	// ScreenshotCacheTTL is how long a cached screenshot is kept.
	ScreenshotCacheTTL = 30 * 24 * time.Hour
	screenshotsDir     = "screenshots"
)

// Devices are the screenshot viewports.
var Devices = map[string][2]int{"desktop": {1280, 800}, "mobile": {390, 844}}

// Screenshotter takes a screenshot through the renderer.
type Screenshotter interface {
	Screenshot(ctx context.Context, shot render.Shot) ([]byte, error)
}

type renderGrant struct {
	siteID, deployID string
	expires          time.Time
}

type shotState struct {
	shooter    Screenshotter
	renderBase string
	shotDir    string
	shotLimit  *ratelimit.Window
	grantsMu   sync.Mutex
	grants     map[string]renderGrant
}

func newShotState(d Deps, now func() time.Time) shotState {
	st := shotState{
		shotDir:    filepath.Join(d.Config.StateDir, screenshotsDir),
		shotLimit:  ratelimit.New(ScreenshotsPerMinute, time.Minute).WithClock(now),
		grants:     map[string]renderGrant{},
		renderBase: strings.TrimSuffix(d.Config.Renderer.BaseURL, "/"),
		shooter:    d.Renderer,
	}
	if st.shooter == nil && d.Config.Renderer.Enabled() {
		st.shooter = render.New(d.Config.Renderer.URL, nil)
	}
	return st
}

// ScreenshotsEnabled reports whether a renderer is configured.
func (s *Service) ScreenshotsEnabled() bool { return s.shooter != nil }

// Shot is a screenshot of a release.
type Shot struct {
	SiteID   string
	DeployID string
	Device   string
	Width    int
	Height   int
	PNG      []byte
}

// VisualDiff compares two releases.
type VisualDiff struct {
	SiteID         string
	FromDeployID   string
	ToDeployID     string
	Device         string
	ChangedPercent float64
	Width          int
	Height         int
	PNG            []byte
}

func device(name string) (string, [2]int, error) {
	if name == "" {
		name = "desktop"
	}
	size, ok := Devices[name]
	if !ok {
		return "", size, domain.E(domain.KindInvalid, domain.CodeInvalidInput, `device must be "desktop" or "mobile"`).
			WithParams(map[string]any{"field": "device"})
	}
	return name, size, nil
}

// Screenshot returns a PNG of the first screen of a release of a visible
// site (the current one when deployID is empty).
func (s *Service) Screenshot(ctx context.Context, a Actor, ref, deployID, dev string) (Shot, error) {
	if s.shooter == nil {
		return Shot{}, domain.ErrRendererUnavailable
	}
	dev, size, err := device(dev)
	if err != nil {
		return Shot{}, err
	}
	site, err := s.resolveSite(ctx, a, ref)
	if err != nil {
		return Shot{}, err
	}
	d, err := s.shotDeploy(ctx, site, deployID)
	if err != nil {
		return Shot{}, err
	}
	out := Shot{SiteID: site.ID, DeployID: d.ID, Device: dev, Width: size[0], Height: size[1]}
	cache := filepath.Join(s.shotDir, site.ID, d.ID+"-"+dev+".png")
	if data, err := os.ReadFile(cache); err == nil { //nolint:gosec // path built from validated IDs
		out.PNG = data
		return out, nil
	}
	if ok, retry := s.shotLimit.Allow(userKey(a.User.ID)); !ok {
		return Shot{}, domain.ErrRateLimited.WithRetryAfter(retry).
			WithParams(map[string]any{"limit": ScreenshotsPerMinute})
	}
	token := s.grantRender(site.ID, d.ID)
	defer s.revokeRender(token)
	data, err := s.shooter.Screenshot(ctx, render.Shot{URL: s.renderBase + "/", Token: token, Width: size[0], Height: size[1]})
	if err != nil {
		s.log.Warn("screenshot failed", "site", site.ID, "deploy", d.ID, "err", err)
		return Shot{}, domain.ErrRendererUnavailable
	}
	s.storeShot(cache, data)
	out.PNG = data
	return out, nil
}

func (s *Service) shotDeploy(ctx context.Context, site domain.Site, deployID string) (domain.Deploy, error) {
	if deployID == "" {
		if site.CurrentDeployID == nil {
			return domain.Deploy{}, domain.E(domain.KindInvalid, domain.CodeSiteNotPublished,
				"the site has no published version yet")
		}
		deployID = *site.CurrentDeployID
	}
	d, err := s.repo.GetDeploy(ctx, site.ID, deployID)
	if err != nil {
		return domain.Deploy{}, err
	}
	if d.Status != domain.DeployReady {
		return domain.Deploy{}, domain.E(domain.KindInvalid, domain.CodeDeployNotReady, "this release is no longer available")
	}
	return d, nil
}

// storeShot caches a screenshot; failures only cost a later re-render.
func (s *Service) storeShot(path string, data []byte) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		s.log.Warn("screenshot cache", "err", err)
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		s.log.Warn("screenshot cache", "err", err)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		s.log.Warn("screenshot cache", "err", err)
	}
}

// CompareReleases screenshots two releases of a visible site (to empty:
// the current one) and compares them.
func (s *Service) CompareReleases(ctx context.Context, a Actor, ref, from, to, dev string) (VisualDiff, error) {
	if s.shooter == nil {
		return VisualDiff{}, domain.ErrRendererUnavailable
	}
	if strings.TrimSpace(from) == "" {
		return VisualDiff{}, domain.E(domain.KindInvalid, domain.CodeInvalidInput,
			"from_deploy_id is required: pick an earlier release from list_deploys").WithParams(map[string]any{"field": "from"})
	}
	before, err := s.Screenshot(ctx, a, ref, from, dev)
	if err != nil {
		return VisualDiff{}, err
	}
	after, err := s.Screenshot(ctx, a, ref, to, dev)
	if err != nil {
		return VisualDiff{}, err
	}
	res, err := visualdiff.ComparePNG(before.PNG, after.PNG)
	if err != nil {
		s.log.Warn("visual diff: bad screenshot", "site", before.SiteID, "err", err)
		return VisualDiff{}, domain.ErrRendererUnavailable
	}
	img, err := visualdiff.EncodePNG(res.Image)
	if err != nil {
		return VisualDiff{}, err
	}
	return VisualDiff{
		SiteID: before.SiteID, FromDeployID: before.DeployID, ToDeployID: after.DeployID, Device: before.Device,
		ChangedPercent: res.ChangedPercent, Width: res.Width, Height: res.Height, PNG: img,
	}, nil
}

func (s *Service) grantRender(siteID, deployID string) string {
	token := rand.Text()
	s.grantsMu.Lock()
	defer s.grantsMu.Unlock()
	now := s.now()
	for k, g := range s.grants {
		if now.After(g.expires) {
			delete(s.grants, k)
		}
	}
	s.grants[token] = renderGrant{siteID: siteID, deployID: deployID, expires: now.Add(RenderTokenTTL)}
	return token
}

func (s *Service) revokeRender(token string) {
	s.grantsMu.Lock()
	delete(s.grants, token)
	s.grantsMu.Unlock()
}

// OpenRenderRelease opens the release a live render token grants, for the
// render listener. It is the only way the renderer reaches site files.
func (s *Service) OpenRenderRelease(token string) (fs.FS, func(), bool) {
	if token == "" {
		return nil, nil, false
	}
	s.grantsMu.Lock()
	g, ok := s.grants[token]
	s.grantsMu.Unlock()
	if !ok || s.now().After(g.expires) {
		return nil, nil, false
	}
	root, err := s.files.OpenRelease(g.siteID, g.deployID)
	if err != nil {
		s.log.Warn("render: open release", "site", g.siteID, "deploy", g.deployID, "err", err)
		return nil, nil, false
	}
	return root.FS(), func() { _ = root.Close() }, true
}

// purgeScreenshots removes cached screenshots older than ScreenshotCacheTTL.
func (s *Service) purgeScreenshots(now time.Time) {
	err := filepath.WalkDir(s.shotDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil //nolint:nilerr // removed meanwhile
		}
		if now.Sub(info.ModTime()) > ScreenshotCacheTTL {
			if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
				s.log.Warn("screenshot cache: remove", "path", p, "err", err)
			}
		}
		return nil
	})
	if err != nil {
		s.log.Warn("screenshot cache: purge", "err", err)
	}
}
