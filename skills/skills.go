// Package skills embeds the harness instructions. `dropsite setup` installs
// the publish skill into Claude Code, Codex and Cursor.
package skills

import _ "embed"

// Publish is skills/dropsite-publish/SKILL.md, shared by Claude Code, Codex
// and Cursor (all read the same SKILL.md format).
//
//go:embed dropsite-publish/SKILL.md
var Publish string

// Codex is skills/codex/AGENTS.md, a snippet for a project's AGENTS.md.
//
//go:embed codex/AGENTS.md
var Codex string
