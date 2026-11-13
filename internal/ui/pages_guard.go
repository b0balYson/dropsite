package ui

import (
	"context"
	"net/http"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/service"
)

// Abuse protection in the general settings (administrators only): the
// caddy-defender switches and the list of IP bans.

const guardSection = "guard-h"

type guardView struct {
	Defender service.DefenderStatus
	Bans     []domain.IPBan
}

func (s *server) guardView(ctx context.Context, a service.Actor) (guardView, error) {
	d, err := s.svc.GetDefender(ctx, a)
	if err != nil {
		return guardView{}, err
	}
	bans, err := s.svc.ActiveBans(ctx, a)
	if err != nil {
		return guardView{}, err
	}
	return guardView{Defender: d, Bans: bans}, nil
}

func (s *server) saveGuard(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	back := "/admin/settings#" + guardSection
	st := service.DefenderStatus{Enabled: r.PostFormValue("defender") == "1", Strict: r.PostFormValue("strict") == "1"}
	if err := s.svc.SetDefender(r.Context(), rc.actor, st); err != nil {
		s.fail(w, r, rc, err, back)
		return
	}
	s.setFlash(w, r, flashOK, rc.loc.T("guard.saved"))
	redirect(w, r, back)
}

func (s *server) clearBan(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	back := "/admin/settings#" + guardSection
	key := r.PostFormValue("key")
	if err := s.svc.ClearBan(r.Context(), rc.actor, key); err != nil {
		s.fail(w, r, rc, err, back)
		return
	}
	s.setFlash(w, r, flashOK, rc.loc.T("guard.unbanned", "ip", key))
	redirect(w, r, back)
}
