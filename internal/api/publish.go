package api

import (
	"encoding/json"
	"net/http"

	"github.com/b0balYson/dropsite/internal/service"
)

type publishSettingsDTO struct {
	SiteID              string `json:"site_id"`
	Optimize            bool   `json:"optimize"`
	InstallationEnabled bool   `json:"installation_enabled"`
	Effective           bool   `json:"effective"`
}

func toPublishSettings(v service.PublishSettingsView) publishSettingsDTO {
	return publishSettingsDTO{
		SiteID: v.SiteID, Optimize: v.Optimize, InstallationEnabled: v.InstallationEnabled, Effective: v.Effective,
	}
}

func (a *api) getPublishSettings(w http.ResponseWriter, r *http.Request) error {
	v, err := a.svc.GetPublishSettings(r.Context(), actorFrom(r.Context()), r.PathValue("ref"))
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, toPublishSettings(v))
	return nil
}

type updatePublishSettingsRequest struct {
	Optimize json.RawMessage `json:"optimize"`
}

func (a *api) updatePublishSettings(w http.ResponseWriter, r *http.Request) error {
	var req updatePublishSettingsRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return err
	}
	on, err := optionalBool(req.Optimize, "optimize")
	if err != nil {
		return err
	}
	if on == nil {
		return invalidField("optimize", "optimize must be true or false")
	}
	v, err := a.svc.SetPublishSettings(r.Context(), actorFrom(r.Context()), r.PathValue("ref"), *on)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, toPublishSettings(v))
	return nil
}
