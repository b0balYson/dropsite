package client

import (
	"context"
	"net/http"
)

// AttachedForms are the forms and delivery channels of a site.
type AttachedForms struct {
	Names           []string `json:"names"`
	Channels        int      `json:"channels"`
	WorkingChannels int      `json:"working_channels"`
	RequireAltcha   bool     `json:"require_altcha"`
}

// AttachedCounters are a site's own analytics counters and the global ones.
type AttachedCounters struct {
	Own    CounterIDs `json:"own"`
	Global CounterIDs `json:"global"`
}

// SiteAttachments is what is attached to a site besides its files.
type SiteAttachments struct {
	SiteID   string           `json:"site_id"`
	Forms    AttachedForms    `json:"forms"`
	Counters AttachedCounters `json:"counters"`
}

// SiteAttachments returns what is attached to a site.
func (c *Client) SiteAttachments(ctx context.Context, ref string) (*SiteAttachments, error) {
	var out SiteAttachments
	if err := c.doJSON(ctx, http.MethodGet, sitePath(ref)+"/attachments", nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
