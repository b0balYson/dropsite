package api

import (
	"net/http"

	"github.com/b0balYson/dropsite/internal/quality"
)

// deployReportDTO is a release with its full quality report.
type deployReportDTO struct {
	deployDTO
	Quality *quality.Report `json:"quality"`
}

func (a *api) getDeploy(w http.ResponseWriter, r *http.Request) error {
	v, err := a.svc.GetDeployReport(r.Context(), actorFrom(r.Context()), r.PathValue("ref"), r.PathValue("id"))
	if err != nil {
		return err
	}
	cur := v.Site.Site.CurrentDeployID
	writeJSON(w, http.StatusOK, deployReportDTO{
		deployDTO: toDeploy(v.Deploy, cur, a.svc.PreviewURL(v.Deploy)), Quality: v.Report,
	})
	return nil
}
