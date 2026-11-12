package api

import (
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"strings"

	"github.com/b0balYson/dropsite/internal/domain"
)

const (
	// multipartOverhead allows for multipart framing and small extra fields
	// on top of the archive size limit.
	multipartOverhead = 64 << 10
	archiveField      = "archive"
	clientHeader      = "X-Dropsite-Client"
	cliClientPrefix   = "cli/"
)

func tooLarge(limit int64) error {
	return domain.E(domain.KindTooLarge, domain.CodeArchiveTooLarge, "archive is too large").
		WithParams(map[string]any{"limit": limit})
}

// deploy streams the multipart "archive" part straight into the service,
// which spools it to a temporary file and enforces the size limit.
func (a *api) deploy(w http.ResponseWriter, r *http.Request) error {
	limit, err := a.svc.UploadLimit(r.Context(), actorFrom(r.Context()), r.PathValue("ref"))
	if err != nil {
		return err
	}
	if r.ContentLength > limit+multipartOverhead {
		w.Header().Set("Connection", "close")
		return tooLarge(limit)
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit+multipartOverhead)
	part, err := archivePart(r)
	if err != nil {
		return err
	}
	defer part.Close()
	body := &limitedBody{r: part, limit: limit}
	res, err := a.svc.Deploy(r.Context(), actorFrom(r.Context()), r.PathValue("ref"), body, deploySource(r))
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, toDeployResult(res))
	return nil
}

// archivePart returns the first multipart part named "archive", skipping
// any other fields before it.
func archivePart(r *http.Request) (*multipart.Part, error) {
	mr, err := r.MultipartReader()
	if err != nil {
		return nil, invalidField(archiveField, "the request must be multipart/form-data with an archive field")
	}
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			return nil, invalidField(archiveField, "the archive field is missing")
		}
		if err != nil {
			var mbe *http.MaxBytesError
			if errors.As(err, &mbe) {
				return nil, tooLarge(mbe.Limit - multipartOverhead)
			}
			return nil, invalidField(archiveField, "malformed multipart body")
		}
		if part.FormName() == archiveField {
			return part, nil
		}
		_ = part.Close()
	}
}

// limitedBody turns the request size cap into the domain "too large" error,
// so the service reports it as 413 instead of a broken upload.
type limitedBody struct {
	r     io.Reader
	limit int64
}

func (b *limitedBody) Read(p []byte) (int, error) {
	n, err := b.r.Read(p)
	var mbe *http.MaxBytesError
	if err != nil && errors.As(err, &mbe) {
		return n, tooLarge(b.limit)
	}
	return n, err
}

func deploySource(r *http.Request) domain.DeploySource {
	if strings.HasPrefix(r.Header.Get(clientHeader), cliClientPrefix) {
		return domain.SourceCLI
	}
	return domain.SourceAPI
}

func (a *api) listDeploys(w http.ResponseWriter, r *http.Request) error {
	ds, v, err := a.svc.ListDeploys(r.Context(), actorFrom(r.Context()), r.PathValue("ref"))
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, newList(toDeploys(ds, v.Site.CurrentDeployID, a.svc.PreviewURL), len(ds)))
	return nil
}

type rollbackRequest struct {
	DeployID string `json:"deploy_id"`
}

func (a *api) rollback(w http.ResponseWriter, r *http.Request) error {
	var req rollbackRequest
	if err := decodeOptionalJSON(w, r, &req); err != nil {
		return err
	}
	res, err := a.svc.Rollback(r.Context(), actorFrom(r.Context()), r.PathValue("ref"), req.DeployID)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, toDeployResult(res))
	return nil
}
