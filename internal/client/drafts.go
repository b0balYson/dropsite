package client

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// Draft releases (change staged-publish).

// DeployOptions are the options of a publication.
type DeployOptions struct {
	// Draft builds the release as the site's draft: checked like any
	// publication, shown only at the site's preview address.
	Draft bool
}

func (o DeployOptions) query() url.Values {
	if !o.Draft {
		return nil
	}
	return url.Values{"draft": {"true"}}
}

// Draft is a site's draft release.
type Draft struct {
	DeployID   string    `json:"deploy_id"`
	CreatedAt  time.Time `json:"created_at"`
	PreviewURL *string   `json:"preview_url"`
}

// CommitUploadWith closes an upload session; with draft the release becomes
// the site's draft.
func (c *Client) CommitUploadWith(ctx context.Context, id string, deletes []string, draft bool) (*FilesResult, error) {
	var out FilesResult
	in := commitUploadRequest{Delete: deletes, Draft: draft}
	if err := c.doJSON(ctx, http.MethodPost, uploadPath(id)+"/commit", nil, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// PublishDraft switches the site to its draft.
func (c *Client) PublishDraft(ctx context.Context, ref string) (*DeployResult, error) {
	var out DeployResult
	if err := c.doJSON(ctx, http.MethodPost, sitePath(ref)+"/draft/publish", nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DiscardDraft deletes the site's draft and returns the site.
func (c *Client) DiscardDraft(ctx context.Context, ref string) (*Site, error) {
	var out Site
	if err := c.doJSON(ctx, http.MethodDelete, sitePath(ref)+"/draft", nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
