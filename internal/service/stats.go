package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/stats"
	"github.com/b0balYson/dropsite/internal/store"
)

// Statistics bounds.
const (
	StatsDefaultDays   = 30
	StatsMaxDays       = 400
	StatsRetentionDays = 400
	StatsAdminDays     = 7
	statsTopN          = 10
	// statsReadBytes bounds one transaction; statsMaxReads bounds one pass so
	// a large backlog is caught up over several passes.
	statsReadBytes = 8 << 20
	statsMaxReads  = 16
)

var statsLimits = store.StatsLimits{Pages: 500, Referrers: 200}

func day(t time.Time) string { return t.UTC().Format(time.DateOnly) }

// SiteStats is the traffic of one site over a period of whole UTC days.
type SiteStats struct {
	SiteID       string
	From, To     string
	Days         []domain.DayStat // every day of the period, oldest first
	Views        int64
	Visitors     int64 // sum of daily unique visitors
	MaxViews     int64 // largest daily view count
	TopPages     []domain.NamedCount
	TopReferrers []domain.NamedCount
}

// GetSiteStats returns the last days of a site's statistics, today included.
func (s *Service) GetSiteStats(ctx context.Context, a Actor, ref string, days int) (SiteStats, error) {
	if days < 1 || days > StatsMaxDays {
		return SiteStats{}, domain.E(domain.KindInvalid, domain.CodeInvalidInput,
			fmt.Sprintf("days must be between 1 and %d", StatsMaxDays)).
			WithParams(map[string]any{"field": "days", "max": StatsMaxDays})
	}
	site, err := s.resolveSite(ctx, a, ref)
	if err != nil {
		return SiteStats{}, err
	}
	now := s.now()
	st := SiteStats{SiteID: site.ID, From: day(now.AddDate(0, 0, -(days - 1))), To: day(now)}
	rows, err := s.repo.StatsDaily(ctx, site.ID, st.From, st.To)
	if err != nil {
		return SiteStats{}, err
	}
	byDay := make(map[string]domain.DayStat, len(rows))
	for _, r := range rows {
		byDay[r.Day] = r
	}
	st.Days = make([]domain.DayStat, 0, days)
	for i := days - 1; i >= 0; i-- {
		d := day(now.AddDate(0, 0, -i))
		r, ok := byDay[d]
		if !ok {
			r = domain.DayStat{Day: d}
		}
		st.Days = append(st.Days, r)
		st.Views += r.Views
		st.Visitors += r.Visitors
		st.MaxViews = max(st.MaxViews, r.Views)
	}
	if st.TopPages, err = s.repo.StatsTopPages(ctx, site.ID, st.From, st.To, statsTopN); err != nil {
		return SiteStats{}, err
	}
	if st.TopReferrers, err = s.repo.StatsTopReferrers(ctx, site.ID, st.From, st.To, statsTopN); err != nil {
		return SiteStats{}, err
	}
	return st, nil
}

// SitesViews returns the views of the given sites over the last days
// (admin only; used by the "All sites" screen).
func (s *Service) SitesViews(ctx context.Context, a Actor, siteIDs []string, days int) (map[string]int64, error) {
	if err := requireAdmin(a); err != nil {
		return nil, err
	}
	now := s.now()
	return s.repo.SitesViews(ctx, siteIDs, day(now.AddDate(0, 0, -(days-1))), day(now))
}

// CollectStats reads new lines of the sites access log into the aggregates.
// It is a no-op when collection is disabled or the log does not exist yet.
func (s *Service) CollectStats(ctx context.Context) error {
	path := s.cfg.StatsLogFile
	if path == "" {
		return nil
	}
	today := day(s.now())
	if err := s.repo.DeleteVisitorData(ctx, today); err != nil {
		return err
	}
	sites, err := s.hostIndex(ctx)
	if err != nil {
		return err
	}
	cur, err := s.repo.StatsCursor(ctx)
	if err != nil {
		return err
	}
	salt := s.saltFunc(ctx, today)
	isPreview := s.isPreviewHost
	for range statsMaxReads {
		ch, err := stats.ReadLog(path, cur, statsReadBytes)
		if err != nil {
			return err
		}
		if ch.Done(cur) {
			return nil
		}
		if ch.Lost {
			s.log.Warn("statistics: the access log was rotated away before it was read completely; some visits are lost")
		}
		b := stats.NewBatch()
		b.SkipHost = isPreview
		for _, line := range ch.Lines {
			if err := b.AddLine(line, sites, salt); err != nil {
				return err
			}
		}
		if b.Malformed > 0 || ch.Skipped > 0 {
			s.log.Debug("statistics: skipped log lines", "malformed", b.Malformed, "too_long", ch.Skipped)
		}
		if err := s.repo.SaveStatsBatch(ctx, b, ch.Next, statsLimits, s.now()); err != nil {
			return err
		}
		cur = ch.Next
	}
	return nil
}

// saltFunc creates and caches today's salt; other days count views only.
func (s *Service) saltFunc(ctx context.Context, today string) stats.SaltFunc {
	var cached []byte
	return func(d string) ([]byte, error) {
		if d != today {
			return nil, nil
		}
		if cached == nil {
			salt, err := s.repo.StatsSalt(ctx, today, true)
			if err != nil {
				return nil, err
			}
			cached = salt
		}
		return cached, nil
	}
}

// hostIndex maps every active hostname to its site.
func (s *Service) hostIndex(ctx context.Context) (stats.SiteFunc, error) {
	layout, err := s.repo.ActiveLayout(ctx)
	if err != nil {
		return nil, fmt.Errorf("load hostnames: %w", err)
	}
	m := map[string]string{}
	for _, l := range layout {
		for _, h := range l.Hostnames {
			m[h] = l.SiteID
		}
	}
	return func(h string) (string, bool) {
		if id, ok := m[h]; ok {
			return id, true
		}
		return s.previewSite(h)
	}, nil
}

// previewSite maps a release preview host (p-<deploy>-<site>.<BASE_DOMAIN>,
// wildcard mode) to its site. Previews are review traffic, not visits: their
// requests are skipped (isPreviewHost), but links from a preview to the site
// are internal navigation rather than a referrer.
func (s *Service) previewSite(host string) (string, bool) {
	if id, ok := s.draftPreviewSite(host); ok {
		return id, true
	}
	label, ok := strings.CutSuffix(host, "."+s.cfg.BaseDomain)
	if !ok || !domain.IsPreviewLabel(label) {
		return "", false
	}
	return label[len(label)-domain.SiteIDLength:], true
}

func (s *Service) isPreviewHost(host string) bool {
	_, ok := s.previewSite(host)
	return ok
}

// RunStats collects statistics every interval until ctx is done.
func (s *Service) RunStats(ctx context.Context, interval time.Duration) {
	if s.cfg.StatsLogFile == "" {
		s.log.Info("visit statistics are disabled (STATS_LOG_FILE is off)")
		return
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if err := s.CollectStats(ctx); err != nil && !errors.Is(err, context.Canceled) {
			s.log.Error("statistics collection failed", "err", err)
			if errors.Is(err, stats.ErrNoInode) {
				return
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// pruneStats applies the retention of aggregates.
func (s *Service) pruneStats(ctx context.Context) error {
	return s.repo.DeleteStatsBefore(ctx, day(s.now().AddDate(0, 0, -StatsRetentionDays)))
}
