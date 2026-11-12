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

type attachedCollectionDTO struct {
	Name      string `json:"name"`
	Mode      string `json:"mode"`
	Documents int    `json:"documents"`
	Pending   int    `json:"pending"`
}

// siteAttachmentsDTO is what is attached to a site.
type siteAttachmentsDTO struct {
	SiteID         string                  `json:"site_id"`
	Forms          attachedFormsDTO        `json:"forms"`
	Counters       attachedCountersDTO     `json:"counters"`
	Collections    []attachedCollectionDTO `json:"collections"`
	ProxyEndpoints []string                `json:"proxy_endpoints"`
}

func toSiteAttachments(at service.SiteAttachments) siteAttachmentsDTO {
	cols := make([]attachedCollectionDTO, 0, len(at.Collections))
	for _, c := range at.Collections {
		cols = append(cols, attachedCollectionDTO{Name: c.Name, Mode: string(c.Mode), Documents: c.Documents,
			Pending: c.Pending})
	}
	eps := at.ProxyEndpoints
	if eps == nil {
		eps = []string{}
	}
	return siteAttachmentsDTO{
		Collections: cols, ProxyEndpoints: eps,
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
