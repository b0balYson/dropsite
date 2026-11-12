package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"time"

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
	Title     json.RawMessage `json:"title"`
	Slug      json.RawMessage `json:"slug"`
	Noindex   json.RawMessage `json:"noindex"`
	Protected json.RawMessage `json:"protected"`
	Password  json.RawMessage `json:"password"`
	ExpiresAt json.RawMessage `json:"expires_at"`
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
	var err error
	if u.Noindex, err = optionalBool(req.Noindex, "noindex"); err != nil {
		return u, err
	}
	if u.Protected, err = optionalBool(req.Protected, "protected"); err != nil {
		return u, err
	}
	if req.Password != nil {
		var pw string
		if bytes.Equal(req.Password, jsonNull) || json.Unmarshal(req.Password, &pw) != nil {
			return u, invalidField("password", "password must be a string")
		}
		u.Password = &pw
	}
	if req.ExpiresAt != nil {
		exp, err := parseExpiry(req.ExpiresAt)
		if err != nil {
			return u, err
		}
		u.Expiry = &service.Expiry{At: exp}
	}
	return u, nil
}

// optionalBool decodes a field that, when present, must be true or false.
func optionalBool(raw json.RawMessage, field string) (*bool, error) {
	if raw == nil {
		return nil, nil
	}
	var v bool
	if bytes.Equal(raw, jsonNull) || json.Unmarshal(raw, &v) != nil {
		return nil, invalidField(field, field+" must be true or false")
	}
	return &v, nil
}

// parseExpiry decodes an RFC 3339 time or null (no expiry).
func parseExpiry(raw json.RawMessage) (*time.Time, error) {
	if bytes.Equal(raw, jsonNull) {
		return nil, nil
	}
	var v string
	if json.Unmarshal(raw, &v) != nil {
		return nil, invalidField("expires_at", "expires_at must be an RFC 3339 time or null")
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return nil, invalidField("expires_at", "expires_at must be an RFC 3339 time or null")
	}
	t = t.UTC()
	return &t, nil
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
