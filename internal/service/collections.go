package service

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/ratelimit"
	"github.com/b0balYson/dropsite/internal/store"
)

// Site collection limits (change site-collections).
const (
	MaxCollectionsPerSite     = 20
	MaxDocumentsPerCollection = 5000
	MaxDocumentBytes          = 16 << 10
	MaxDocumentsPerWrite      = 1000
	MaxPendingDocuments       = 1000
	MaxSchemaBytes            = 16 << 10
	// MaxVisitorDocumentBytes bounds a visitor proposal (Caddy enforces
	// the same body limit).
	MaxVisitorDocumentBytes = 8 << 10
	DefaultDocumentPage     = 100
	MaxDocumentPage         = 500
	// DataPathPrefix is where a site's collections are read on its hosts.
	DataPathPrefix = "/_dropsite/data/"
)

// Document orders, re-exported for adapters.
const (
	OrderPosition = store.OrderPosition
	OrderNewest   = store.OrderNewest
	OrderOldest   = store.OrderOldest
)

// collectionState is the collections part of Service, created in New.
type collectionState struct {
	schemas      *schemaCache
	dataPostIP   *ratelimit.Window
	dataPostSite *ratelimit.Window
	dataReadIP   *ratelimit.Window
}

// Visitor proposals have their own windows with the values of form posts,
// so forms and data never use up each other's quota.
func newCollectionState(now func() time.Time) collectionState {
	return collectionState{
		schemas:      &schemaCache{},
		dataPostIP:   ratelimit.New(FormPostsPerIP, FormPostsPerIPWindow).WithClock(now),
		dataPostSite: ratelimit.New(FormPostsPerSite, time.Hour).WithClock(now),
		dataReadIP:   newDataReadLimit(now),
	}
}

// CollectionView is a collection with its public read address.
type CollectionView struct {
	domain.Collection
	// PublicURL is https://<primary address>/_dropsite/data/<name>, or ""
	// while the site has no active address.
	PublicURL string
}

// CollectionInput creates a collection. Schema may be empty.
type CollectionInput struct {
	Name   string
	Title  string
	Mode   domain.CollectionMode
	Schema json.RawMessage
}

// CollectionUpdate changes a collection; nil fields stay. A non-nil Schema
// that is empty or JSON null removes the schema.
type CollectionUpdate struct {
	Title  *string
	Mode   *domain.CollectionMode
	Schema *json.RawMessage
}

// DocumentInput is one document written by the owner. An empty ID asks the
// server for one; a nil Position keeps the stored position.
type DocumentInput struct {
	ID       string
	Data     json.RawMessage
	Position *int64
}

// DocumentQuery selects owner documents. Status is approved, pending or
// all ("" = all); Order is position (default), newest or oldest.
type DocumentQuery struct {
	Status string
	Order  string
	Cursor string
	Limit  int
}

// DocumentPage is one page of documents; Next is "" on the last page.
type DocumentPage struct {
	Items []domain.Document
	Next  string
}

func collectionErr(field, msg string) error {
	return domain.E(domain.KindInvalid, domain.CodeCollectionInvalid, msg).WithParams(map[string]any{"field": field})
}

var errCollectionConfirm = domain.E(domain.KindInvalid, domain.CodeInvalidInput,
	"not deleted: confirm must repeat the collection name").WithParams(map[string]any{"field": "confirm"})

// collectionURL is the public read address of a collection.
func collectionURL(siteURL, name string) string {
	if siteURL == "" {
		return ""
	}
	return strings.TrimSuffix(siteURL, "/") + DataPathPrefix + name
}

// ownedCollection resolves the actor's site and one of its collections.
func (s *Service) ownedCollection(ctx context.Context, a Actor, ref, name string) (SiteView, domain.Collection, error) {
	site, err := s.resolveSite(ctx, a, ref)
	if err != nil {
		return SiteView{}, domain.Collection{}, err
	}
	n, ok := domain.NormalizeCollectionName(name)
	if !ok {
		return SiteView{}, domain.Collection{}, domain.ErrNotFound
	}
	c, err := s.repo.GetCollection(ctx, site.ID, n)
	if err != nil {
		return SiteView{}, domain.Collection{}, err
	}
	v, err := s.view(ctx, site)
	return v, c, err
}

func (s *Service) collectionView(ctx context.Context, v SiteView, name string) (CollectionView, error) {
	c, err := s.repo.GetCollection(ctx, v.Site.ID, name)
	if err != nil {
		return CollectionView{}, err
	}
	return CollectionView{Collection: c, PublicURL: collectionURL(v.URL, c.Name)}, nil
}

// ListCollections returns the site's collections with counts.
func (s *Service) ListCollections(ctx context.Context, a Actor, ref string) (SiteView, []CollectionView, error) {
	site, err := s.resolveSite(ctx, a, ref)
	if err != nil {
		return SiteView{}, nil, err
	}
	v, err := s.view(ctx, site)
	if err != nil {
		return SiteView{}, nil, err
	}
	cs, err := s.repo.ListCollections(ctx, site.ID)
	if err != nil {
		return SiteView{}, nil, err
	}
	out := make([]CollectionView, 0, len(cs))
	for _, c := range cs {
		out = append(out, CollectionView{Collection: c, PublicURL: collectionURL(v.URL, c.Name)})
	}
	return v, out, nil
}

// GetCollection returns one collection of the actor's site.
func (s *Service) GetCollection(ctx context.Context, a Actor, ref, name string) (CollectionView, error) {
	v, c, err := s.ownedCollection(ctx, a, ref, name)
	if err != nil {
		return CollectionView{}, err
	}
	return CollectionView{Collection: c, PublicURL: collectionURL(v.URL, c.Name)}, nil
}

func checkTitle(title string) (string, error) {
	title = strings.TrimSpace(title)
	if utf8.RuneCountInString(title) > domain.MaxCollectionTitleLength || strings.ContainsAny(title, "\r\n\x00") {
		return "", collectionErr("title", "the title must be one line of at most 200 characters")
	}
	return title, nil
}

// checkModeSchema validates the mode and the schema together and returns
// the compact schema (nil when none).
func checkModeSchema(mode domain.CollectionMode, schema json.RawMessage) (json.RawMessage, error) {
	if !mode.Valid() {
		return nil, collectionErr("mode", "mode must be public or moderated")
	}
	trimmed := strings.TrimSpace(string(schema))
	if trimmed == "" || trimmed == "null" {
		if mode == domain.CollectionModerated {
			return nil, collectionErr("schema", "a moderated collection needs a schema: visitors may only send "+
				"the fields it describes")
		}
		return nil, nil
	}
	compact, _, err := parseSchema(schema)
	return compact, err
}

// CreateCollection adds a collection to the actor's site.
func (s *Service) CreateCollection(ctx context.Context, a Actor, ref string, in CollectionInput) (CollectionView, error) {
	site, err := s.resolveSite(ctx, a, ref)
	if err != nil {
		return CollectionView{}, err
	}
	name, ok := domain.NormalizeCollectionName(in.Name)
	if !ok {
		return CollectionView{}, collectionErr("name", "the name must be 1-64 characters a-z, 0-9, - and _, "+
			"starting with a letter or digit")
	}
	title, err := checkTitle(in.Title)
	if err != nil {
		return CollectionView{}, err
	}
	if in.Mode == "" {
		in.Mode = domain.CollectionPublic
	}
	schema, err := checkModeSchema(in.Mode, in.Schema)
	if err != nil {
		return CollectionView{}, err
	}
	now := s.now()
	c := domain.Collection{SiteID: site.ID, Name: name, Title: title, Mode: in.Mode, Schema: schema, CreatedAt: now, UpdatedAt: now}
	if err := s.repo.CreateCollection(ctx, c, MaxCollectionsPerSite); err != nil {
		return CollectionView{}, err
	}
	s.audit(ctx, a, "collection.create", siteTarget(site.ID), map[string]any{"name": name, "mode": string(in.Mode)})
	v, err := s.view(ctx, site)
	if err != nil {
		return CollectionView{}, err
	}
	return s.collectionView(ctx, v, name)
}

// UpdateCollection changes the title, mode or schema of a collection. A
// new schema applies only to documents written afterwards.
func (s *Service) UpdateCollection(ctx context.Context, a Actor, ref, name string, u CollectionUpdate) (CollectionView, error) {
	v, c, err := s.ownedCollection(ctx, a, ref, name)
	if err != nil {
		return CollectionView{}, err
	}
	if u.Title != nil {
		if c.Title, err = checkTitle(*u.Title); err != nil {
			return CollectionView{}, err
		}
	}
	if u.Mode != nil {
		c.Mode = *u.Mode
	}
	schema := c.Schema
	if u.Schema != nil {
		schema = *u.Schema
	}
	if c.Schema, err = checkModeSchema(c.Mode, schema); err != nil {
		return CollectionView{}, err
	}
	// updated_at keys the schema cache: keep it strictly increasing.
	now := s.now().Truncate(time.Millisecond)
	if !now.After(c.UpdatedAt) {
		now = c.UpdatedAt.Add(time.Millisecond)
	}
	c.UpdatedAt = now
	if err := s.repo.UpdateCollection(ctx, c); err != nil {
		return CollectionView{}, err
	}
	s.audit(ctx, a, "collection.update", siteTarget(c.SiteID), map[string]any{"name": c.Name, "mode": string(c.Mode),
		"schema": len(c.Schema) > 0})
	return s.collectionView(ctx, v, c.Name)
}

// DeleteCollection removes a collection with its documents; confirm must
// repeat its name.
func (s *Service) DeleteCollection(ctx context.Context, a Actor, ref, name, confirm string) error {
	_, c, err := s.ownedCollection(ctx, a, ref, name)
	if err != nil {
		return err
	}
	if strings.ToLower(strings.TrimSpace(confirm)) != c.Name {
		return errCollectionConfirm
	}
	if err := s.repo.DeleteCollection(ctx, c.SiteID, c.Name); err != nil {
		return err
	}
	s.audit(ctx, a, "collection.delete", siteTarget(c.SiteID), map[string]any{"name": c.Name, "documents": c.Documents})
	return nil
}

// newDocumentID returns a random document ID.
func newDocumentID() (string, error) {
	b := make([]byte, 10)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate document id: %w", err)
	}
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)), nil
}

// prepareDocuments validates an owner write and assigns missing IDs.
func (s *Service) prepareDocuments(c domain.Collection, docs []DocumentInput) ([]store.DocumentInput, error) {
	if len(docs) > MaxDocumentsPerWrite {
		return nil, domain.E(domain.KindLimit, domain.CodeLimitExceeded,
			fmt.Sprintf("at most %d documents per write", MaxDocumentsPerWrite)).
			WithParams(map[string]any{"limit": MaxDocumentsPerWrite, "what": "documents per write"})
	}
	rs, err := s.schemas.get(c)
	if err != nil {
		return nil, err
	}
	out := make([]store.DocumentInput, 0, len(docs))
	seen := make(map[string]bool, len(docs))
	for i, d := range docs {
		id := d.ID
		if strings.TrimSpace(id) == "" {
			if id, err = newDocumentID(); err != nil {
				return nil, err
			}
		} else if id, err = normalizeDocID(id, i); err != nil {
			return nil, err
		}
		if seen[id] {
			return nil, docErr(i, "id", "the document ID "+id+" is used twice in this write")
		}
		seen[id] = true
		data, obj, err := compactObject(d.Data, MaxDocumentBytes, i)
		if err != nil {
			return nil, err
		}
		if err := validateDocument(rs, obj, i); err != nil {
			return nil, err
		}
		out = append(out, store.DocumentInput{ID: id, Data: data, Position: d.Position})
	}
	return out, nil
}

func normalizeDocID(id string, index int) (string, error) {
	n, ok := domain.NormalizeDocumentID(id)
	if !ok {
		return "", docErr(index, "id", "the document ID must be 1-64 characters a-z, 0-9, - and _, "+
			"starting with a letter or digit")
	}
	return n, nil
}

// SaveResult is the outcome of an owner write.
type SaveResult struct {
	IDs        []string
	Collection CollectionView
}

// SaveDocuments writes documents (approved at once) atomically; replace
// first removes every document of the collection.
func (s *Service) SaveDocuments(ctx context.Context, a Actor, ref, name string, docs []DocumentInput, replace bool) (SaveResult, error) {
	v, c, err := s.ownedCollection(ctx, a, ref, name)
	if err != nil {
		return SaveResult{}, err
	}
	in, err := s.prepareDocuments(c, docs)
	if err != nil {
		return SaveResult{}, err
	}
	if err := s.repo.SaveDocuments(ctx, store.DocumentsWrite{SiteID: c.SiteID, Collection: c.Name, Docs: in,
		Replace: replace, MaxDocs: MaxDocumentsPerCollection, Now: s.now()}); err != nil {
		return SaveResult{}, err
	}
	if replace {
		s.audit(ctx, a, "collection.replace", siteTarget(c.SiteID), map[string]any{"name": c.Name, "documents": len(in)})
	}
	ids := make([]string, 0, len(in))
	for _, d := range in {
		ids = append(ids, d.ID)
	}
	cv, err := s.collectionView(ctx, v, c.Name)
	return SaveResult{IDs: ids, Collection: cv}, err
}

func normalizeIDs(ids []string) ([]string, error) {
	if len(ids) > MaxDocumentsPerWrite {
		return nil, domain.E(domain.KindLimit, domain.CodeLimitExceeded,
			fmt.Sprintf("at most %d documents per request", MaxDocumentsPerWrite)).
			WithParams(map[string]any{"limit": MaxDocumentsPerWrite, "what": "documents per request"})
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		// An ID that cannot exist matches nothing.
		if n, ok := domain.NormalizeDocumentID(id); ok {
			out = append(out, n)
		}
	}
	return out, nil
}

// DeleteDocuments removes documents of a collection and returns how many
// existed.
func (s *Service) DeleteDocuments(ctx context.Context, a Actor, ref, name string, ids []string) (int, CollectionView, error) {
	v, c, err := s.ownedCollection(ctx, a, ref, name)
	if err != nil {
		return 0, CollectionView{}, err
	}
	ids, err = normalizeIDs(ids)
	if err != nil {
		return 0, CollectionView{}, err
	}
	n, err := s.repo.DeleteDocuments(ctx, c.SiteID, c.Name, ids, s.now())
	if err != nil {
		return 0, CollectionView{}, err
	}
	cv, err := s.collectionView(ctx, v, c.Name)
	return n, cv, err
}

// ModerateDocuments approves or rejects (deletes) pending documents and
// returns how many were pending.
func (s *Service) ModerateDocuments(ctx context.Context, a Actor, ref, name string, ids []string, approve bool) (int, CollectionView, error) {
	v, c, err := s.ownedCollection(ctx, a, ref, name)
	if err != nil {
		return 0, CollectionView{}, err
	}
	ids, err = normalizeIDs(ids)
	if err != nil {
		return 0, CollectionView{}, err
	}
	n, err := s.repo.ModerateDocuments(ctx, c.SiteID, c.Name, ids, approve, s.now())
	if err != nil {
		return 0, CollectionView{}, err
	}
	action := "reject"
	if approve {
		action = "approve"
	}
	if n > 0 {
		s.audit(ctx, a, "collection.moderate", siteTarget(c.SiteID), map[string]any{"name": c.Name, "action": action, "documents": n})
	}
	cv, err := s.collectionView(ctx, v, c.Name)
	return n, cv, err
}

// GetDocument returns one document of the actor's collection (any status).
func (s *Service) GetDocument(ctx context.Context, a Actor, ref, name, id string) (domain.Document, error) {
	_, c, err := s.ownedCollection(ctx, a, ref, name)
	if err != nil {
		return domain.Document{}, err
	}
	n, ok := domain.NormalizeDocumentID(id)
	if !ok {
		return domain.Document{}, domain.ErrNotFound
	}
	return s.repo.GetDocument(ctx, c.SiteID, c.Name, n)
}

// ListDocuments returns one page of the actor's documents.
func (s *Service) ListDocuments(ctx context.Context, a Actor, ref, name string, q DocumentQuery) (DocumentPage, error) {
	_, c, err := s.ownedCollection(ctx, a, ref, name)
	if err != nil {
		return DocumentPage{}, err
	}
	var status domain.DocumentStatus
	switch q.Status {
	case "", "all":
	case string(domain.DocumentApproved), string(domain.DocumentPending):
		status = domain.DocumentStatus(q.Status)
	default:
		return DocumentPage{}, domain.E(domain.KindInvalid, domain.CodeInvalidInput, "status must be approved, pending or all").
			WithParams(map[string]any{"field": "status"})
	}
	return s.listDocuments(ctx, c, status, q.Order, q.Cursor, q.Limit)
}

func (s *Service) listDocuments(ctx context.Context, c domain.Collection, status domain.DocumentStatus, order, cursor string, limit int) (DocumentPage, error) {
	if order == "" {
		order = OrderPosition
	}
	if order != OrderPosition && order != OrderNewest && order != OrderOldest {
		return DocumentPage{}, domain.E(domain.KindInvalid, domain.CodeInvalidInput, "order must be position, newest or oldest").
			WithParams(map[string]any{"field": "order"})
	}
	if limit == 0 {
		limit = DefaultDocumentPage
	}
	if limit < 1 || limit > MaxDocumentPage {
		return DocumentPage{}, domain.E(domain.KindInvalid, domain.CodeInvalidInput,
			fmt.Sprintf("limit must be from 1 to %d", MaxDocumentPage)).WithParams(map[string]any{"field": "limit"})
	}
	after, err := decodeDocCursor(cursor, order)
	if err != nil {
		return DocumentPage{}, err
	}
	docs, more, err := s.repo.ListDocuments(ctx, store.DocumentFilter{SiteID: c.SiteID, Collection: c.Name, Status: status,
		Order: order, After: after, Limit: limit})
	if err != nil {
		return DocumentPage{}, err
	}
	p := DocumentPage{Items: docs}
	if more && len(docs) > 0 {
		p.Next = encodeDocCursor(order, docs[len(docs)-1])
	}
	return p, nil
}

type docCursor struct {
	O string `json:"o"`
	P int64  `json:"p"`
	C int64  `json:"c"`
	I string `json:"i"`
}

func encodeDocCursor(order string, d domain.Document) string {
	b, _ := json.Marshal(docCursor{O: order, P: d.Position, C: d.CreatedAt.UnixMilli(), I: d.ID})
	return base64.RawURLEncoding.EncodeToString(b)
}

var errDocCursor = domain.E(domain.KindInvalid, domain.CodeInvalidInput,
	"the cursor is invalid; start again without it").WithParams(map[string]any{"field": "cursor"})

func decodeDocCursor(s, order string) (*store.DocumentCursor, error) {
	if s == "" {
		return nil, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || len(raw) > 512 {
		return nil, errDocCursor
	}
	var c docCursor
	if err := json.Unmarshal(raw, &c); err != nil || c.O != order {
		return nil, errDocCursor
	}
	if _, ok := domain.NormalizeDocumentID(c.I); !ok {
		return nil, errDocCursor
	}
	return &store.DocumentCursor{Position: c.P, CreatedAt: time.UnixMilli(c.C).UTC(), ID: c.I}, nil
}
