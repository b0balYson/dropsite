package client

import (
	"context"
	"net/http"
	"net/url"

	"github.com/b0balYson/dropsite/internal/quality"
)

// CurrentDeploy asks for the release being served.
const CurrentDeploy = "current"

// DeployReport is a release with its quality report (nil when it has none).
type DeployReport struct {
	Deploy
	Quality *quality.Report `json:"quality"`
}

// DeployReport returns a release with its quality report; deployID may be
// CurrentDeploy.
func (c *Client) DeployReport(ctx context.Context, ref, deployID string) (*DeployReport, error) {
	var out DeployReport
	if err := c.doJSON(ctx, http.MethodGet, sitePath(ref)+"/deploys/"+url.PathEscape(deployID), nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
