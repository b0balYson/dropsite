package ui

import (
	"net/http"

	"github.com/b0balYson/dropsite/internal/service"
)

// qualityData is the quality report screen of one release.
type qualityData struct {
	V service.DeployReportView
}

// qualityPage shows the quality report of a release.
func (s *server) qualityPage(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	v, err := s.svc.GetDeployReport(r.Context(), rc.actor, r.PathValue("ref"), r.PathValue("id"))
	if err != nil {
		s.failPage(w, r, rc, err)
		return
	}
	s.page(w, r, rc, http.StatusOK, "quality", "sites", "quality.title", qualityData{V: v})
}
