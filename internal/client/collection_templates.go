package client

import (
	"context"
	"encoding/json"
	"net/http"
)

// TemplateSample is one sample document of a collection template.
type TemplateSample struct {
	ID       string          `json:"id"`
	Position int64           `json:"position"`
	Data     json.RawMessage `json:"data"`
}

// CollectionTemplate is a built-in collection template.
type CollectionTemplate struct {
	Name        string           `json:"name"`
	Title       string           `json:"title"`
	Description string           `json:"description"`
	Mode        string           `json:"mode"`
	Schema      json.RawMessage  `json:"schema"`
	Samples     []TemplateSample `json:"samples"`
	// Snippet is for a collection named like the template.
	Snippet string `json:"snippet"`
}

// ListCollectionTemplates returns the server's collection templates.
func (c *Client) ListCollectionTemplates(ctx context.Context) ([]CollectionTemplate, error) {
	var out struct {
		Items []CollectionTemplate `json:"items"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "/collection-templates", nil, nil, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}
