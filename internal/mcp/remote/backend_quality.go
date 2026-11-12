package remote

import (
	"context"

	"github.com/b0balYson/dropsite/internal/mcp"
)

var _ mcp.QualityBackend = backend{}

func (b backend) GetQualityReport(ctx context.Context, ref, deployID string) (mcp.QualityReport, error) {
	v, err := b.svc.GetDeployReport(ctx, b.actor, ref, deployID)
	if err != nil {
		return mcp.QualityReport{}, b.toolError(err)
	}
	return mcp.NewQualityReport(v.Site.Site.ID, v.Deploy.ID, v.Current, v.Report), nil
}
