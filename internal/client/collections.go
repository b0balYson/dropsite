package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Collection is a data collection of a site.
type Collection struct {
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

// Document is one document of a collection.
type Document struct {
	ID        string          `json:"id"`
	Data      json.RawMessage `json:"data"`
	Position  int64           `json:"position"`
	Status    string          `json:"status"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// DocumentPage is one page of documents.
type DocumentPage struct {
	Items      []Document `json:"items"`
	NextCursor *string    `json:"next_cursor"`
}

// CollectionCreate creates a collection; a nil Schema means none.
type CollectionCreate struct {
	Name   string          `json:"name"`
	Title  string          `json:"title,omitempty"`
	Mode   string          `json:"mode,omitempty"`
	Schema json.RawMessage `json:"schema,omitempty"`
	// Template names a built-in template; WithSamples writes its samples.
	Template    string `json:"template,omitempty"`
	WithSamples bool   `json:"with_samples,omitempty"`
}

// CollectionPatch changes a collection: nil fields are not sent, a Schema
// of JSON null removes the schema.
type CollectionPatch struct {
	Title  *string         `json:"title,omitempty"`
	Mode   *string         `json:"mode,omitempty"`
	Schema json.RawMessage `json:"schema,omitempty"`
}

// DocumentWrite is one written document.
type DocumentWrite struct {
	ID       string          `json:"id,omitempty"`
	Data     json.RawMessage `json:"data"`
	Position *int64          `json:"position,omitempty"`
}

// DocumentsWritten is the answer to a document write.
type DocumentsWritten struct {
	IDs        []string   `json:"ids"`
	Collection Collection `json:"collection"`
}

// DocumentCount is the answer to a delete or moderation.
type DocumentCount struct {
	Count      int        `json:"count"`
	Collection Collection `json:"collection"`
}

func collectionPath(ref, name string) string {
	return sitePath(ref) + "/collections/" + url.PathEscape(name)
}

// ListCollections returns the collections of a site.
func (c *Client) ListCollections(ctx context.Context, ref string) ([]Collection, error) {
	var out struct {
		Items []Collection `json:"items"`
	}
	if err := c.doJSON(ctx, http.MethodGet, sitePath(ref)+"/collections", nil, nil, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

// CreateCollection adds a collection to a site.
func (c *Client) CreateCollection(ctx context.Context, ref string, in CollectionCreate) (*Collection, error) {
	var out Collection
	if err := c.doJSON(ctx, http.MethodPost, sitePath(ref)+"/collections", nil, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateCollection changes a collection.
func (c *Client) UpdateCollection(ctx context.Context, ref, name string, in CollectionPatch) (*Collection, error) {
	var out Collection
	if err := c.doJSON(ctx, http.MethodPatch, collectionPath(ref, name), nil, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteCollection removes a collection; confirm must repeat its name.
func (c *Client) DeleteCollection(ctx context.Context, ref, name, confirm string) error {
	return c.doJSON(ctx, http.MethodDelete, collectionPath(ref, name), nil, map[string]string{"confirm": confirm}, nil)
}

// ListDocuments returns a page of documents.
func (c *Client) ListDocuments(ctx context.Context, ref, name, status, cursor string, limit int) (*DocumentPage, error) {
	q := url.Values{}
	if status != "" {
		q.Set("status", status)
	}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	var out DocumentPage
	if err := c.doJSON(ctx, http.MethodGet, collectionPath(ref, name)+"/documents", q, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SaveDocuments writes documents; replace first removes every document.
func (c *Client) SaveDocuments(ctx context.Context, ref, name string, docs []DocumentWrite, replace bool) (*DocumentsWritten, error) {
	if docs == nil {
		docs = []DocumentWrite{}
	}
	var out DocumentsWritten
	body := map[string]any{"documents": docs, "replace": replace}
	if err := c.doJSON(ctx, http.MethodPost, collectionPath(ref, name)+"/documents", nil, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteDocuments removes documents by ID.
func (c *Client) DeleteDocuments(ctx context.Context, ref, name string, ids []string) (*DocumentCount, error) {
	if ids == nil {
		ids = []string{}
	}
	var out DocumentCount
	if err := c.doJSON(ctx, http.MethodPost, collectionPath(ref, name)+"/delete-documents", nil, map[string]any{"ids": ids}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ModerateDocuments approves or rejects pending documents.
func (c *Client) ModerateDocuments(ctx context.Context, ref, name string, ids []string, action string) (*DocumentCount, error) {
	if ids == nil {
		ids = []string{}
	}
	var out DocumentCount
	if err := c.doJSON(ctx, http.MethodPost, collectionPath(ref, name)+"/moderate", nil,
		map[string]any{"ids": ids, "action": action}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
