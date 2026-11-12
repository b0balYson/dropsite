package api

import (
	"bytes"
	"encoding/json"
	"net/http"

	"github.com/b0balYson/dropsite/internal/service"
)

func (a *api) me(w http.ResponseWriter, r *http.Request) error {
	u, lim, n, err := a.svc.Me(r.Context(), actorFrom(r.Context()))
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, meDTO{User: toUser(u), Limits: toLimits(lim), SiteCount: n})
	return nil
}

func (a *api) listSites(w http.ResponseWriter, r *http.Request) error {
	p, err := page(r.URL.Query())
	if err != nil {
		return err
	}
	rows, total, err := a.svc.ListSites(r.Context(), actorFrom(r.Context()), p)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, newList(toSiteItems(rows), total))
	return nil
}

type createSiteRequest struct {
	Title string `json:"title"`
	Slug  string `json:"slug"`
}

func (a *api) createSite(w http.ResponseWriter, r *http.Request) error {
	var req createSiteRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return err
	}
	v, err := a.svc.CreateSite(r.Context(), actorFrom(r.Context()), req.Title, req.Slug)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, toSite(v))
	return nil
}

func (a *api) getSite(w http.ResponseWriter, r *http.Request) error {
	v, err := a.svc.GetSite(r.Context(), actorFrom(r.Context()), r.PathValue("ref"))
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, toSite(v))
	return nil
}

// updateSiteRequest keeps raw values to tell a missing field from null.
type updateSiteRequest struct {
	Title json.RawMessage `json:"title"`
	Slug  json.RawMessage `json:"slug"`
}

var jsonNull = []byte("null")

func (req updateSiteRequest) update() (service.SiteUpdate, error) {
	var u service.SiteUpdate
	if req.Title != nil {
		var title string
		if bytes.Equal(req.Title, jsonNull) || json.Unmarshal(req.Title, &title) != nil {
			return u, invalidField("title", "title must be a string")
		}
		u.Title = &title
	}
	if req.Slug != nil {
		slug := ""
		if !bytes.Equal(req.Slug, jsonNull) && json.Unmarshal(req.Slug, &slug) != nil {
			return u, invalidField("slug", "slug must be a string or null")
		}
		u.Slug = &slug
	}
	return u, nil
}

func (a *api) updateSite(w http.ResponseWriter, r *http.Request) error {
	var req updateSiteRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return err
	}
	u, err := req.update()
	if err != nil {
		return err
	}
	v, err := a.svc.UpdateSite(r.Context(), actorFrom(r.Context()), r.PathValue("ref"), u)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, toSite(v))
	return nil
}

func (a *api) deleteSite(w http.ResponseWriter, r *http.Request) error {
	if err := a.svc.DeleteSite(r.Context(), actorFrom(r.Context()), r.PathValue("ref")); err != nil {
		return err
	}
	noContent(w)
	return nil
}
