// Package brand holds the product name and every identifier derived from it.
// Renaming the product means changing Name (and the CLI directory name).
package brand

// Name is the product name used in the CLI, UI, file names and token prefix.
const Name = "pushpage"

// DisplayName is the human-facing product name.
const DisplayName = "Pushpage"

// ProjectFile is the per-directory file the CLI writes to remember the site.
const ProjectFile = "." + Name + ".json"

// TokenPrefix starts every API token so it is recognisable in secret scanners.
const TokenPrefix = "pp_"

// ReservedPathPrefixes are URL path prefixes owned by the platform on every site.
var ReservedPathPrefixes = []string{"/_form", "/_" + Name}

// Version is overwritten at build time with -ldflags "-X .../brand.Version=...".
var Version = "dev"
