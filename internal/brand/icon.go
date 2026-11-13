package brand

import _ "embed" // the icon below

// Icon is the product icon (SVG) for the pages served outside the web
// interface: the visitor login of a protected site and the form reply page.
// It is a copy of internal/ui/static/brand/icon.svg; a test keeps the two equal.
//
//go:embed icon.svg
var Icon []byte
