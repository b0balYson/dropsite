package service

import "github.com/b0balYson/dropsite/internal/store"

// Aliases let adapters use list filters and rows without importing store.
type (
	Page        = store.Page
	UserFilter  = store.UserFilter
	UserSummary = store.UserSummary
	AuditFilter = store.AuditFilter
	AuditRow    = store.AuditRow
)

// Page size bounds, re-exported for adapters.
const (
	DefaultPageSize = store.DefaultPageSize
	MaxPageSize     = store.MaxPageSize
)
