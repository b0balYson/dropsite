package ui

import (
	"net/http"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/service"
	"github.com/b0balYson/dropsite/internal/updates"
)

// updatesSection is the id of the "Updates" settings section.
const updatesSection = "updates-h"

// updateBanner is the calm notice of a newer release in the sidebar
// (administrators only).
type updateBanner struct {
	Version string
	URL     string
}

// updatesView is the data of the section and of its status fragment.
type updatesView struct {
	service.UpdateInfo
	// JustUpdated: the last update finished and this version runs now.
	JustUpdated bool
}

// Failed is the last update's failure, while no newer one waits or runs.
func (u updatesView) Failed() *updates.Status {
	if u.Status == nil || u.Active || u.Status.State != updates.StateFailed {
		return nil
	}
	return u.Status
}

// Target is the version being installed while an update is active.
func (u updatesView) Target() string {
	if u.Active && u.Status != nil && u.Status.To != "" {
		return u.Status.To
	}
	return u.Latest
}

func newUpdatesView(info service.UpdateInfo) updatesView {
	v := updatesView{UpdateInfo: info}
	if st := info.Status; st != nil && !info.Active && st.State == updates.StateDone && st.To == info.Current {
		v.JustUpdated = true
	}
	return v
}

// updateBannerFor returns the banner for administrators while a newer
// release exists.
func (s *server) updateBannerFor(r *http.Request, rc *reqCtx) *updateBanner {
	if !rc.authed || !rc.actor.IsAdmin() {
		return nil
	}
	v, u, ok := s.svc.AvailableUpdate(r.Context())
	if !ok {
		return nil
	}
	return &updateBanner{Version: v, URL: u}
}

// checkUpdates runs the release check now ("Check now").
func (s *server) checkUpdates(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	back := "/admin/settings#" + updatesSection
	if err := s.svc.CheckUpdatesNow(r.Context(), rc.actor); err != nil {
		s.fail(w, r, rc, err, back)
		return
	}
	msg := rc.loc.T("updates.check.latest")
	if v, _, ok := s.svc.AvailableUpdate(r.Context()); ok {
		msg = rc.loc.T("updates.check.found", "version", v)
	}
	s.setFlash(w, r, flashOK, msg)
	redirect(w, r, back)
}

// requestUpdate asks the server helper to install the newer release.
func (s *server) requestUpdate(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	back := "/admin/settings#" + updatesSection
	version := r.PostFormValue("version")
	err := s.svc.RequestUpdate(r.Context(), rc.actor, version, r.PostFormValue("confirm") == "1")
	if err != nil {
		if domain.AsError(err).Code != domain.CodeInvalidInput {
			s.fail(w, r, rc, err, back)
			return
		}
		s.setFlash(w, r, flashError, rc.loc.T("updates.need_confirm"))
		redirect(w, r, back)
		return
	}
	s.setFlash(w, r, flashOK, rc.loc.T("updates.requested", "version", version))
	redirect(w, r, back)
}

// updateStatusReply is what the progress poller reads.
type updateStatusReply struct {
	Active bool   `json:"active"`
	State  string `json:"state"`
}

// updateStatus answers the progress poller (JSON) or renders the status
// part of the section (htmx and plain requests).
func (s *server) updateStatus(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	info, err := s.svc.GetUpdateInfo(r.Context(), rc.actor)
	if err != nil {
		s.failPage(w, r, rc, err)
		return
	}
	if wantsJSON(r) {
		reply := updateStatusReply{Active: info.Active}
		if info.Status != nil {
			reply.State = info.Status.State
		}
		if info.Pending {
			reply.State = updates.StateQueued
		}
		writeJSON(w, http.StatusOK, reply)
		return
	}
	s.fragment(w, r, rc, "update_status", struct{ Updates updatesView }{newUpdatesView(info)})
}
