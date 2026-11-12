package service

import (
	"bytes"
	"context"
	"encoding/json"
	"sort"
	"strings"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/forms"
)

// Visitor access to site collections at /_dropsite/data/ on site hosts.
// Caddy forwards these paths only after the access check of protected
// sites, so here the site is chosen by the host alone. Release preview
// addresses serve the same data as the site.

var errCollectionReadOnly = domain.E(domain.KindInvalid, domain.CodeCollectionReadOnly,
	"this collection does not accept documents from visitors")

// dataSite resolves a visitor-facing host to an active site.
func (s *Service) dataSite(ctx context.Context, rawHost string) (domain.Site, string, error) {
	host := normalizeHost(rawHost)
	if host == "" {
		return domain.Site{}, "", domain.ErrNotFound
	}
	site, err := s.siteByHost(ctx, host)
	return site, host, err
}

// dataCollection resolves a host and a collection name for visitors.
func (s *Service) dataCollection(ctx context.Context, rawHost, name string) (domain.Collection, string, error) {
	site, host, err := s.dataSite(ctx, rawHost)
	if err != nil {
		return domain.Collection{}, "", err
	}
	n, ok := domain.NormalizeCollectionName(name)
	if !ok || n != name {
		return domain.Collection{}, "", domain.ErrNotFound
	}
	c, err := s.repo.GetCollection(ctx, site.ID, n)
	return c, host, err
}

// DataRead is a visitor listing of a collection.
type DataRead struct {
	Host       string
	Collection string
	Order      string
	After      string
	Limit      int
}

// PublicPage is a page of approved documents plus the data version of the
// collection (the adapter derives the ETag from it).
type PublicPage struct {
	DocumentPage
	Version int64
}

// ReadCollection lists approved documents of a collection for visitors.
func (s *Service) ReadCollection(ctx context.Context, q DataRead) (PublicPage, error) {
	c, _, err := s.dataCollection(ctx, q.Host, q.Collection)
	if err != nil {
		return PublicPage{}, err
	}
	p, err := s.listDocuments(ctx, c, domain.DocumentApproved, q.Order, q.After, publicPageLimit(q.Limit))
	if err != nil {
		return PublicPage{}, err
	}
	return PublicPage{DocumentPage: p, Version: c.DataVersion}, nil
}

// ReadDocument returns one approved document for visitors, with the data
// version of its collection.
func (s *Service) ReadDocument(ctx context.Context, host, collection, id string) (domain.Document, int64, error) {
	c, _, err := s.dataCollection(ctx, host, collection)
	if err != nil {
		return domain.Document{}, 0, err
	}
	n, ok := domain.NormalizeDocumentID(id)
	if !ok || n != id {
		return domain.Document{}, 0, domain.ErrNotFound
	}
	d, err := s.repo.GetDocument(ctx, c.SiteID, c.Name, n)
	if err != nil {
		return domain.Document{}, 0, err
	}
	if d.Status != domain.DocumentApproved {
		return domain.Document{}, 0, domain.ErrNotFound
	}
	return d, c.DataVersion, nil
}

// DataPost is a visitor proposal: the raw JSON body.
type DataPost struct {
	Host       string // Host header as received
	Collection string
	Origin     string
	IP         string
	Body       []byte
}

// visitorFields splits a proposal into the document fields and the
// platform fields: keys starting with "_" and "altcha" are never stored.
func visitorFields(body []byte) (map[string]any, string, bool, error) {
	var obj map[string]any
	trimmed := bytes.TrimSpace(body)
	if !bytes.HasPrefix(trimmed, []byte("{")) || json.Unmarshal(trimmed, &obj) != nil {
		return nil, "", false, docErr(-1, "", "the body must be a JSON object")
	}
	honeypot := false
	if v, ok := obj["_hp"]; ok {
		if s, isStr := v.(string); !isStr || s != "" {
			honeypot = true
		}
	}
	altchaPayload, _ := obj["altcha"].(string)
	for k := range obj {
		if strings.HasPrefix(k, "_") || k == "altcha" {
			delete(obj, k)
		}
	}
	return obj, altchaPayload, honeypot, nil
}

// SubmitDocument stores a visitor proposal to a moderated collection as a
// pending document. stored is false for a honeypot hit, which is answered
// like success. The protections are those of site forms: Origin, rate
// limits per IP and site, the honeypot and Altcha when the site requires it.
func (s *Service) SubmitDocument(ctx context.Context, p DataPost) (bool, error) {
	c, host, err := s.dataCollection(ctx, p.Host, p.Collection)
	if err != nil {
		return false, err
	}
	if c.Mode != domain.CollectionModerated {
		return false, errCollectionReadOnly
	}
	if !sameOrigin(p.Origin, p.Host) {
		return false, errFormOrigin
	}
	src := ipKey(p.IP)
	if ok, retry := s.dataPostIP.Allow(src); !ok {
		return false, domain.ErrRateLimited.WithRetryAfter(retry)
	}
	if ok, retry := s.dataPostSite.Check(c.SiteID); !ok {
		return false, domain.ErrRateLimited.WithRetryAfter(retry)
	}
	if len(p.Body) > MaxVisitorDocumentBytes {
		return false, docErr(-1, "", "the document is larger than 8 KB")
	}
	obj, payload, honeypot, err := visitorFields(p.Body)
	if err != nil {
		return false, err
	}
	if honeypot {
		s.penalizeAltcha(forms.PurposeForm, src, c.SiteID)
		return false, nil
	}
	if err := s.checkAltcha(ctx, c.SiteID, host, payload); err != nil {
		s.penalizeAltcha(forms.PurposeForm, src, c.SiteID)
		return false, err
	}
	data, err := s.checkVisitorDocument(c, obj)
	if err != nil {
		return false, err
	}
	s.dataPostSite.Record(c.SiteID)
	id, err := newDocumentID()
	if err != nil {
		return false, err
	}
	now := s.now()
	err = s.repo.InsertPendingDocument(ctx, domain.Document{SiteID: c.SiteID, Collection: c.Name, ID: id, Data: data,
		Status: domain.DocumentPending, CreatedAt: now, UpdatedAt: now}, MaxDocumentsPerCollection, MaxPendingDocuments)
	return err == nil, err
}

// checkVisitorDocument rejects keys the schema does not describe (whatever
// its additionalProperties says) and validates the rest.
func (s *Service) checkVisitorDocument(c domain.Collection, obj map[string]any) (json.RawMessage, error) {
	if len(c.Schema) == 0 {
		return nil, errCollectionReadOnly
	}
	props := schemaProperties(c.Schema)
	var unknown []string
	for k := range obj {
		if !props[k] {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return nil, docErr(-1, unknown[0], "the field "+unknown[0]+" is not accepted by this collection")
	}
	rs, err := s.schemas.get(c)
	if err != nil {
		return nil, err
	}
	if err := validateDocument(rs, obj, -1); err != nil {
		return nil, err
	}
	return encodeVisitorDocument(obj)
}

// encodeVisitorDocument stores a proposal without HTML escaping (which
// would turn "<" into six bytes) and enforces the stored document limit:
// decoding and encoding again can still grow a body, e.g. invalid UTF-8
// becomes U+FFFD and "1e5" becomes 100000.
func encodeVisitorDocument(obj map[string]any) (json.RawMessage, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(obj); err != nil {
		return nil, docErr(-1, "", "the document cannot be stored")
	}
	data := bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
	if len(data) > MaxDocumentBytes {
		return nil, docErr(-1, "", "the document is larger than 16 KB once stored")
	}
	return data, nil
}
