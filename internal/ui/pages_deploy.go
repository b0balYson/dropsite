package ui

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/service"
)

const (
	// multipartSlack covers multipart headers and the CSRF field on top of
	// the archive itself.
	multipartSlack = 64 << 10
	maxFieldBytes  = 256
)

// deployReply is the JSON answer to an XHR upload.
type deployReply struct {
	OK       bool     `json:"ok"`
	URL      string   `json:"url,omitempty"`
	URLs     []string `json:"urls,omitempty"`
	DeployID string   `json:"deploy_id,omitempty"`
	Message  string   `json:"message,omitempty"`
}

var errNoFile = errors.New("no archive in the upload")

// deploy streams the "archive" part of a multipart upload into the service
// without buffering it in memory or on disk here.
func (s *server) deploy(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	ref := r.PathValue("ref")
	limit, err := s.svc.UploadLimit(r.Context(), rc.actor, ref)
	if err != nil {
		s.deployFailed(w, r, rc, ref, err)
		return
	}
	if r.ContentLength > limit+multipartSlack {
		s.deployFailed(w, r, rc, ref, domain.E(domain.KindTooLarge, domain.CodeArchiveTooLarge, "archive is too large").
			WithParams(map[string]any{"limit": limit}))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit+multipartSlack)
	res, err := s.streamDeploy(r, rc, ref)
	switch {
	case errors.Is(err, errForbidden):
		s.forbidden(w, r, rc)
	case err != nil:
		s.deployFailed(w, r, rc, ref, err)
	default:
		s.deployDone(w, r, rc, res)
	}
}

var errForbidden = errors.New("csrf token missing or invalid")

func (s *server) streamDeploy(r *http.Request, rc *reqCtx, ref string) (service.DeployResult, error) {
	mr, err := r.MultipartReader()
	if err != nil {
		return service.DeployResult{}, errNoFile
	}
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			return service.DeployResult{}, errNoFile
		}
		if err != nil {
			return service.DeployResult{}, domain.E(domain.KindInvalid, domain.CodeArchiveCorrupt, "upload was interrupted").WithCause(err)
		}
		switch part.FormName() {
		case csrfField:
			b, _ := io.ReadAll(io.LimitReader(part, maxFieldBytes))
			rc.csrfChecked = rc.csrfChecked || tokensEqual(rc.csrf, string(b))
		case "archive":
			if !rc.csrfChecked {
				return service.DeployResult{}, errForbidden
			}
			if part.FileName() == "" {
				return service.DeployResult{}, errNoFile
			}
			return s.svc.Deploy(r.Context(), rc.actor, ref, part, domain.SourceUI)
		default:
			_, _ = io.Copy(io.Discard, io.LimitReader(part, maxFieldBytes))
		}
	}
}

func (s *server) deployDone(w http.ResponseWriter, r *http.Request, rc *reqCtx, res service.DeployResult) {
	if wantsJSON(r) {
		writeJSON(w, http.StatusOK, deployReply{
			OK: true, URL: res.Site.URL, URLs: res.Site.URLs, DeployID: res.Deploy.ID,
			Message: rc.loc.T("deploy.done"),
		})
		return
	}
	s.setFlash(w, r, flashDeployed, rc.loc.T("deploy.done"))
	redirect(w, r, sitePath(res.Site.Site.ID))
}

func (s *server) deployFailed(w http.ResponseWriter, r *http.Request, rc *reqCtx, ref string, err error) {
	var msg string
	status := http.StatusUnprocessableEntity
	if errors.Is(err, errNoFile) {
		msg = rc.loc.T("deploy.nofile")
	} else {
		if isNotFound(err) && !wantsJSON(r) {
			s.notFound(w, r, rc)
			return
		}
		msg, status = s.describe(rc, err)
	}
	if wantsJSON(r) {
		writeJSON(w, status, deployReply{Message: msg})
		return
	}
	s.setFlash(w, r, flashError, msg)
	redirect(w, r, sitePath(ref))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
