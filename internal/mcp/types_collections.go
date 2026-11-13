package mcp

import "context"

// CollectionsBackend manages the collections (site data) of the user's
// sites. Both servers implement it: the remote one over the service, the
// local one over the HTTP API.
type CollectionsBackend interface {
	ListCollections(ctx context.Context, ref string) ([]Collection, error)
	CreateCollection(ctx context.Context, ref string, in CollectionInput) (Collection, error)
	UpdateCollection(ctx context.Context, ref, name string, in CollectionPatch) (Collection, error)
	DeleteCollection(ctx context.Context, ref, name, confirm string) error
	ListDocuments(ctx context.Context, ref, name string, q DocumentQuery) (DocumentPage, error)
	SaveDocuments(ctx context.Context, ref, name string, docs []DocumentInput, replace bool) ([]string, Collection, error)
	DeleteDocuments(ctx context.Context, ref, name string, ids []string) (int, Collection, error)
	ModerateDocuments(ctx context.Context, ref, name string, ids []string, approve bool) (int, Collection, error)
	ListCollectionTemplates(ctx context.Context) ([]CollectionTemplate, error)
}

// CollectionInput creates a collection; Schema nil means none. Template
// names a built-in template; WithSamples also writes its samples.
type CollectionInput struct {
	Name        string
	Title       string
	Mode        string
	Schema      map[string]any
	Template    string
	WithSamples bool
}

// CollectionPatch changes a collection; nil fields stay. RemoveSchema
// drops the schema.
type CollectionPatch struct {
	Title        *string
	Mode         *string
	Schema       map[string]any
	RemoveSchema bool
}

// DocumentQuery selects documents for list_documents.
type DocumentQuery struct {
	Status string
	Cursor string
	Limit  int
}

// DocumentPage is one page of documents with its continuation.
type DocumentPage struct {
	Documents  []Document
	NextCursor string
}

// DocumentInput is one document of save_documents.
type DocumentInput struct {
	ID       string         `json:"id,omitempty" jsonschema:"document ID (a-z, 0-9, - and _); an existing ID is replaced, omit it to add a new document"`
	Data     map[string]any `json:"data" jsonschema:"the document: a JSON object of at most 16 KB, e.g. {\"name\": \"Cappuccino\", \"price\": 250}"`
	Position *int64         `json:"position,omitempty" jsonschema:"sort position (smaller first); omit it to keep the stored one, new documents get 0"`
}

// Collection is a collection of a site.
type Collection struct {
	Name      string         `json:"name"`
	Title     string         `json:"title"`
	Mode      string         `json:"mode" jsonschema:"public (only the owner writes) or moderated (visitors propose, the owner approves)"`
	Schema    map[string]any `json:"schema,omitempty" jsonschema:"JSON Schema of the documents, if any"`
	Documents int            `json:"documents" jsonschema:"number of documents, pending ones included"`
	Pending   int            `json:"pending" jsonschema:"documents proposed by visitors and waiting for approval"`
	PublicURL string         `json:"public_url" jsonschema:"where the site's pages read the collection; in the site's HTML use the relative path /_dropsite/data/<name>"`
	Snippet   string         `json:"snippet,omitempty" jsonschema:"ready HTML and JavaScript that loads the collection and keeps the fallback content on error"`
}

// Document is one document of a collection.
type Document struct {
	ID        string         `json:"id"`
	Data      map[string]any `json:"data"`
	Position  int64          `json:"position"`
	Status    string         `json:"status" jsonschema:"approved (public) or pending (waiting for approval)"`
	CreatedAt string         `json:"created_at"`
	UpdatedAt string         `json:"updated_at"`
}

// CollectionList is the output of list_collections.
type CollectionList struct {
	Collections []Collection `json:"collections"`
	Summary     string       `json:"summary" jsonschema:"what to tell the user"`
}

// CollectionSaved is the output of save_collection.
type CollectionSaved struct {
	Collection Collection `json:"collection"`
	Created    bool       `json:"created" jsonschema:"true when the collection was created by this call"`
	Summary    string     `json:"summary" jsonschema:"what to tell the user"`
}

// CollectionDeleted is the output of delete_collection.
type CollectionDeleted struct {
	Deleted string `json:"deleted" jsonschema:"name of the deleted collection"`
	Summary string `json:"summary"`
}

// DocumentList is the output of list_documents.
type DocumentList struct {
	Documents  []Document `json:"documents"`
	NextCursor string     `json:"next_cursor,omitempty" jsonschema:"pass as cursor to get the next page; empty on the last page"`
	PublicURL  string     `json:"public_url"`
	Summary    string     `json:"summary" jsonschema:"what to tell the user"`
}

// DocumentsChanged is the output of the document writes.
type DocumentsChanged struct {
	IDs       []string `json:"ids,omitempty" jsonschema:"IDs of the written documents, in the order given"`
	Count     int      `json:"count" jsonschema:"documents written, deleted, approved or rejected"`
	Documents int      `json:"documents" jsonschema:"documents in the collection now"`
	Pending   int      `json:"pending" jsonschema:"documents waiting for approval now"`
	PublicURL string   `json:"public_url" jsonschema:"where the site reads the collection; no new release is needed"`
	Summary   string   `json:"summary" jsonschema:"what to tell the user"`
}
