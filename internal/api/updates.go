package api

import (
	"net/http"
	"time"

	"github.com/b0balYson/dropsite/internal/service"
)

// Version facts for API clients and the local MCP server (check_updates).
// Both endpoints are read-only: no API path can request an update; that
// takes an administrator signed in to the web interface.

type updatesDTO struct {
	CurrentVersion  string     `json:"current_version"`
	LatestVersion   string     `json:"latest_version,omitempty"`
	UpdateAvailable bool       `json:"update_available"`
	ReleaseNotesURL string     `json:"release_notes_url,omitempty"`
	CheckedAt       *time.Time `json:"checked_at,omitempty"`
	CheckEnabled    bool       `json:"check_enabled"`
	IsAdmin         bool       `json:"is_admin"`
	// Administrators only.
	HelperInstalled *bool  `json:"helper_installed,omitempty"`
	UpdatePanelURL  string `json:"update_panel_url,omitempty"`
}

func toUpdates(f service.UpdateFacts) updatesDTO {
	out := updatesDTO{
		CurrentVersion: f.Current, LatestVersion: f.Latest, UpdateAvailable: f.Available,
		ReleaseNotesURL: f.ReleaseURL, CheckEnabled: f.CheckEnabled, IsAdmin: f.IsAdmin,
	}
	if !f.CheckedAt.IsZero() {
		t := f.CheckedAt.UTC()
		out.CheckedAt = &t
	}
	if f.IsAdmin {
		h := f.HelperPresent
		out.HelperInstalled = &h
		out.UpdatePanelURL = f.PanelURL
	}
	return out
}

func (a *api) getUpdates(w http.ResponseWriter, r *http.Request) error {
	f, err := a.svc.GetUpdateFacts(r.Context(), actorFrom(r.Context()), false)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, toUpdates(f))
	return nil
}

// checkUpdates runs the release check now for administrators (rate
// limited, shared with the web interface); others get the stored facts.
func (a *api) checkUpdates(w http.ResponseWriter, r *http.Request) error {
	f, err := a.svc.GetUpdateFacts(r.Context(), actorFrom(r.Context()), true)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, toUpdates(f))
	return nil
}
