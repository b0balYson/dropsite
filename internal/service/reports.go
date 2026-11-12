package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/optimize"
	"github.com/b0balYson/dropsite/internal/quality"
	"github.com/b0balYson/dropsite/internal/storage"
)

// CurrentDeploy names the release being served where a deploy ID is
// expected (GET /sites/{ref}/deploys/current).
const CurrentDeploy = "current"

// DeployReportView is a release with its quality report (nil when the
// release has none: published before reports existed, or analysis failed).
type DeployReportView struct {
	Site    SiteView
	Deploy  domain.Deploy
	Current bool
	Report  *quality.Report
}

// analyzeRelease computes the quality report of a staging directory. It
// never fails the publication: errors and panics give a nil report.
func (s *Service) analyzeRelease(ctx context.Context, site domain.Site, staging string, opt *optimize.Result) (rep *quality.Report) {
	defer func() {
		if p := recover(); p != nil {
			s.log.Error("quality: analysis panicked", "site", site.ID, "panic", p)
			rep = nil
		}
	}()
	o := quality.Options{}
	if v, err := s.view(ctx, site); err == nil {
		for _, u := range v.URLs {
			if pu, err := url.Parse(u); err == nil && pu.Hostname() != "" {
				o.Hosts = append(o.Hosts, pu.Hostname())
			}
		}
	}
	if opt != nil {
		o.Generated = opt.Generated
	}
	r, err := quality.Analyze(ctx, staging, o)
	if err != nil {
		s.log.Warn("quality: analysis failed", "site", site.ID, "err", err)
		return nil
	}
	if opt != nil {
		r.Optimization = &quality.Optimization{
			ImagesConverted: opt.Images.Converted, BytesSaved: opt.Images.BytesBefore - opt.Images.BytesAfter,
			Generated: append([]string{}, opt.Generated...), TimedOut: opt.TimedOut,
		}
	}
	r.Normalize()
	return &r
}

// saveReport stores a release's report; failures are only logged.
func (s *Service) saveReport(ctx context.Context, siteID, deployID string, rep *quality.Report) {
	if rep == nil {
		return
	}
	data, err := json.Marshal(rep)
	if err == nil {
		err = s.repo.PutDeployReport(context.WithoutCancel(ctx), siteID, deployID, data, s.now())
	}
	if err != nil {
		s.log.Error("quality: save report", "site", siteID, "deploy", deployID, "err", err)
	}
}

// reportOf loads a release's report; a missing or unreadable one is nil.
func (s *Service) reportOf(ctx context.Context, siteID, deployID string) *quality.Report {
	data, err := s.repo.GetDeployReport(ctx, siteID, deployID)
	if err != nil {
		if !errors.Is(err, domain.ErrNotFound) {
			s.log.Error("quality: load report", "site", siteID, "deploy", deployID, "err", err)
		}
		return nil
	}
	var r quality.Report
	if err := json.Unmarshal(data, &r); err != nil {
		s.log.Error("quality: decode report", "site", siteID, "deploy", deployID, "err", err)
		return nil
	}
	r.Normalize()
	return &r
}

// copyReport gives a re-release (same files, new counters) the report of
// the release it was copied from.
func (s *Service) copyReport(ctx context.Context, siteID, fromID, toID string) {
	if rep := s.reportOf(ctx, siteID, fromID); rep != nil {
		s.saveReport(ctx, siteID, toID, rep)
	}
}

// dropReports removes the reports of pruned releases.
func (s *Service) dropReports(ctx context.Context, siteID string, ids []string) {
	if err := s.repo.DeleteDeployReports(ctx, siteID, ids); err != nil {
		s.log.Error("quality: delete reports", "site", siteID, "err", err)
	}
}

// GetDeployReport returns a release of a site the actor may see with its
// quality report; an empty deployID or CurrentDeploy means the release
// being served.
func (s *Service) GetDeployReport(ctx context.Context, a Actor, ref, deployID string) (DeployReportView, error) {
	site, err := s.resolveSite(ctx, a, ref)
	if err != nil {
		return DeployReportView{}, err
	}
	if deployID == "" || deployID == CurrentDeploy {
		if site.CurrentDeployID == nil {
			return DeployReportView{}, domain.E(domain.KindInvalid, domain.CodeSiteNotPublished,
				"the site has no published release yet")
		}
		deployID = *site.CurrentDeployID
	}
	if !storage.ValidDeployID(deployID) {
		return DeployReportView{}, domain.ErrNotFound
	}
	d, err := s.repo.GetDeploy(ctx, site.ID, deployID)
	if err != nil {
		return DeployReportView{}, err
	}
	v, err := s.view(ctx, site)
	if err != nil {
		return DeployReportView{}, fmt.Errorf("site view: %w", err)
	}
	return DeployReportView{
		Site: v, Deploy: d, Current: site.CurrentDeployID != nil && *site.CurrentDeployID == d.ID,
		Report: s.reportOf(ctx, site.ID, d.ID),
	}, nil
}

// DeployScores returns the score and grade of the site's releases that have
// a report, by deploy ID.
func (s *Service) DeployScores(ctx context.Context, a Actor, ref string) (map[string]domain.QualityScore, error) {
	site, err := s.resolveSite(ctx, a, ref)
	if err != nil {
		return nil, err
	}
	return s.repo.ListDeployScores(ctx, site.ID)
}
