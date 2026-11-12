package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/b0balYson/dropsite/internal/service"
)

// Draft releases (change staged-publish).

func (a *api) draftRoutes() []route {
	return []route{
		{Method: http.MethodPost, Path: "/sites/{ref}/draft/publish", handle: a.publishDraft},
		{Method: http.MethodDelete, Path: "/sites/{ref}/draft", handle: a.discardDraft},
	}
}

type draftDTO struct {
	DeployID  string    `json:"deploy_id"`
	CreatedAt time.Time `json:"created_at"`
	// PreviewURL is the site's stable draft preview address; null only when
	// an older slug owns the name.
	PreviewURL *string `json:"preview_url"`
}

func toDraft(v service.SiteView) *draftDTO {
	if v.Draft == nil {
		return nil
	}
	return &draftDTO{DeployID: v.Draft.Deploy.ID, CreatedAt: utc(v.Draft.CreatedAt), PreviewURL: optional(v.Draft.PreviewURL)}
}

// markDraft flags the site's draft in a release list.
func markDraft(ds []deployDTO, v service.SiteView) []deployDTO {
	out := make([]deployDTO, len(ds))
	for i, d := range ds {
		d.Draft = v.Draft != nil && d.ID == v.Draft.Deploy.ID
		out[i] = d
	}
	return out
}

// publishOptions reads ?draft=true of a publication.
func publishOptions(r *http.Request) (service.PublishOptions, error) {
	raw := r.URL.Query().Get("draft")
	if raw == "" {
		return service.PublishOptions{}, nil
	}
	draft, err := strconv.ParseBool(raw)
	if err != nil {
		return service.PublishOptions{}, invalidField("draft", "draft must be true or false")
	}
	return service.PublishOptions{Draft: draft}, nil
}

func (a *api) publishDraft(w http.ResponseWriter, r *http.Request) error {
	res, err := a.svc.PublishDraft(r.Context(), actorFrom(r.Context()), r.PathValue("ref"))
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, toDeployResult(res))
	return nil
}

func (a *api) discardDraft(w http.ResponseWriter, r *http.Request) error {
	v, err := a.svc.DiscardDraft(r.Context(), actorFrom(r.Context()), r.PathValue("ref"))
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, toSite(v))
	return nil
}
