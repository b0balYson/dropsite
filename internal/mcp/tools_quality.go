package mcp

import (
	"context"
	"errors"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/b0balYson/dropsite/internal/quality"
)

// QualityReport is the result of get_quality_report.
type QualityReport struct {
	SiteID   string          `json:"site_id"`
	DeployID string          `json:"deploy_id"`
	Current  bool            `json:"current" jsonschema:"true for the version being served"`
	Summary  string          `json:"summary" jsonschema:"short human summary to relay to the user"`
	Report   *quality.Report `json:"report,omitempty" jsonschema:"the full report; absent for versions published before reports existed"`
}

// QualityBackend reads release quality reports. The tool fails when the
// backend does not implement it.
type QualityBackend interface {
	GetQualityReport(ctx context.Context, ref, deployID string) (QualityReport, error)
}

type qualityArgs struct {
	Site     string `json:"site" jsonschema:"site ID or slug"`
	DeployID string `json:"deploy_id,omitempty" jsonschema:"version to check; empty means the version being served"`
}

var errNoQuality = errors.New("this server cannot show quality reports; use the web interface of Dropsite")

// NoReportSummary is the summary of a version without a report.
const NoReportSummary = "This version has no quality report (it was published before reports existed); publish again to get one."

// NewQualityReport builds the tool result; both servers use it.
func NewQualityReport(siteID, deployID string, current bool, r *quality.Report) QualityReport {
	out := QualityReport{SiteID: siteID, DeployID: deployID, Current: current, Report: r, Summary: NoReportSummary}
	if r != nil {
		out.Summary = r.Summary
	}
	return out
}

func (t tools) registerQuality(s *sdk.Server, o Options) {
	if !o.Cabinet {
		return
	}
	sdk.AddTool(s, &sdk.Tool{Name: "get_quality_report", Annotations: readOnly("Quality report"),
		Description: "Show the quality check of a site version (the one being served by default): broken internal " +
			"links, missing images, styles, scripts or fonts, images without alt text, pages without a title, " +
			"description, viewport or language, page weight, files loaded from other servers (CDNs, Google Fonts, " +
			"hotlinked images) and a score from 0 to 100. Every publication already returns a short version in " +
			"quality. Fix what you can right away with update_site_files (ship external files with the site via " +
			"fetch); tell the user in plain words only about problems that matter to them."}, t.getQualityReport)
}

func (t tools) getQualityReport(ctx context.Context, req *sdk.CallToolRequest, in qualityArgs) (*sdk.CallToolResult, QualityReport, error) {
	b, err := t.resolve(ctx, req)
	if err != nil {
		return nil, QualityReport{}, err
	}
	q, ok := b.(QualityBackend)
	if !ok {
		return nil, QualityReport{}, errNoQuality
	}
	out, err := q.GetQualityReport(ctx, in.Site, in.DeployID)
	return nil, out, err
}
