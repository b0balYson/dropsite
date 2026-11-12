package api

import (
	"net/http"
)

// The root domain with an explicit confirmation: the same service methods
// as the MCP tools set_root_site and clear_root_site, so an assistant using
// the API (the local MCP server of the CLI) gets the same safeguard. The
// service allows administrators only and checks confirm.

type rootSiteDTO struct {
	RootURL string  `json:"root_url"`
	Site    siteDTO `json:"site"`
}

type setRootSiteRequest struct {
	Site    string `json:"site"`
	Confirm string `json:"confirm"`
}

func (a *api) rootURL() string { return a.svc.SiteURL(a.svc.Config().BaseDomain) }

func (a *api) setRootSite(w http.ResponseWriter, r *http.Request) error {
	var req setRootSiteRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return err
	}
	v, err := a.svc.SetRootSite(r.Context(), actorFrom(r.Context()), req.Site, req.Confirm)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, rootSiteDTO{RootURL: a.rootURL(), Site: toSite(v)})
	return nil
}

func (a *api) clearRootSite(w http.ResponseWriter, r *http.Request) error {
	v, err := a.svc.ClearRootSite(r.Context(), actorFrom(r.Context()), r.URL.Query().Get("confirm"))
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, rootSiteDTO{RootURL: a.rootURL(), Site: toSite(v)})
	return nil
}
