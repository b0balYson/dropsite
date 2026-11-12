package client

import (
	"context"
	"net/http"
)

// Me returns the current user, limits and site count; it also validates the token.
func (c *Client) Me(ctx context.Context) (*Me, error) {
	var out Me
	if err := c.doJSON(ctx, http.MethodGet, "/me", nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListSites returns one page of the caller's sites.
func (c *Client) ListSites(ctx context.Context, o ListOptions) (*List[SiteItem], error) {
	var out List[SiteItem]
	if err := c.doJSON(ctx, http.MethodGet, "/sites", listQuery(o), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateSite creates an empty site.
func (c *Client) CreateSite(ctx context.Context, r CreateSiteRequest) (*Site, error) {
	var out Site
	if err := c.doJSON(ctx, http.MethodPost, "/sites", nil, r, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetSite returns a site by ID or slug.
func (c *Client) GetSite(ctx context.Context, ref string) (*Site, error) {
	var out Site
	if err := c.doJSON(ctx, http.MethodGet, refPath("/sites", ref), nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateSite changes a site's title and/or slug.
func (c *Client) UpdateSite(ctx context.Context, ref string, r UpdateSiteRequest) (*Site, error) {
	var out Site
	if err := c.doJSON(ctx, http.MethodPatch, refPath("/sites", ref), nil, r, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteSite deletes a site and all its releases.
func (c *Client) DeleteSite(ctx context.Context, ref string) error {
	return c.doJSON(ctx, http.MethodDelete, refPath("/sites", ref), nil, nil, nil)
}

// ListDeploys returns the site's releases, newest first.
func (c *Client) ListDeploys(ctx context.Context, ref string) (*List[Deploy], error) {
	var out List[Deploy]
	if err := c.doJSON(ctx, http.MethodGet, refPath("/sites", ref)+"/deploys", nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

type rollbackRequest struct {
	DeployID string `json:"deploy_id,omitempty"`
}

// Rollback switches the site to deployID, or to the previous release when empty.
func (c *Client) Rollback(ctx context.Context, ref, deployID string) (*DeployResult, error) {
	var out DeployResult
	path := refPath("/sites", ref) + "/rollback"
	if err := c.doJSON(ctx, http.MethodPost, path, nil, rollbackRequest{DeployID: deployID}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListTokens returns the caller's API tokens.
func (c *Client) ListTokens(ctx context.Context) (*List[Token], error) {
	var out List[Token]
	if err := c.doJSON(ctx, http.MethodGet, "/tokens", nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
