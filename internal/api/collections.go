package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/service"
)

// maxDocumentsBody bounds the JSON bodies of document writes: 1000
// documents of 16 KB each plus framing.
const maxDocumentsBody = 20 << 20

type collectionDTO struct {
	Name      string          `json:"name"`
	Title     string          `json:"title"`
	Mode      string          `json:"mode"`
	Schema    json.RawMessage `json:"schema"`
	Documents int             `json:"documents"`
	Pending   int             `json:"pending"`
	PublicURL string          `json:"public_url"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

func toCollection(c service.CollectionView) collectionDTO {
	schema := c.Schema
	if len(schema) == 0 {
		schema = json.RawMessage("null")
	}
	return collectionDTO{
		Name: c.Name, Title: c.Title, Mode: string(c.Mode), Schema: schema, Documents: c.Documents,
		Pending: c.Pending, PublicURL: c.PublicURL, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
	}
}

type documentDTO struct {
	ID        string          `json:"id"`
	Data      json.RawMessage `json:"data"`
	Position  int64           `json:"position"`
	Status    string          `json:"status"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

func toDocument(d domain.Document) documentDTO {
	return documentDTO{ID: d.ID, Data: d.Data, Position: d.Position, Status: string(d.Status),
		CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt}
}

func (a *api) listCollections(w http.ResponseWriter, r *http.Request) error {
	_, cs, err := a.svc.ListCollections(r.Context(), actorFrom(r.Context()), r.PathValue("ref"))
	if err != nil {
		return err
	}
	items := make([]collectionDTO, 0, len(cs))
	for _, c := range cs {
		items = append(items, toCollection(c))
	}
	writeJSON(w, http.StatusOK, newList(items, len(items)))
	return nil
}

type createCollectionRequest struct {
	Name   string          `json:"name"`
	Title  string          `json:"title"`
	Mode   string          `json:"mode"`
	Schema json.RawMessage `json:"schema"`
}

func (a *api) createCollection(w http.ResponseWriter, r *http.Request) error {
	var req createCollectionRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return err
	}
	c, err := a.svc.CreateCollection(r.Context(), actorFrom(r.Context()), r.PathValue("ref"), service.CollectionInput{
		Name: req.Name, Title: req.Title, Mode: domain.CollectionMode(req.Mode), Schema: req.Schema,
	})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, toCollection(c))
	return nil
}

type updateCollectionRequest struct {
	Title  *string         `json:"title"`
	Mode   *string         `json:"mode"`
	Schema json.RawMessage `json:"schema"`
}

func (a *api) updateCollection(w http.ResponseWriter, r *http.Request) error {
	var req updateCollectionRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return err
	}
	u := service.CollectionUpdate{Title: req.Title}
	if req.Mode != nil {
		m := domain.CollectionMode(*req.Mode)
		u.Mode = &m
	}
	if req.Schema != nil {
		// JSON null removes the schema.
		u.Schema = &req.Schema
	}
	c, err := a.svc.UpdateCollection(r.Context(), actorFrom(r.Context()), r.PathValue("ref"), r.PathValue("name"), u)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, toCollection(c))
	return nil
}

type confirmRequest struct {
	Confirm string `json:"confirm"`
}

func (a *api) deleteCollection(w http.ResponseWriter, r *http.Request) error {
	var req confirmRequest
	if err := decodeOptionalJSON(w, r, &req); err != nil {
		return err
	}
	if req.Confirm == "" {
		req.Confirm = r.URL.Query().Get("confirm")
	}
	if err := a.svc.DeleteCollection(r.Context(), actorFrom(r.Context()), r.PathValue("ref"), r.PathValue("name"), req.Confirm); err != nil {
		return err
	}
	noContent(w)
	return nil
}

type documentPageDTO struct {
	Items      []documentDTO `json:"items"`
	NextCursor *string       `json:"next_cursor"`
}

func toDocumentPage(p service.DocumentPage) documentPageDTO {
	out := documentPageDTO{Items: make([]documentDTO, 0, len(p.Items)), NextCursor: optional(p.Next)}
	for _, d := range p.Items {
		out.Items = append(out.Items, toDocument(d))
	}
	return out
}

func (a *api) listDocuments(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	limit := 0
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > service.MaxDocumentPage {
			return invalidField("limit", "limit must be an integer from 1 to "+strconv.Itoa(service.MaxDocumentPage))
		}
		limit = n
	}
	p, err := a.svc.ListDocuments(r.Context(), actorFrom(r.Context()), r.PathValue("ref"), r.PathValue("name"),
		service.DocumentQuery{Status: q.Get("status"), Order: q.Get("order"), Cursor: q.Get("cursor"), Limit: limit})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, toDocumentPage(p))
	return nil
}

type documentInput struct {
	ID       string          `json:"id"`
	Data     json.RawMessage `json:"data"`
	Position *int64          `json:"position"`
}

type saveDocumentsRequest struct {
	Documents []documentInput `json:"documents"`
	Replace   bool            `json:"replace"`
}

type saveDocumentsReply struct {
	IDs        []string      `json:"ids"`
	Collection collectionDTO `json:"collection"`
}

func (a *api) saveDocuments(w http.ResponseWriter, r *http.Request) error {
	var req saveDocumentsRequest
	if err := decode(w, r, &req, false, maxDocumentsBody); err != nil {
		return err
	}
	docs := make([]service.DocumentInput, 0, len(req.Documents))
	for _, d := range req.Documents {
		docs = append(docs, service.DocumentInput(d))
	}
	res, err := a.svc.SaveDocuments(r.Context(), actorFrom(r.Context()), r.PathValue("ref"), r.PathValue("name"), docs, req.Replace)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, saveDocumentsReply{IDs: res.IDs, Collection: toCollection(res.Collection)})
	return nil
}

type countReply struct {
	Count      int           `json:"count"`
	Collection collectionDTO `json:"collection"`
}

func (a *api) deleteDocument(w http.ResponseWriter, r *http.Request) error {
	n, _, err := a.svc.DeleteDocuments(r.Context(), actorFrom(r.Context()), r.PathValue("ref"), r.PathValue("name"),
		[]string{r.PathValue("doc")})
	if err != nil {
		return err
	}
	if n == 0 {
		return domain.ErrNotFound
	}
	noContent(w)
	return nil
}

type idsRequest struct {
	IDs []string `json:"ids"`
}

func (a *api) deleteDocuments(w http.ResponseWriter, r *http.Request) error {
	var req idsRequest
	if err := decode(w, r, &req, false, maxDocumentsBody); err != nil {
		return err
	}
	n, c, err := a.svc.DeleteDocuments(r.Context(), actorFrom(r.Context()), r.PathValue("ref"), r.PathValue("name"), req.IDs)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, countReply{Count: n, Collection: toCollection(c)})
	return nil
}

type moderateRequest struct {
	IDs    []string `json:"ids"`
	Action string   `json:"action"`
}

func (a *api) moderateDocuments(w http.ResponseWriter, r *http.Request) error {
	var req moderateRequest
	if err := decode(w, r, &req, false, maxDocumentsBody); err != nil {
		return err
	}
	if req.Action != "approve" && req.Action != "reject" {
		return invalidField("action", "action must be approve or reject")
	}
	n, c, err := a.svc.ModerateDocuments(r.Context(), actorFrom(r.Context()), r.PathValue("ref"), r.PathValue("name"),
		req.IDs, req.Action == "approve")
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, countReply{Count: n, Collection: toCollection(c)})
	return nil
}
