package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// NotFoundCollection is the not-found text of the collection tools.
const NotFoundCollection = "not found: no such site or collection among this user's sites; " +
	"call list_sites and list_collections to see valid names"

// maxDocumentPage mirrors the server's page limit of list_documents.
const maxDocumentPage = 500

var errNoCollections = errors.New("this server cannot manage site data; use the web interface of Dropsite")

// ErrCollectionConfirm is returned by delete_collection when confirm does
// not repeat the collection name.
var ErrCollectionConfirm = errors.New("not deleted: ask the user to confirm, then call delete_collection again " +
	"with confirm set to the collection name")

type saveCollectionArgs struct {
	Site         string         `json:"site" jsonschema:"site ID or slug"`
	Name         string         `json:"name" jsonschema:"collection name: 1-64 characters a-z, 0-9, - and _, e.g. menu or reviews; it is part of the address /_dropsite/data/<name> and cannot be changed later"`
	Title        string         `json:"title,omitempty" jsonschema:"human-readable title shown in the web interface; empty keeps the current one"`
	Mode         string         `json:"mode,omitempty" jsonschema:"public (default: everybody reads, only the owner writes; menus, prices, catalogs, schedules) or moderated (visitors propose documents, only approved ones are shown; reviews, questions); empty keeps the current mode"`
	Schema       map[string]any `json:"schema,omitempty" jsonschema:"JSON Schema with type object describing the documents; required for moderated collections, where visitors may send only the properties it lists"`
	RemoveSchema bool           `json:"remove_schema,omitempty" jsonschema:"true to drop the schema of a public collection"`
	Template     string         `json:"template,omitempty" jsonschema:"only when creating: a template from list_collection_templates (menu, catalog, services, events, team, faq, reviews); it fills the title, mode and schema you leave empty"`
	WithSamples  bool           `json:"with_samples,omitempty" jsonschema:"only with template: also add the template's sample documents, to replace with the user's real content"`
}

type deleteCollectionArgs struct {
	Site    string `json:"site" jsonschema:"site ID or slug"`
	Name    string `json:"name" jsonschema:"collection name"`
	Confirm string `json:"confirm" jsonschema:"the collection name typed again after the user explicitly confirmed the deletion"`
}

type listDocumentsArgs struct {
	Site       string `json:"site" jsonschema:"site ID or slug"`
	Collection string `json:"collection" jsonschema:"collection name"`
	Status     string `json:"status,omitempty" jsonschema:"approved, pending (proposals waiting for approval) or all (default)"`
	Limit      int    `json:"limit,omitempty" jsonschema:"documents per page, 1-500 (default 100)"`
	Cursor     string `json:"cursor,omitempty" jsonschema:"next_cursor of the previous page"`
}

type saveDocumentsArgs struct {
	Site       string          `json:"site" jsonschema:"site ID or slug"`
	Collection string          `json:"collection" jsonschema:"collection name"`
	Documents  []DocumentInput `json:"documents" jsonschema:"up to 1000 documents; all are written or none"`
	Replace    bool            `json:"replace,omitempty" jsonschema:"true to remove every document of the collection first and keep exactly these; only after the user confirmed"`
}

type documentIDsArgs struct {
	Site       string   `json:"site" jsonschema:"site ID or slug"`
	Collection string   `json:"collection" jsonschema:"collection name"`
	IDs        []string `json:"ids" jsonschema:"document IDs from list_documents, up to 1000"`
}

type moderateArgs struct {
	Site       string   `json:"site" jsonschema:"site ID or slug"`
	Collection string   `json:"collection" jsonschema:"collection name"`
	IDs        []string `json:"ids" jsonschema:"IDs of pending documents from list_documents, up to 1000"`
	Action     string   `json:"action" jsonschema:"approve (show on the site) or reject (delete)"`
}

func (t tools) registerCollections(s *sdk.Server, o Options) {
	if !o.Collections {
		return
	}
	sdk.AddTool(s, &sdk.Tool{Name: "list_collections", Annotations: readOnly("List site data"),
		Description: "List the data collections of a site (\"I want to change prices from the chat\"): small sets of JSON documents (menu items, prices, " +
			"catalog entries, schedule, reviews) that the site's pages load from /_dropsite/data/<name>, so they " +
			"change from the chat without a new release. Shows mode, schema, document and pending counts, the " +
			"public address and snippet: ready JavaScript that loads the collection and keeps the HTML fallback " +
			"when loading fails. Keep content that changes often in a collection rather than in files; see " +
			"get_guide topic data."}, t.listCollections)
	sdk.AddTool(s, &sdk.Tool{Name: "save_collection", Annotations: writes("Save collection", false, true),
		Description: "Create a collection of a site, or change the title, mode or schema of an existing one. " +
			"public: everybody reads, only the owner writes (menu, prices, catalog, schedule). moderated: " +
			"visitors propose documents with POST /_dropsite/data/<name>, only approved ones are shown (reviews, " +
			"questions); it needs a schema listing the fields visitors may send. A new schema applies only to " +
			"documents written afterwards. At most 20 collections per site. For a typical list (menu, catalog, " +
			"price list, events, team, FAQ, reviews) create it with template, after list_collection_templates."}, t.saveCollection)
	t.registerCollectionTemplates(s)
	sdk.AddTool(s, &sdk.Tool{Name: "delete_collection", Annotations: writes("Delete collection", true, true),
		Description: "Delete a collection with all its documents; pages that load it will show their fallback " +
			"content. Cannot be undone. Only after the user explicitly confirmed: say which collection and how " +
			"many documents will be deleted, wait for yes, then pass the name again in confirm."}, t.deleteCollection)
	sdk.AddTool(s, &sdk.Tool{Name: "list_documents", Annotations: readOnly("List documents"),
		Description: "List the documents of a collection, in display order, with their IDs and data; status " +
			"pending shows visitor proposals waiting for approval. Call it before changing documents, e.g. to " +
			"find the ID of the item whose price the user wants to change."}, t.listDocuments)
	sdk.AddTool(s, &sdk.Tool{Name: "save_documents", Annotations: writes("Save documents", true, false),
		Description: "Add or change items of a collection (\"the cappuccino now costs 250\", \"add a dish\"): a " +
			"document with an existing ID is replaced as a whole, one without ID is added. Changes are live on the " +
			"site at once, without a new release or draft, and are not undone by a rollback. All documents are " +
			"written or none (an error names the document index and field). replace true first removes every " +
			"document of the collection: ask the user to confirm before using it. A collection linked to a Google " +
			"Sheet (get_sheet_link) is overwritten by the next synchronization: change the sheet instead. Tell the " +
			"user what changed and give the site link."}, t.saveDocuments)
	sdk.AddTool(s, &sdk.Tool{Name: "delete_documents", Annotations: writes("Delete documents", true, true),
		Description: "Delete documents of a collection by ID; they disappear from the site at once and cannot be " +
			"restored. Only after the user explicitly confirmed: say which items will be deleted and wait for yes " +
			"(this tool has no confirm parameter)."}, t.deleteDocuments)
	sdk.AddTool(s, &sdk.Tool{Name: "moderate_documents", Annotations: writes("Approve or reject", true, true),
		Description: "Approve pending visitor proposals of a moderated collection (they appear on the site at " +
			"once) or reject them (they are deleted), e.g. \"show new reviews\". Show the user the pending documents " +
			"from list_documents and act only on their explicit decision; rejecting needs them to confirm which ones."}, t.moderateDocuments)
}

func (t tools) collections(ctx context.Context, req *sdk.CallToolRequest) (CollectionsBackend, error) {
	b, err := t.resolve(ctx, req)
	if err != nil {
		return nil, err
	}
	c, ok := b.(CollectionsBackend)
	if !ok {
		return nil, errNoCollections
	}
	return c, nil
}

func normName(name string) string { return strings.ToLower(strings.TrimSpace(name)) }

// findCollection returns the collection of the site with the name, if any.
func findCollection(ctx context.Context, b CollectionsBackend, site, name string) (Collection, bool, error) {
	cs, err := b.ListCollections(ctx, site)
	if err != nil {
		return Collection{}, false, err
	}
	for _, c := range cs {
		if c.Name == normName(name) {
			return c, true, nil
		}
	}
	return Collection{}, false, nil
}

// CollectionSnippet is HTML that loads a collection and renders its
// documents, keeping the fallback content of the page on any error.
func CollectionSnippet(name string) string {
	id := name + "-list"
	return `<div id="` + id + `">
  <!-- fallback content: shown when the data cannot be loaded and read by search engines -->
</div>
<script>
fetch('/_dropsite/data/` + name + `')
  .then((r) => (r.ok ? r.json() : Promise.reject(r.status)))
  .then(({ items }) => {
    const box = document.getElementById('` + id + `');
    box.replaceChildren(...items.map(({ data }) => {
      const row = document.createElement('div');
      row.textContent = Object.values(data).join(' — '); // textContent: never innerHTML with data
      return row;
    }));
  })
  .catch(() => { /* keep the fallback content */ });
</script>`
}

func withSnippets(cs []Collection) []Collection {
	out := make([]Collection, 0, len(cs))
	for _, c := range cs {
		c.Snippet = snippetOf(c)
		out = append(out, c)
	}
	return out
}

func (t tools) listCollections(ctx context.Context, req *sdk.CallToolRequest, in siteArgs) (*sdk.CallToolResult, CollectionList, error) {
	b, err := t.collections(ctx, req)
	if err != nil {
		return nil, CollectionList{}, err
	}
	cs, err := b.ListCollections(ctx, in.Site)
	if err != nil {
		return nil, CollectionList{}, err
	}
	out := CollectionList{Collections: withSnippets(cs)}
	pending := 0
	for _, c := range cs {
		pending += c.Pending
	}
	switch {
	case len(cs) == 0:
		out.Summary = "The site has no data collections yet; save_collection creates one."
	case pending > 0:
		out.Summary = fmt.Sprintf("%d collection(s); %d visitor proposal(s) wait for approval.", len(cs), pending)
	default:
		out.Summary = fmt.Sprintf("%d collection(s).", len(cs))
	}
	return nil, out, nil
}

func (t tools) saveCollection(ctx context.Context, req *sdk.CallToolRequest, in saveCollectionArgs) (*sdk.CallToolResult, CollectionSaved, error) {
	b, err := t.collections(ctx, req)
	if err != nil {
		return nil, CollectionSaved{}, err
	}
	cur, exists, err := findCollection(ctx, b, in.Site, in.Name)
	if err != nil {
		return nil, CollectionSaved{}, err
	}
	mode := strings.ToLower(strings.TrimSpace(in.Mode))
	template := strings.ToLower(strings.TrimSpace(in.Template))
	if !exists {
		c, err := b.CreateCollection(ctx, in.Site, CollectionInput{Name: in.Name, Title: in.Title, Mode: mode, Schema: in.Schema,
			Template: template, WithSamples: in.WithSamples})
		if err != nil {
			return nil, CollectionSaved{}, err
		}
		c.Snippet = snippetOf(c)
		return nil, CollectionSaved{Collection: c, Created: true, Summary: createdSummary(c, template)}, nil
	}
	if template != "" || in.WithSamples {
		return nil, CollectionSaved{}, fmt.Errorf("collection %s already exists: a template applies only when a collection "+
			"is created; pass schema to change its schema, or save_documents to add documents", cur.Name)
	}
	p := CollectionPatch{Schema: in.Schema, RemoveSchema: in.RemoveSchema}
	if in.Title != "" {
		p.Title = &in.Title
	}
	if mode != "" {
		p.Mode = &mode
	}
	c, err := b.UpdateCollection(ctx, in.Site, cur.Name, p)
	if err != nil {
		return nil, CollectionSaved{}, err
	}
	c.Snippet = snippetOf(c)
	summary := fmt.Sprintf("Collection %s saved (%s).", c.Name, c.Mode)
	if in.Schema != nil && c.Documents > 0 {
		summary += fmt.Sprintf(" The new schema applies only to documents written from now on; the %d existing "+
			"document(s) were not checked against it.", c.Documents)
	}
	return nil, CollectionSaved{Collection: c, Summary: summary}, nil
}

func (t tools) deleteCollection(ctx context.Context, req *sdk.CallToolRequest, in deleteCollectionArgs) (*sdk.CallToolResult, CollectionDeleted, error) {
	b, err := t.collections(ctx, req)
	if err != nil {
		return nil, CollectionDeleted{}, err
	}
	cur, exists, err := findCollection(ctx, b, in.Site, in.Name)
	if err != nil {
		return nil, CollectionDeleted{}, err
	}
	if !exists {
		return nil, CollectionDeleted{}, errors.New(NotFoundCollection)
	}
	if normName(in.Confirm) != cur.Name {
		return nil, CollectionDeleted{}, ErrCollectionConfirm
	}
	if err := b.DeleteCollection(ctx, in.Site, cur.Name, cur.Name); err != nil {
		return nil, CollectionDeleted{}, err
	}
	return nil, CollectionDeleted{Deleted: cur.Name, Summary: fmt.Sprintf(
		"Collection %s and its %d document(s) were deleted.", cur.Name, cur.Documents)}, nil
}

func (t tools) listDocuments(ctx context.Context, req *sdk.CallToolRequest, in listDocumentsArgs) (*sdk.CallToolResult, DocumentList, error) {
	b, err := t.collections(ctx, req)
	if err != nil {
		return nil, DocumentList{}, err
	}
	if in.Limit < 0 || in.Limit > maxDocumentPage {
		return nil, DocumentList{}, fmt.Errorf("limit must be from 1 to %d", maxDocumentPage)
	}
	cur, exists, err := findCollection(ctx, b, in.Site, in.Collection)
	if err != nil {
		return nil, DocumentList{}, err
	}
	if !exists {
		return nil, DocumentList{}, errors.New(NotFoundCollection)
	}
	p, err := b.ListDocuments(ctx, in.Site, cur.Name, DocumentQuery{Status: strings.ToLower(strings.TrimSpace(in.Status)),
		Cursor: in.Cursor, Limit: in.Limit})
	if err != nil {
		return nil, DocumentList{}, err
	}
	docs := p.Documents
	if docs == nil {
		docs = []Document{}
	}
	summary := fmt.Sprintf("%d document(s) shown; the collection has %d, %d waiting for approval.",
		len(docs), cur.Documents, cur.Pending)
	if p.NextCursor != "" {
		summary += " More follow: call again with cursor."
	}
	return nil, DocumentList{Documents: docs, NextCursor: p.NextCursor, PublicURL: cur.PublicURL, Summary: summary}, nil
}

func changed(c Collection, ids []string, n int, what string) DocumentsChanged {
	return DocumentsChanged{IDs: ids, Count: n, Documents: c.Documents, Pending: c.Pending, PublicURL: c.PublicURL,
		Summary: fmt.Sprintf("%d document(s) %s in %s; the site shows the change at once, no new release was made. "+
			"The collection now has %d document(s), %d waiting for approval.", n, what, c.Name, c.Documents, c.Pending)}
}

func (t tools) saveDocuments(ctx context.Context, req *sdk.CallToolRequest, in saveDocumentsArgs) (*sdk.CallToolResult, DocumentsChanged, error) {
	b, err := t.collections(ctx, req)
	if err != nil {
		return nil, DocumentsChanged{}, err
	}
	ids, c, err := b.SaveDocuments(ctx, in.Site, normName(in.Collection), in.Documents, in.Replace)
	if err != nil {
		return nil, DocumentsChanged{}, err
	}
	what := "written"
	if in.Replace {
		what = "written (the previous content was replaced)"
	}
	return nil, changed(c, ids, len(ids), what), nil
}

func (t tools) deleteDocuments(ctx context.Context, req *sdk.CallToolRequest, in documentIDsArgs) (*sdk.CallToolResult, DocumentsChanged, error) {
	b, err := t.collections(ctx, req)
	if err != nil {
		return nil, DocumentsChanged{}, err
	}
	n, c, err := b.DeleteDocuments(ctx, in.Site, normName(in.Collection), in.IDs)
	if err != nil {
		return nil, DocumentsChanged{}, err
	}
	return nil, changed(c, nil, n, "deleted"), nil
}

func (t tools) moderateDocuments(ctx context.Context, req *sdk.CallToolRequest, in moderateArgs) (*sdk.CallToolResult, DocumentsChanged, error) {
	b, err := t.collections(ctx, req)
	if err != nil {
		return nil, DocumentsChanged{}, err
	}
	action := strings.ToLower(strings.TrimSpace(in.Action))
	if action != "approve" && action != "reject" {
		return nil, DocumentsChanged{}, errors.New("action must be approve or reject")
	}
	n, c, err := b.ModerateDocuments(ctx, in.Site, normName(in.Collection), in.IDs, action == "approve")
	if err != nil {
		return nil, DocumentsChanged{}, err
	}
	what := "approved and shown on the site"
	if action == "reject" {
		what = "rejected and deleted"
	}
	return nil, changed(c, nil, n, what), nil
}
