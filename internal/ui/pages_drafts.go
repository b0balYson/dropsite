package ui

import (
	"net/http"

	"github.com/b0balYson/dropsite/internal/service"
)

// Draft releases (change staged-publish).

// draftField is the upload form field that saves the archive as a draft.
// The form sends it before the archive.
const draftField = "draft"

// draftSaved answers an upload saved as a draft: the site page shows it in
// its own block, so a script reloads the page.
func (s *server) draftSaved(w http.ResponseWriter, r *http.Request, rc *reqCtx, res service.DeployResult) {
	msg := rc.loc.T("draft.saved")
	s.setFlash(w, r, flashOK, msg)
	if wantsJSON(r) {
		writeJSON(w, http.StatusOK, deployReply{OK: true, URL: res.Site.Draft.PreviewURL, DeployID: res.Deploy.ID,
			Message: msg, Reload: true})
		return
	}
	redirect(w, r, sitePath(res.Site.Site.ID))
}

func (s *server) publishDraft(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	ref := r.PathValue("ref")
	res, err := s.svc.PublishDraft(r.Context(), rc.actor, ref)
	if err != nil {
		s.fail(w, r, rc, err, sitePath(ref))
		return
	}
	s.setFlash(w, r, flashDeployed, rc.loc.T("draft.published"))
	redirect(w, r, sitePath(res.Site.Site.ID))
}

func (s *server) discardDraft(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	ref := r.PathValue("ref")
	v, err := s.svc.DiscardDraft(r.Context(), rc.actor, ref)
	if err != nil {
		s.fail(w, r, rc, err, sitePath(ref))
		return
	}
	s.setFlash(w, r, flashOK, rc.loc.T("draft.discarded"))
	redirect(w, r, sitePath(v.Site.ID))
}
