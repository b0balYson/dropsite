package domain

import (
	"encoding/json"
	"regexp"
	"strings"
	"time"
)

// Site collections (change site-collections): error codes and validation.
const (
	// CodeDocumentInvalid rejects a document that is not a JSON object, is
	// too large or does not match the collection schema; Params name the
	// document index and the field path.
	CodeDocumentInvalid = "document_invalid"
	// CodeCollectionPendingFull refuses a visitor proposal while the
	// collection holds the maximum number of pending documents.
	CodeCollectionPendingFull = "collection_pending_full"
	// CodeLimitExceeded is a collection limit: collections per site,
	// documents per collection or per write.
	CodeLimitExceeded = "limit_exceeded"
	// CodeCollectionInvalid covers an invalid collection name, title, mode
	// or schema.
	CodeCollectionInvalid = "collection_invalid"
	// CodeCollectionExists rejects a second collection with the same name.
	CodeCollectionExists = "collection_exists"
	// CodeCollectionReadOnly answers a visitor POST to a public collection.
	CodeCollectionReadOnly = "collection_read_only"
)

// ErrCollectionPendingFull refuses a visitor proposal to a full collection.
var ErrCollectionPendingFull = E(KindRateLimited, CodeCollectionPendingFull,
	"too many proposals are waiting for approval; try again later")

// CollectionMode is who may write a collection.
type CollectionMode string

const (
	// CollectionPublic: everybody reads, only the owner writes.
	CollectionPublic CollectionMode = "public"
	// CollectionModerated: visitors propose, approved documents are public.
	CollectionModerated CollectionMode = "moderated"
)

// Valid reports whether m is a known mode.
func (m CollectionMode) Valid() bool { return m == CollectionPublic || m == CollectionModerated }

// DocumentStatus tells whether a document is publicly visible.
type DocumentStatus string

const (
	DocumentApproved DocumentStatus = "approved"
	DocumentPending  DocumentStatus = "pending"
)

// Collection is a named set of JSON documents of a site. Schema is nil when
// the collection has none. Documents and Pending are counts filled by lists.
type Collection struct {
	SiteID      string
	Name        string
	Title       string
	Mode        CollectionMode
	Schema      json.RawMessage
	DataVersion int64
	Documents   int
	Pending     int
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Document is one JSON object of a collection.
type Document struct {
	SiteID     string
	Collection string
	ID         string
	Data       json.RawMessage
	Position   int64
	Status     DocumentStatus
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// MaxCollectionTitleLength bounds collection titles in characters.
const MaxCollectionTitleLength = 200

// keyPattern is the form of collection names and document IDs: 1–64
// characters a-z, 0-9, "-" and "_", starting with a letter or digit.
var keyPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// NormalizeCollectionName lower-cases a collection name; ok is false when
// the result is not a valid name.
func NormalizeCollectionName(name string) (string, bool) {
	n := strings.ToLower(strings.TrimSpace(name))
	return n, keyPattern.MatchString(n)
}

// NormalizeDocumentID lower-cases a document ID; ok is false when the
// result is not a valid ID.
func NormalizeDocumentID(id string) (string, bool) {
	return NormalizeCollectionName(id)
}
