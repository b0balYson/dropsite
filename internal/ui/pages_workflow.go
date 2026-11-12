package ui

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/service"
)

// Site copies, templates, scheduled publishes and screenshots (change
// chat-workflow).

// localTimeLayout is the value format of <input type="datetime-local">.
const localTimeLayout = "2006-01-02T15:04"

// maxTZOffsetMinutes bounds the browser's time zone offset.
const maxTZOffsetMinutes = 14 * 60

type workflowView struct {
	Schedules []service.ScheduleView
	// Ready lists the ready releases that are not being served.
	Ready []deployRow
	// Shots is true when screenshots are available.
	Shots bool
	// MinLocal is the earliest time the schedule form accepts (UTC).
	MinLocal string
}

func (s *server) workflowView(r *http.Request, rc *reqCtx, data siteData) (workflowView, error) {
	scheds, err := s.svc.ListSchedules(r.Context(), rc.actor, data.V.Site.ID)
	if err != nil {
		return workflowView{}, err
	}
	v := workflowView{Schedules: scheds, Shots: s.svc.ScreenshotsEnabled(),
		MinLocal: s.svc.Now().Format(localTimeLayout)}
	for _, d := range data.Deploys {
		if !d.Current && d.Status == domain.DeployReady {
			v.Ready = append(v.Ready, d)
		}
	}
	return v, nil
}

func (s *server) createFromTemplate(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	res, err := s.svc.CreateSiteFromTemplate(r.Context(), rc.actor, r.PostFormValue("template"),
		r.PostFormValue("title"), normSlug(r.PostFormValue("slug")), r.PostFormValue("lang"), domain.SourceUI)
	if err != nil {
		s.fail(w, r, rc, err, "/#templates")
		return
	}
	s.setFlash(w, r, flashDeployed, rc.loc.T("workflow.template.done"))
	redirect(w, r, sitePath(res.Site.Site.ID))
}

func (s *server) cloneSite(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	ref := r.PathValue("ref")
	res, err := s.svc.CloneSite(r.Context(), rc.actor, ref, r.PostFormValue("title"),
		normSlug(r.PostFormValue("slug")), domain.SourceUI)
	if err != nil {
		s.fail(w, r, rc, err, sitePath(ref))
		return
	}
	s.setFlash(w, r, flashDeployed, rc.loc.T("workflow.clone.done"))
	redirect(w, r, sitePath(res.Site.Site.ID))
}

// browserTime reads a datetime-local value in the browser's time zone; the
// offset is JavaScript's getTimezoneOffset() (minutes, UTC − local), sent
// by workflow.js. Without it the time is taken as UTC.
func browserTime(local, offset string) (time.Time, bool) {
	t, err := time.ParseInLocation(localTimeLayout, strings.TrimSpace(local), time.UTC)
	if err != nil {
		return time.Time{}, false
	}
	if offset = strings.TrimSpace(offset); offset != "" {
		m, err := strconv.Atoi(offset)
		if err != nil || m < -maxTZOffsetMinutes || m > maxTZOffsetMinutes {
			return time.Time{}, false
		}
		t = t.Add(time.Duration(m) * time.Minute)
	}
	return t, true
}

func (s *server) scheduleRelease(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	ref := r.PathValue("ref")
	at, ok := browserTime(r.PostFormValue("publish_at"), r.PostFormValue("tz_offset"))
	if !ok {
		s.setFlash(w, r, flashError, rc.loc.T("workflow.schedule.badtime"))
		redirect(w, r, sitePath(ref)+"#workflow-h")
		return
	}
	if _, err := s.svc.ScheduleRelease(r.Context(), rc.actor, ref, r.PostFormValue("deploy"), at); err != nil {
		s.fail(w, r, rc, err, sitePath(ref)+"#workflow-h")
		return
	}
	s.setFlash(w, r, flashOK, rc.loc.T("workflow.schedule.done"))
	redirect(w, r, sitePath(ref)+"#workflow-h")
}

func (s *server) cancelSchedule(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	ref := r.PathValue("ref")
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.notFound(w, r, rc)
		return
	}
	if _, err := s.svc.CancelSchedule(r.Context(), rc.actor, id); err != nil {
		s.fail(w, r, rc, err, sitePath(ref)+"#workflow-h")
		return
	}
	s.setFlash(w, r, flashOK, rc.loc.T("workflow.schedule.cancelled"))
	redirect(w, r, sitePath(ref)+"#workflow-h")
}

func writePNG(w http.ResponseWriter, png []byte) {
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "private, max-age=300")
	w.Header().Set("Content-Length", strconv.Itoa(len(png)))
	_, _ = w.Write(png)
}

// pngFailed answers an image request that failed: 404 for an invisible site
// or release, otherwise the status of the error, with a short text.
func (s *server) pngFailed(w http.ResponseWriter, r *http.Request, rc *reqCtx, err error) {
	if isNotFound(err) {
		s.notFound(w, r, rc)
		return
	}
	msg, status := s.describe(rc, err)
	if domain.AsError(err).Code == domain.CodeRendererUnavailable {
		status = http.StatusServiceUnavailable
	}
	http.Error(w, msg, status)
}

func (s *server) screenshotPNG(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	q := r.URL.Query()
	shot, err := s.svc.Screenshot(r.Context(), rc.actor, r.PathValue("ref"), q.Get("deploy"), q.Get("device"))
	if err != nil {
		s.pngFailed(w, r, rc, err)
		return
	}
	writePNG(w, shot.PNG)
}

func (s *server) comparePNG(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	q := r.URL.Query()
	d, err := s.svc.CompareReleases(r.Context(), rc.actor, r.PathValue("ref"), q.Get("from"), q.Get("to"), q.Get("device"))
	if err != nil {
		s.pngFailed(w, r, rc, err)
		return
	}
	w.Header().Set("X-Dropsite-Changed-Percent", strconv.FormatFloat(d.ChangedPercent, 'f', 2, 64))
	writePNG(w, d.PNG)
}
