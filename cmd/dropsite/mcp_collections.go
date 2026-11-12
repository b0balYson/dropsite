package main

import (
	"context"
	"encoding/json"

	"github.com/b0balYson/dropsite/internal/client"
	"github.com/b0balYson/dropsite/internal/mcp"
)

// Site data tools of the local server, through the API.

var _ mcp.CollectionsBackend = localBackend{}

func collectionsError(err error) error { return toolErrorFor(err, mcp.NotFoundCollection) }

func jsonObject(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	return m
}

func fromCollection(c *client.Collection) mcp.Collection {
	return mcp.Collection{Name: c.Name, Title: c.Title, Mode: c.Mode, Schema: jsonObject(c.Schema),
		Documents: c.Documents, Pending: c.Pending, PublicURL: c.PublicURL}
}

func fromDocument(d client.Document) mcp.Document {
	data := jsonObject(d.Data)
	if data == nil {
		data = map[string]any{}
	}
	return mcp.Document{ID: d.ID, Data: data, Position: d.Position, Status: d.Status,
		CreatedAt: stampTime(&d.CreatedAt), UpdatedAt: stampTime(&d.UpdatedAt)}
}

func rawSchema(m map[string]any) (json.RawMessage, error) {
	if m == nil {
		return nil, nil
	}
	return json.Marshal(m)
}

func (b localBackend) ListCollections(ctx context.Context, ref string) ([]mcp.Collection, error) {
	cs, err := b.c.ListCollections(ctx, ref)
	if err != nil {
		return nil, collectionsError(err)
	}
	out := make([]mcp.Collection, 0, len(cs))
	for i := range cs {
		out = append(out, fromCollection(&cs[i]))
	}
	return out, nil
}

func (b localBackend) CreateCollection(ctx context.Context, ref string, in mcp.CollectionInput) (mcp.Collection, error) {
	schema, err := rawSchema(in.Schema)
	if err != nil {
		return mcp.Collection{}, err
	}
	c, err := b.c.CreateCollection(ctx, ref, client.CollectionCreate{Name: in.Name, Title: in.Title, Mode: in.Mode, Schema: schema})
	if err != nil {
		return mcp.Collection{}, collectionsError(err)
	}
	return fromCollection(c), nil
}

func (b localBackend) UpdateCollection(ctx context.Context, ref, name string, in mcp.CollectionPatch) (mcp.Collection, error) {
	p := client.CollectionPatch{Title: in.Title, Mode: in.Mode}
	switch {
	case in.RemoveSchema:
		p.Schema = json.RawMessage("null")
	case in.Schema != nil:
		raw, err := rawSchema(in.Schema)
		if err != nil {
			return mcp.Collection{}, err
		}
		p.Schema = raw
	}
	c, err := b.c.UpdateCollection(ctx, ref, name, p)
	if err != nil {
		return mcp.Collection{}, collectionsError(err)
	}
	return fromCollection(c), nil
}

func (b localBackend) DeleteCollection(ctx context.Context, ref, name, confirm string) error {
	return collectionsError(b.c.DeleteCollection(ctx, ref, name, confirm))
}

func (b localBackend) ListDocuments(ctx context.Context, ref, name string, q mcp.DocumentQuery) (mcp.DocumentPage, error) {
	p, err := b.c.ListDocuments(ctx, ref, name, q.Status, q.Cursor, q.Limit)
	if err != nil {
		return mcp.DocumentPage{}, collectionsError(err)
	}
	out := mcp.DocumentPage{Documents: make([]mcp.Document, 0, len(p.Items)), NextCursor: deref(p.NextCursor)}
	for _, d := range p.Items {
		out.Documents = append(out.Documents, fromDocument(d))
	}
	return out, nil
}

func (b localBackend) SaveDocuments(ctx context.Context, ref, name string, docs []mcp.DocumentInput, replace bool) ([]string, mcp.Collection, error) {
	in := make([]client.DocumentWrite, 0, len(docs))
	for _, d := range docs {
		data, err := json.Marshal(d.Data)
		if err != nil {
			return nil, mcp.Collection{}, err
		}
		in = append(in, client.DocumentWrite{ID: d.ID, Data: data, Position: d.Position})
	}
	w, err := b.c.SaveDocuments(ctx, ref, name, in, replace)
	if err != nil {
		return nil, mcp.Collection{}, collectionsError(err)
	}
	return w.IDs, fromCollection(&w.Collection), nil
}

func (b localBackend) DeleteDocuments(ctx context.Context, ref, name string, ids []string) (int, mcp.Collection, error) {
	r, err := b.c.DeleteDocuments(ctx, ref, name, ids)
	if err != nil {
		return 0, mcp.Collection{}, collectionsError(err)
	}
	return r.Count, fromCollection(&r.Collection), nil
}

func (b localBackend) ModerateDocuments(ctx context.Context, ref, name string, ids []string, approve bool) (int, mcp.Collection, error) {
	action := "reject"
	if approve {
		action = "approve"
	}
	r, err := b.c.ModerateDocuments(ctx, ref, name, ids, action)
	if err != nil {
		return 0, mcp.Collection{}, collectionsError(err)
	}
	return r.Count, fromCollection(&r.Collection), nil
}
