package api

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/service"
)

// Site data on site hosts: /_dropsite/data/<collection>[/<id>]. Caddy
// forwards these paths after the access check of protected sites. Every
// answer, errors included, is JSON with Cache-Control: no-cache.
const dataPath = "/_dropsite/data"

func (a *api) registerDataPaths(mux *http.ServeMux) {
	mux.Handle("GET "+dataPath+"/{collection}", a.dataHandler("GET /_dropsite/data/{collection}", a.readCollection))
	mux.Handle("GET "+dataPath+"/{collection}/{id}", a.dataHandler("GET /_dropsite/data/{collection}/{id}", a.readDocument))
	mux.Handle("POST "+dataPath+"/{collection}", a.dataHandler("POST /_dropsite/data/{collection}", a.proposeDocument))
	fallback := a.dataHandler(dataPath, func(_ http.ResponseWriter, r *http.Request) error {
		if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodPost {
			return domain.ErrNotFound
		}
		return errMethodNotAllowed
	})
	mux.Handle(dataPath, fallback)
	mux.Handle(dataPath+"/", fallback)
}

// dataHandler is wrap with the data path error format and headers.
func (a *api) dataHandler(pattern string, h handlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if info := infoFrom(r.Context()); info != nil {
			info.route = pattern
		}
		w.Header().Set("Cache-Control", "no-cache")
		if err := h(w, r); err != nil {
			a.dataError(w, r, err)
		}
	})
}

// dataStatus maps visitor errors to HTTP statuses.
func dataStatus(e *domain.Error) int {
	switch e.Code {
	case domain.CodeFormOrigin:
		return http.StatusForbidden
	case domain.CodeFormUnsupported:
		return http.StatusUnsupportedMediaType
	case domain.CodeCollectionReadOnly, codeMethodNotAllowed:
		return http.StatusMethodNotAllowed
	}
	return statusFor(e)
}

func (a *api) dataError(w http.ResponseWriter, r *http.Request, err error) {
	e := domain.AsError(err)
	if e.Kind == domain.KindInternal {
		a.logInternal(r, err)
		e = internalError()
	}
	status := dataStatus(e)
	switch status {
	case http.StatusTooManyRequests:
		w.Header().Set("Retry-After", retryAfterSeconds(e))
	case http.StatusMethodNotAllowed:
		if e.Code == domain.CodeCollectionReadOnly {
			w.Header().Set("Allow", "GET, HEAD")
		} else {
			w.Header().Set("Allow", "GET, HEAD, POST")
		}
	}
	writeJSON(w, status, errorBody(e))
}

// dataETag ties a response to the collection's data version and the query.
func dataETag(version int64, parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return `"v` + strconv.FormatInt(version, 10) + "-" + hex.EncodeToString(sum[:6]) + `"`
}

// notModified answers 304 when the client already has this version.
func notModified(w http.ResponseWriter, r *http.Request, etag string) bool {
	w.Header().Set("ETag", etag)
	for _, v := range strings.Split(r.Header.Get("If-None-Match"), ",") {
		if strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(v), "W/")) == etag {
			w.WriteHeader(http.StatusNotModified)
			return true
		}
	}
	return false
}

type publicDocument struct {
	ID        string `json:"id"`
	Data      any    `json:"data"`
	Position  int64  `json:"position"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

func toPublic(d domain.Document) publicDocument {
	return publicDocument{ID: d.ID, Data: rawJSON(d.Data), Position: d.Position,
		CreatedAt: d.CreatedAt.UTC().Format("2006-01-02T15:04:05.000Z07:00"),
		UpdatedAt: d.UpdatedAt.UTC().Format("2006-01-02T15:04:05.000Z07:00")}
}

type rawJSON []byte

func (r rawJSON) MarshalJSON() ([]byte, error) { return r, nil }

type publicList struct {
	Items []publicDocument `json:"items"`
	Next  *string          `json:"next"`
}

func (a *api) readCollection(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	limit := 0
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > service.MaxDocumentPage {
			return invalidField("limit", "limit must be an integer from 1 to "+strconv.Itoa(service.MaxDocumentPage))
		}
		limit = n
	}
	if err := a.svc.AllowDataRead(infoFrom(r.Context()).ip); err != nil {
		return err
	}
	p, err := a.svc.ReadCollection(r.Context(), service.DataRead{Host: r.Host, Collection: r.PathValue("collection"),
		Order: q.Get("order"), After: q.Get("after"), Limit: limit})
	if err != nil {
		return err
	}
	if notModified(w, r, dataETag(p.Version, r.URL.RawQuery)) {
		return nil
	}
	out := publicList{Items: make([]publicDocument, 0, len(p.Items)), Next: optional(p.Next)}
	for _, d := range p.Items {
		out.Items = append(out.Items, toPublic(d))
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

func (a *api) readDocument(w http.ResponseWriter, r *http.Request) error {
	if err := a.svc.AllowDataRead(infoFrom(r.Context()).ip); err != nil {
		return err
	}
	d, version, err := a.svc.ReadDocument(r.Context(), r.Host, r.PathValue("collection"), r.PathValue("id"))
	if err != nil {
		return err
	}
	if notModified(w, r, dataETag(version, d.ID)) {
		return nil
	}
	writeJSON(w, http.StatusOK, toPublic(d))
	return nil
}

var (
	errDataType = domain.E(domain.KindInvalid, domain.CodeFormUnsupported, "send the document as application/json")
	errDataSize = domain.E(domain.KindTooLarge, domain.CodeDocumentInvalid, "the document is larger than 8 KB")
)

type proposalReply struct {
	OK     bool   `json:"ok"`
	Status string `json:"status"`
}

func (a *api) proposeDocument(w http.ResponseWriter, r *http.Request) error {
	if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "application/json" {
		return errDataType
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, service.MaxVisitorDocumentBytes))
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return errDataSize
		}
		return invalid("could not read the request body")
	}
	if _, err := a.svc.SubmitDocument(r.Context(), service.DataPost{
		Host: r.Host, Collection: r.PathValue("collection"), Origin: r.Header.Get("Origin"),
		IP: infoFrom(r.Context()).ip, Body: body,
	}); err != nil {
		return err
	}
	writeJSON(w, http.StatusAccepted, proposalReply{OK: true, Status: string(domain.DocumentPending)})
	return nil
}
