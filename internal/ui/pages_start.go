package ui

import (
	"net/http"
	"strings"

	"github.com/b0balYson/dropsite/internal/service"
	"github.com/b0balYson/dropsite/internal/setupcheck"
)

// requiredSteps is the number of checklist steps that make a server ready:
// domain, HTTPS, a connected chat and a first site.
const requiredSteps = 4

// startData feeds the first-run checklist. Checks is nil until the network
// checks have run: the page then shows a placeholder that loads them.
type startData struct {
	Progress service.SetupProgress
	Checks   *setupcheck.Report
	Domain   string
	APIHost  string
	// Done counts finished required steps; it is only meaningful with Checks.
	Done  int
	Total int
}

// AllDone reports a ready server: every required step is finished.
func (d startData) AllDone() bool { return d.Checks != nil && d.Done == d.Total }

// StepState is the state of a local step.
func (d startData) StepState(done bool) setupcheck.State {
	if done {
		return setupcheck.Done
	}
	return setupcheck.Todo
}

// Next names the first unfinished required step ("" when all are done).
func (d startData) Next() string {
	switch {
	case d.Checks == nil:
		return ""
	case d.Checks.Domain.State != setupcheck.Done:
		return "domain"
	case d.Checks.HTTPS.State != setupcheck.Done:
		return "https"
	case !d.Progress.Chat:
		return "chat"
	case !d.Progress.Site:
		return "site"
	}
	return ""
}

// Unknown is true when the server's addresses are not configured.
func (d startData) Unknown() bool {
	return d.Checks != nil && len(d.Checks.Domain.Records) > 0 && d.Checks.Domain.Records[0].Value == ""
}

func (s *server) startData(r *http.Request, rc *reqCtx, checks *setupcheck.Report) (startData, error) {
	p, err := s.svc.SetupProgress(r.Context(), rc.actor)
	if err != nil {
		return startData{}, err
	}
	cfg := s.svc.Config()
	d := startData{Progress: p, Checks: checks, Domain: cfg.BaseDomain, APIHost: cfg.APIHost, Total: requiredSteps}
	if checks != nil {
		for _, ok := range []bool{checks.Domain.State == setupcheck.Done, checks.HTTPS.State == setupcheck.Done, p.Chat, p.Site} {
			if ok {
				d.Done++
			}
		}
	}
	return d, nil
}

// startPage shows the checklist; the network checks load separately unless
// a fresh report is cached, so the page never waits for DNS or HTTPS.
func (s *server) startPage(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	var checks *setupcheck.Report
	if r.URL.Query().Get("fresh") == "1" {
		// "Check again" without JavaScript: run the checks in the request.
		rep, err := s.svc.SetupChecks(r.Context(), rc.actor, true)
		if err != nil {
			s.failPage(w, r, rc, err)
			return
		}
		checks = &rep
	} else if rep, ok := s.svc.CachedSetupChecks(rc.actor); ok {
		checks = &rep
	}
	data, err := s.startData(r, rc, checks)
	if err != nil {
		s.failPage(w, r, rc, err)
		return
	}
	s.page(w, r, rc, http.StatusOK, "start", "start", "setup.title", data)
}

// startChecks runs the network checks and returns the checklist fragment;
// fresh=1 ("Check again") bypasses the cache.
func (s *server) startChecks(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	data, ok := s.runChecks(w, r, rc)
	if !ok {
		return
	}
	s.fragment(w, r, rc, "start_checks", data)
}

// startSummary is the compact checklist card of the sites list.
func (s *server) startSummary(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	data, ok := s.runChecks(w, r, rc)
	if !ok {
		return
	}
	if data.Progress.Hidden {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	s.fragment(w, r, rc, "start_summary", data)
}

func (s *server) runChecks(w http.ResponseWriter, r *http.Request, rc *reqCtx) (startData, bool) {
	rep, err := s.svc.SetupChecks(r.Context(), rc.actor, r.URL.Query().Get("fresh") == "1")
	if err != nil {
		s.failPage(w, r, rc, err)
		return startData{}, false
	}
	data, err := s.startData(r, rc, &rep)
	if err != nil {
		s.failPage(w, r, rc, err)
		return startData{}, false
	}
	return data, true
}

// hideStart and showStart dismiss or restore the checklist for all admins.
func (s *server) hideStart(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	s.setStartHidden(w, r, rc, true, "setup.hidden", "/")
}

func (s *server) showStart(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	s.setStartHidden(w, r, rc, false, "setup.shown", "/start")
}

func (s *server) setStartHidden(w http.ResponseWriter, r *http.Request, rc *reqCtx, hidden bool, msg, target string) {
	if err := s.svc.SetSetupHidden(r.Context(), rc.actor, hidden); err != nil {
		s.fail(w, r, rc, err, "/start")
		return
	}
	s.setFlash(w, r, flashOK, rc.loc.T(msg))
	redirect(w, r, target)
}

// recordHost is the full name of a record: "@" is the domain itself.
func recordHost(name, domain string) string {
	if name == "@" {
		return domain
	}
	return strings.Replace(name, "*", "*."+domain, 1)
}
