package ui

import "net/http"

// sitePublishSettings saves the "Optimize at publish time" switch.
func (s *server) sitePublishSettings(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	ref := r.PathValue("ref")
	v, err := s.svc.SetPublishSettings(r.Context(), rc.actor, ref, r.PostFormValue("optimize") == "1")
	if err != nil {
		s.fail(w, r, rc, err, sectionPath(ref, secSettings))
		return
	}
	s.setFlash(w, r, flashOK, rc.loc.T("publish.saved"))
	redirect(w, r, sectionPath(v.SiteID, secSettings)+"#optimize-h")
}
