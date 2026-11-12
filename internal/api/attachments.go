package api

import (
	"net/http"

	"github.com/b0balYson/dropsite/internal/service"
)

type attachedFormsDTO struct {
	Names           []string `json:"names"`
	Channels        int      `json:"channels"`
	WorkingChannels int      `json:"working_channels"`
	RequireAltcha   bool     `json:"require_altcha"`
}

type attachedCountersDTO struct {
	Own    counterIDsDTO `json:"own"`
	Global counterIDsDTO `json:"global"`
}

// siteAttachmentsDTO is what is attached to a site.
//
// Extension point (change site-collections): add the site's collections
// here once service.SiteAttachments carries them.
type siteAttachmentsDTO struct {
	SiteID   string              `json:"site_id"`
	Forms    attachedFormsDTO    `json:"forms"`
	Counters attachedCountersDTO `json:"counters"`
}

func toSiteAttachments(at service.SiteAttachments) siteAttachmentsDTO {
	return siteAttachmentsDTO{
		SiteID: at.SiteID,
		Forms: attachedFormsDTO{Names: at.FormNames, Channels: at.Channels, WorkingChannels: at.WorkingChannels,
			RequireAltcha: at.RequireAltcha},
		Counters: attachedCountersDTO{Own: toCounterIDs(at.Counters.Own), Global: toCounterIDs(at.Counters.Global)},
	}
}

func (a *api) getSiteAttachments(w http.ResponseWriter, r *http.Request) error {
	at, err := a.svc.SiteAttachments(r.Context(), actorFrom(r.Context()), r.PathValue("ref"))
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, toSiteAttachments(at))
	return nil
}
