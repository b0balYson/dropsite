package api

import (
	"fmt"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/b0balYson/dropsite/internal/service"
)

// Upload links (spec upload-link): the files of one upload session arrive
// at https://<API_HOST>/u/<token> without a bearer token; the token in the
// path is the capability and the service checks it. The page at
// GET /u/<token> is a mount of internal/ui.

// Route templates of the upload link endpoints; the access log records
// these, never the path with the token.
const (
	routeLinkPut  = "PUT /u/{token}/{path}"
	routeLinkPost = "POST /u/{token}"
)

// tarTypes are the media types accepted for a folder upload.
var tarTypes = map[string]bool{
	"application/x-tar": true, "application/tar": true, "application/x-gtar": true,
	"application/gzip": true, "application/x-gzip": true, "application/x-compressed-tar": true,
}

type linkInfoDTO struct {
	Files        int       `json:"files"`
	Bytes        int64     `json:"bytes"`
	MaxBytes     int64     `json:"max_bytes"`
	MaxFiles     int       `json:"max_files"`
	MaxFileBytes int64     `json:"max_file_bytes"`
	ExpiresAt    time.Time `json:"expires_at"`
}

type skippedDTO struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

type linkReceiptDTO struct {
	Received int          `json:"received"`
	Bytes    int64        `json:"bytes"`
	Skipped  []skippedDTO `json:"skipped"`
	Upload   linkInfoDTO  `json:"upload"`
	Message  string       `json:"message"`
}

func (a *api) registerUploadLinks(mux *http.ServeMux) {
	mux.Handle("PUT "+service.UploadLinkPath+"{token}/{path...}", a.wrap(routeLinkPut, a.putLinkFile))
	mux.Handle("POST "+service.UploadLinkPath+"{token}", a.wrap(routeLinkPost, a.postLinkArchive))
}

func (a *api) putLinkFile(w http.ResponseWriter, r *http.Request) error {
	linkHeaders(w)
	rec, err := a.svc.PutLinkFile(r.Context(), r.PathValue("token"), RequestIP(r.Context()), r.PathValue("path"), r.Body)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, toLinkReceipt(rec))
	return nil
}

func (a *api) postLinkArchive(w http.ResponseWriter, r *http.Request) error {
	linkHeaders(w)
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || !tarTypes[strings.ToLower(mt)] {
		return invalid("send a tar stream of the site folder with Content-Type: application/x-tar " +
			"(gzip-compressed is fine), or PUT single files to <link>/<path>")
	}
	rec, err := a.svc.PutLinkArchive(r.Context(), r.PathValue("token"), RequestIP(r.Context()), r.Body)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, toLinkReceipt(rec))
	return nil
}

func linkHeaders(w http.ResponseWriter) {
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
}

func toLinkReceipt(r service.LinkReceipt) linkReceiptDTO {
	out := linkReceiptDTO{
		Received: r.Files, Bytes: r.Bytes, Skipped: make([]skippedDTO, 0, len(r.Skipped)),
		Upload: linkInfoDTO{
			Files: r.Upload.Files, Bytes: r.Upload.Bytes, MaxBytes: r.Upload.MaxBytes, MaxFiles: r.Upload.MaxFiles,
			MaxFileBytes: r.Upload.MaxFileBytes, ExpiresAt: utc(r.Upload.ExpiresAt),
		},
	}
	for _, s := range r.Skipped {
		out.Skipped = append(out.Skipped, skippedDTO(s))
	}
	out.Message = fmt.Sprintf("Received %d file(s), %s; the upload now holds %d file(s), %s. "+
		"When everything is sent, call commit_upload to publish.",
		r.Files, humanBytes(r.Bytes), r.Upload.Files, humanBytes(r.Upload.Bytes))
	if len(r.Skipped) > 0 {
		out.Message += fmt.Sprintf(" %d file(s) were skipped (see skipped).", len(r.Skipped))
	}
	return out
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d bytes", n)
}

// redactPath hides the token of an upload link in logged paths.
func redactPath(p string) string {
	if rest, ok := strings.CutPrefix(p, service.UploadLinkPath); ok {
		if _, tail, found := strings.Cut(rest, "/"); found {
			return service.UploadLinkPath + "REDACTED/" + tail
		}
		return service.UploadLinkPath + "REDACTED"
	}
	return p
}
