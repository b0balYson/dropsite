package ui

import (
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/service"
)

// recentSubmissions is how many submissions the site page shows.
const recentSubmissions = 5

// formsSummary is the "Submissions" block of the site page.
type formsSummary struct {
	Channels      []domain.FormChannel
	Recent        []domain.FormSubmission
	Total         int
	RequireAltcha bool
}

func (s *server) formsSummary(r *http.Request, rc *reqCtx, siteID string) (formsSummary, error) {
	chs, err := s.svc.ListFormChannels(r.Context(), rc.actor, siteID)
	if err != nil {
		return formsSummary{}, err
	}
	subs, total, err := s.svc.ListFormSubmissions(r.Context(), rc.actor, siteID, "", service.Page{Limit: recentSubmissions})
	if err != nil {
		return formsSummary{}, err
	}
	st, err := s.svc.GetFormSiteSettings(r.Context(), rc.actor, siteID)
	if err != nil {
		return formsSummary{}, err
	}
	return formsSummary{Channels: chs, Recent: subs, Total: total, RequireAltcha: st.RequireAltcha}, nil
}

// issuedChannel is a value shown once after creating or rotating a channel.
type issuedChannel struct {
	Kind   string
	Secret string
	Link   string
}

type formsPageData struct {
	V          service.SiteView
	Summary    formsSummary
	Subs       []domain.FormSubmission
	Pager      pager
	FormNames  []string
	Form       string
	Available  service.FormAvailability
	Issued     *issuedChannel
	Error      string
	ErrorKind  string
	FormTarget string
	Snippet    string
}

// formSnippet is a ready-to-paste form for the site's own pages.
const formSnippet = `<form action="/_form/lead" method="post">
  <input name="name" placeholder="Name" required>
  <input name="phone" type="tel" placeholder="Phone" required>
  <input name="_hp" tabindex="-1" autocomplete="off" aria-hidden="true" style="position:absolute;left:-10000px">
  <input type="hidden" name="_next" value="/thanks.html">
  <altcha-widget challenge="/_form/challenge"></altcha-widget>
  <button type="submit">Send</button>
</form>
<script async defer src="/_dropsite/altcha.js" type="module"></script>`

func formsPath(id string) string { return sitePath(id) + "/forms" }

func (s *server) loadFormsPage(r *http.Request, rc *reqCtx) (formsPageData, error) {
	ref := r.PathValue("ref")
	v, err := s.svc.GetSite(r.Context(), rc.actor, ref)
	if err != nil {
		return formsPageData{}, err
	}
	sum, err := s.formsSummary(r, rc, v.Site.ID)
	if err != nil {
		return formsPageData{}, err
	}
	n, p := pageParam(r)
	form := r.URL.Query().Get("form")
	subs, total, err := s.svc.ListFormSubmissions(r.Context(), rc.actor, v.Site.ID, form, p)
	if err != nil {
		return formsPageData{}, err
	}
	names, err := s.svc.ListFormNames(r.Context(), rc.actor, v.Site.ID)
	if err != nil {
		return formsPageData{}, err
	}
	avail, err := s.svc.FormChannelAvailability(r.Context())
	if err != nil {
		return formsPageData{}, err
	}
	return formsPageData{
		V: v, Summary: sum, Subs: subs, Pager: newPager(formsPath(v.Site.ID), r.URL.Query(), n, total),
		FormNames: names, Form: form, Available: avail, Snippet: formSnippet,
	}, nil
}

func (s *server) formsPage(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	s.renderForms(w, r, rc, http.StatusOK, nil, "", "", "")
}

func (s *server) renderForms(w http.ResponseWriter, r *http.Request, rc *reqCtx, status int, issued *issuedChannel, errMsg, errKind, target string) {
	data, err := s.loadFormsPage(r, rc)
	if err != nil {
		s.failPage(w, r, rc, err)
		return
	}
	data.Issued, data.Error, data.ErrorKind, data.FormTarget = issued, errMsg, errKind, target
	s.sitePageOf(w, r, rc, status, "forms", secForms, "forms.title", data.V, data)
}

func (s *server) addChannel(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	kind, target := r.PostFormValue("kind"), r.PostFormValue("target")
	res, err := s.svc.AddFormChannel(r.Context(), rc.actor, r.PathValue("ref"), domain.ChannelKind(kind), target)
	if err != nil {
		if isNotFound(err) {
			s.notFound(w, r, rc)
			return
		}
		msg, status := s.describe(rc, err)
		s.renderForms(w, r, rc, status, nil, msg, kind, target)
		return
	}
	if res.Secret != "" || res.Link != "" {
		// Shown once, straight from this response: never stored in a cookie.
		s.renderForms(w, r, rc, http.StatusOK, &issuedChannel{Kind: kind, Secret: res.Secret, Link: res.Link}, "", "", "")
		return
	}
	s.setFlash(w, r, flashOK, rc.loc.T("forms.channel.codesent"))
	redirect(w, r, formsPath(res.Channel.SiteID))
}

func (s *server) channelAction(w http.ResponseWriter, r *http.Request, rc *reqCtx, act func(id int64) error, okKey string) {
	ref := r.PathValue("ref")
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.notFound(w, r, rc)
		return
	}
	if err := act(id); err != nil {
		s.fail(w, r, rc, err, formsPath(ref))
		return
	}
	s.setFlash(w, r, flashOK, rc.loc.T(okKey))
	redirect(w, r, formsPath(ref))
}

func (s *server) verifyChannel(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	s.channelAction(w, r, rc, func(id int64) error {
		_, err := s.svc.VerifyFormChannel(r.Context(), rc.actor, r.PathValue("ref"), id, r.PostFormValue("code"))
		return err
	}, "forms.channel.verified")
}

func (s *server) toggleChannel(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	enabled := r.PostFormValue("enabled") == "1"
	key := "forms.channel.disabled"
	switch {
	case enabled && r.PostFormValue("resume") == "1":
		key = "forms.channel.resumed"
	case enabled:
		key = "forms.channel.enabled"
	}
	s.channelAction(w, r, rc, func(id int64) error {
		_, err := s.svc.SetFormChannelEnabled(r.Context(), rc.actor, r.PathValue("ref"), id, enabled)
		return err
	}, key)
}

func (s *server) deleteChannel(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	s.channelAction(w, r, rc, func(id int64) error {
		return s.svc.DeleteFormChannel(r.Context(), rc.actor, r.PathValue("ref"), id)
	}, "forms.channel.deleted")
}

func (s *server) rotateChannel(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.notFound(w, r, rc)
		return
	}
	res, err := s.svc.RotateWebhookSecret(r.Context(), rc.actor, r.PathValue("ref"), id)
	if err != nil {
		s.fail(w, r, rc, err, formsPath(r.PathValue("ref")))
		return
	}
	s.renderForms(w, r, rc, http.StatusOK, &issuedChannel{Kind: string(domain.ChannelWebhook), Secret: res.Secret}, "", "", "")
}

func (s *server) formSiteSettings(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	ref := r.PathValue("ref")
	back := sitePath(ref)
	if r.PostFormValue("back") == "forms" {
		back = formsPath(ref)
	}
	if _, err := s.svc.SetFormSiteSettings(r.Context(), rc.actor, ref, r.PostFormValue("require_altcha") == "1"); err != nil {
		s.fail(w, r, rc, err, back)
		return
	}
	s.setFlash(w, r, flashOK, rc.loc.T("forms.settings.saved"))
	redirect(w, r, back)
}

func (s *server) exportForms(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	started := false
	err := s.svc.ExportFormSubmissions(r.Context(), rc.actor, r.PathValue("ref"), r.URL.Query().Get("form"),
		func(siteID string) io.Writer {
			started = true
			h := w.Header()
			h.Set("Content-Type", "text/csv; charset=utf-8")
			h.Set("Content-Disposition", `attachment; filename="`+url.PathEscape(siteID)+`-submissions.csv"`)
			h.Set("Cache-Control", "no-store")
			return w
		})
	switch {
	case err != nil && started:
		s.log.Warn("form export interrupted", "err", err) // the file is truncated
	case err != nil:
		s.failPage(w, r, rc, err)
	}
}
