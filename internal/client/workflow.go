package client

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// Site copies, templates, scheduled publishes and screenshots.

// renderTimeout bounds screenshot and visual diff calls: the server waits
// for its renderer, twice for a diff.
const renderTimeout = 3 * time.Minute

// maxImage bounds a screenshot download.
const maxImage = 20 << 20

// NewSiteRequest names a site created by a copy or from a template.
type NewSiteRequest struct {
	Title string `json:"title,omitempty"`
	Slug  string `json:"slug,omitempty"`
	Lang  string `json:"lang,omitempty"`
}

// Template is a built-in starter template.
type Template struct {
	Name        string `json:"name"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

// Schedule is a scheduled publish.
type Schedule struct {
	ID         string     `json:"id"`
	SiteID     string     `json:"site_id"`
	SiteTitle  string     `json:"site_title"`
	SiteURL    string     `json:"site_url"`
	DeployID   string     `json:"deploy_id"`
	PublishAt  time.Time  `json:"publish_at"`
	Status     string     `json:"status"`
	Error      string     `json:"error"`
	CreatedAt  time.Time  `json:"created_at"`
	FinishedAt *time.Time `json:"finished_at"`
	PreviewURL *string    `json:"preview_url"`
}

// Screenshot is a PNG of a release.
type Screenshot struct {
	SiteID   string
	DeployID string
	Device   string
	Width    int
	Height   int
	PNG      []byte
}

// VisualDiff compares two releases.
type VisualDiff struct {
	SiteID         string  `json:"site_id"`
	FromDeployID   string  `json:"from_deploy_id"`
	ToDeployID     string  `json:"to_deploy_id"`
	Device         string  `json:"device"`
	ChangedPercent float64 `json:"changed_percent"`
	Width          int     `json:"width"`
	Height         int     `json:"height"`
	ImagePNG       string  `json:"image_png"`
}

// PNG decodes the comparison picture.
func (d VisualDiff) PNG() ([]byte, error) { return base64.StdEncoding.DecodeString(d.ImagePNG) }

// CloneSite copies a site into a new one.
func (c *Client) CloneSite(ctx context.Context, ref string, r NewSiteRequest) (*DeployResult, error) {
	var out DeployResult
	if err := c.doJSON(ctx, http.MethodPost, sitePath(ref)+"/clone", nil, r, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListTemplates lists the built-in templates.
func (c *Client) ListTemplates(ctx context.Context, lang string) (*List[Template], error) {
	var out List[Template]
	q := url.Values{}
	if lang != "" {
		q.Set("lang", lang)
	}
	if err := c.doJSON(ctx, http.MethodGet, "/templates", q, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateSiteFromTemplate creates and publishes a site from a template.
func (c *Client) CreateSiteFromTemplate(ctx context.Context, name string, r NewSiteRequest) (*DeployResult, error) {
	var out DeployResult
	if err := c.doJSON(ctx, http.MethodPost, "/templates/"+url.PathEscape(name)+"/sites", nil, r, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

type scheduleRequest struct {
	DeployID  string `json:"deploy_id,omitempty"`
	PublishAt string `json:"publish_at"`
}

// ScheduleRelease schedules a ready release of a site.
func (c *Client) ScheduleRelease(ctx context.Context, ref, deployID, publishAt string) (*Schedule, error) {
	var out Schedule
	err := c.doJSON(ctx, http.MethodPost, sitePath(ref)+"/schedules", nil,
		scheduleRequest{DeployID: deployID, PublishAt: publishAt}, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// ScheduleUpload turns an upload session into a scheduled release.
func (c *Client) ScheduleUpload(ctx context.Context, id, publishAt string) (*Schedule, error) {
	var out Schedule
	if err := c.doJSON(ctx, http.MethodPost, uploadPath(id)+"/schedule", nil, scheduleRequest{PublishAt: publishAt}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListSchedules lists the scheduled publishes of one site or all sites.
func (c *Client) ListSchedules(ctx context.Context, site string) (*List[Schedule], error) {
	var out List[Schedule]
	q := url.Values{}
	if site != "" {
		q.Set("site", site)
	}
	if err := c.doJSON(ctx, http.MethodGet, "/schedules", q, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CancelSchedule cancels a pending publish.
func (c *Client) CancelSchedule(ctx context.Context, id string) (*Schedule, error) {
	var out Schedule
	if err := c.doJSON(ctx, http.MethodDelete, "/schedules/"+url.PathEscape(id), nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Screenshot downloads the PNG of a release (deployID empty: the current one).
func (c *Client) Screenshot(ctx context.Context, ref, deployID, device string) (*Screenshot, error) {
	ctx, cancel := context.WithTimeout(ctx, renderTimeout)
	defer cancel()
	q := url.Values{}
	if deployID != "" {
		q.Set("deploy_id", deployID)
	}
	if device != "" {
		q.Set("device", device)
	}
	req, err := c.newRequest(ctx, http.MethodGet, sitePath(ref)+"/screenshot", q, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, c.networkError(req, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, parseAPIError(resp)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxImage+1))
	if err != nil {
		return nil, fmt.Errorf("read screenshot: %w", err)
	}
	if len(data) > maxImage {
		return nil, fmt.Errorf("screenshot is larger than %d bytes", maxImage)
	}
	out := &Screenshot{SiteID: resp.Header.Get("X-Dropsite-Site-Id"), DeployID: resp.Header.Get("X-Dropsite-Deploy-Id"), Device: resp.Header.Get("X-Dropsite-Device"), PNG: data}
	_, _ = fmt.Sscanf(resp.Header.Get("X-Dropsite-Viewport"), "%dx%d", &out.Width, &out.Height)
	return out, nil
}

// CompareVersions compares two releases (to empty: the current one).
func (c *Client) CompareVersions(ctx context.Context, ref, from, to, device string) (*VisualDiff, error) {
	ctx, cancel := context.WithTimeout(ctx, renderTimeout)
	defer cancel()
	q := url.Values{"from": {from}}
	if to != "" {
		q.Set("to", to)
	}
	if device != "" {
		q.Set("device", device)
	}
	req, err := c.newRequest(ctx, http.MethodGet, sitePath(ref)+"/visual-diff", q, nil)
	if err != nil {
		return nil, err
	}
	var out VisualDiff
	if err := c.send(req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
