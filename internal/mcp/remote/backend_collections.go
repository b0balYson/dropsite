package remote

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/mcp"
	"github.com/b0balYson/dropsite/internal/service"
)

var _ mcp.CollectionsBackend = backend{}

// collectionsError is toolError with a not-found text that names
// collections too.
func (b backend) collectionsError(err error) error {
	if err == nil {
		return nil
	}
	if e := domain.AsError(err); e.Kind == domain.KindNotFound {
		return errors.New(mcp.NotFoundCollection)
	}
	return b.toolError(err)
}

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

func toCollection(c service.CollectionView) mcp.Collection {
	return mcp.Collection{Name: c.Name, Title: c.Title, Mode: string(c.Mode), Schema: jsonObject(c.Schema),
		Documents: c.Documents, Pending: c.Pending, PublicURL: c.PublicURL}
}

func toDocument(d domain.Document) mcp.Document {
	data := jsonObject(d.Data)
	if data == nil {
		data = map[string]any{}
	}
	return mcp.Document{ID: d.ID, Data: data, Position: d.Position, Status: string(d.Status),
		CreatedAt: stamp(&d.CreatedAt), UpdatedAt: stamp(&d.UpdatedAt)}
}

func schemaRaw(m map[string]any) (json.RawMessage, error) {
	if m == nil {
		return nil, nil
	}
	return json.Marshal(m)
}

func (b backend) ListCollections(ctx context.Context, ref string) ([]mcp.Collection, error) {
	_, cs, err := b.svc.ListCollections(ctx, b.actor, ref)
	if err != nil {
		return nil, b.collectionsError(err)
	}
	out := make([]mcp.Collection, 0, len(cs))
	for _, c := range cs {
		out = append(out, toCollection(c))
	}
	return out, nil
}

func (b backend) CreateCollection(ctx context.Context, ref string, in mcp.CollectionInput) (mcp.Collection, error) {
	schema, err := schemaRaw(in.Schema)
	if err != nil {
		return mcp.Collection{}, err
	}
	c, err := b.svc.CreateCollection(ctx, b.actor, ref, service.CollectionInput{Name: in.Name, Title: in.Title,
		Mode: domain.CollectionMode(in.Mode), Schema: schema, Template: in.Template, WithSamples: in.WithSamples})
	if err != nil {
		return mcp.Collection{}, b.collectionsError(err)
	}
	return toCollection(c), nil
}

func (b backend) UpdateCollection(ctx context.Context, ref, name string, in mcp.CollectionPatch) (mcp.Collection, error) {
	u := service.CollectionUpdate{Title: in.Title}
	if in.Mode != nil {
		m := domain.CollectionMode(*in.Mode)
		u.Mode = &m
	}
	switch {
	case in.RemoveSchema:
		none := json.RawMessage("null")
		u.Schema = &none
	case in.Schema != nil:
		raw, err := schemaRaw(in.Schema)
		if err != nil {
			return mcp.Collection{}, err
		}
		u.Schema = &raw
	}
	c, err := b.svc.UpdateCollection(ctx, b.actor, ref, name, u)
	if err != nil {
		return mcp.Collection{}, b.collectionsError(err)
	}
	return toCollection(c), nil
}

func (b backend) DeleteCollection(ctx context.Context, ref, name, confirm string) error {
	return b.collectionsError(b.svc.DeleteCollection(ctx, b.actor, ref, name, confirm))
}

func (b backend) ListDocuments(ctx context.Context, ref, name string, q mcp.DocumentQuery) (mcp.DocumentPage, error) {
	p, err := b.svc.ListDocuments(ctx, b.actor, ref, name, service.DocumentQuery{Status: q.Status, Cursor: q.Cursor, Limit: q.Limit})
	if err != nil {
		return mcp.DocumentPage{}, b.collectionsError(err)
	}
	out := mcp.DocumentPage{Documents: make([]mcp.Document, 0, len(p.Items)), NextCursor: p.Next}
	for _, d := range p.Items {
		out.Documents = append(out.Documents, toDocument(d))
	}
	return out, nil
}

func (b backend) SaveDocuments(ctx context.Context, ref, name string, docs []mcp.DocumentInput, replace bool) ([]string, mcp.Collection, error) {
	in := make([]service.DocumentInput, 0, len(docs))
	for _, d := range docs {
		data, err := json.Marshal(d.Data)
		if err != nil {
			return nil, mcp.Collection{}, err
		}
		in = append(in, service.DocumentInput{ID: d.ID, Data: data, Position: d.Position})
	}
	res, err := b.svc.SaveDocuments(ctx, b.actor, ref, name, in, replace)
	if err != nil {
		return nil, mcp.Collection{}, b.collectionsError(err)
	}
	return res.IDs, toCollection(res.Collection), nil
}

func (b backend) DeleteDocuments(ctx context.Context, ref, name string, ids []string) (int, mcp.Collection, error) {
	n, c, err := b.svc.DeleteDocuments(ctx, b.actor, ref, name, ids)
	if err != nil {
		return 0, mcp.Collection{}, b.collectionsError(err)
	}
	return n, toCollection(c), nil
}

func (b backend) ModerateDocuments(ctx context.Context, ref, name string, ids []string, approve bool) (int, mcp.Collection, error) {
	n, c, err := b.svc.ModerateDocuments(ctx, b.actor, ref, name, ids, approve)
	if err != nil {
		return 0, mcp.Collection{}, b.collectionsError(err)
	}
	return n, toCollection(c), nil
}
