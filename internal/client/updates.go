package client

import (
	"context"
	"net/http"
	"time"
)

// Updates are the version facts of the server (GET /updates).
type Updates struct {
	CurrentVersion  string     `json:"current_version"`
	LatestVersion   string     `json:"latest_version,omitempty"`
	UpdateAvailable bool       `json:"update_available"`
	ReleaseNotesURL string     `json:"release_notes_url,omitempty"`
	CheckedAt       *time.Time `json:"checked_at,omitempty"`
	CheckEnabled    bool       `json:"check_enabled"`
	IsAdmin         bool       `json:"is_admin"`
	HelperInstalled *bool      `json:"helper_installed,omitempty"`
	UpdatePanelURL  string     `json:"update_panel_url,omitempty"`
}

// Updates returns the stored version facts; refresh asks the server to run
// the release check first (administrators; rate limited).
func (c *Client) Updates(ctx context.Context, refresh bool) (*Updates, error) {
	var out Updates
	method, path := http.MethodGet, "/updates"
	if refresh {
		method, path = http.MethodPost, "/updates/check"
	}
	if err := c.doJSON(ctx, method, path, nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
