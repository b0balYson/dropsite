package api

import (
	"encoding/base64"
	"net/http"

	"github.com/b0balYson/dropsite/internal/designref"
)

// Design references (change design-references).

func (a *api) referenceRoutes() []route {
	return []route{
		{Method: http.MethodPost, Path: "/references/inspect", handle: a.inspectReference},
	}
}

type referenceRequest struct {
	URL string `json:"url"`
}

type referenceShotDTO struct {
	Status   string `json:"status"`
	Width    int    `json:"width,omitempty"`
	Height   int    `json:"height,omitempty"`
	ImagePNG string `json:"image_png,omitempty"`
}

type referenceDTO struct {
	designref.Brief
	Screenshot referenceShotDTO `json:"screenshot"`
}

func (a *api) inspectReference(w http.ResponseWriter, r *http.Request) error {
	var req referenceRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return err
	}
	if req.URL == "" {
		return invalidField("url", "url is required")
	}
	ref, err := a.svc.InspectReference(r.Context(), actorFrom(r.Context()), req.URL)
	if err != nil {
		return err
	}
	shot := referenceShotDTO{Status: ref.Shot.Status, Width: ref.Shot.Width, Height: ref.Shot.Height}
	if len(ref.Shot.PNG) > 0 {
		shot.ImagePNG = base64.StdEncoding.EncodeToString(ref.Shot.PNG)
	}
	writeJSON(w, http.StatusOK, referenceDTO{Brief: ref.Brief, Screenshot: shot})
	return nil
}
