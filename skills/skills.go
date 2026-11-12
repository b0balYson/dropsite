// Package skills embeds the harness instructions so the web interface can
// show them on the "Connect AI" page.
package skills

import _ "embed"

// ClaudeCode is skills/claude-code/SKILL.md.
//
//go:embed claude-code/SKILL.md
var ClaudeCode string

// Codex is skills/codex/AGENTS.md.
//
//go:embed codex/AGENTS.md
var Codex string
