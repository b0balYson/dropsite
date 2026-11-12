package main

import (
	"context"

	"github.com/b0balYson/dropsite/internal/client"
	"github.com/b0balYson/dropsite/internal/mcp"
)

// Quality report tool of the local server, through the API.

var _ mcp.QualityBackend = localBackend{}

func (b localBackend) GetQualityReport(ctx context.Context, ref, deployID string) (mcp.QualityReport, error) {
	if deployID == "" {
		deployID = client.CurrentDeploy
	}
	r, err := b.c.DeployReport(ctx, ref, deployID)
	if err != nil {
		return mcp.QualityReport{}, toolError(err)
	}
	return mcp.NewQualityReport(r.SiteID, r.ID, r.Current, r.Quality), nil
}
