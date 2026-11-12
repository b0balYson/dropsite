// Package skills embeds the instructions for AI assistants. One guide
// (guide/*.md) is the single source: the MCP servers send its instructions
// and serve its topics through get_guide, and the publish skill and the
// Codex snippet are composed from the same files (see compose.go).
// `dropsite setup` installs the publish skill into Claude Code, Codex and
// Cursor.
package skills

import (
	"embed"
	"strings"
)

// Publish is skills/dropsite-publish/SKILL.md, shared by Claude Code, Codex
// and Cursor (all read the same SKILL.md format).
//
//go:embed dropsite-publish/SKILL.md
var Publish string

// Codex is skills/codex/AGENTS.md, a snippet for a project's AGENTS.md.
//
//go:embed codex/AGENTS.md
var Codex string

//go:embed guide/*.md parts/*.md
var files embed.FS

// Instructions is the text MCP servers send on initialize. Its first
// paragraph holds the essentials: ChatGPT weighs the first 512 characters
// most.
var Instructions = strings.TrimSpace(read("guide/instructions.md"))

// TopicAll asks get_guide for every topic at once.
const TopicAll = "all"

// topics lists the guide topics in reading order with their files.
var topics = []struct {
	name  string
	files []string
}{
	{"overview", []string{"overview.md"}},
	{"create", []string{"create.md"}},
	{"update", []string{"update.md"}},
	{"large_sites", []string{"large_sites.md"}},
	{"assets", []string{"assets.md"}},
	{"forms", []string{"form_html.md", "forms.md"}},
	{"stats", []string{"stats.md"}},
	{"addresses", []string{"addresses.md"}},
	{"protection", []string{"protection.md"}},
	{"publishing", []string{"publishing.md"}},
	{"rollback", []string{"rollback.md"}},
	{"account", []string{"account.md"}},
	{"errors", []string{"errors.md"}},
}

// Topics returns the guide topic names in reading order, without "all".
func Topics() []string {
	out := make([]string, 0, len(topics))
	for _, t := range topics {
		out = append(out, t.name)
	}
	return out
}

// Guide returns one topic of the guide, or every topic for "all".
func Guide(topic string) (string, bool) {
	topic = strings.ToLower(strings.TrimSpace(topic))
	if topic == "" {
		topic = TopicAll
	}
	var b strings.Builder
	found := false
	for _, t := range topics {
		if topic != TopicAll && topic != t.name {
			continue
		}
		found = true
		for _, f := range t.files {
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			b.WriteString(read("guide/" + f))
		}
	}
	return b.String(), found
}

// read returns an embedded file; the names are fixed at build time and
// covered by tests, so a missing file is a programming error.
func read(name string) string {
	data, err := files.ReadFile(name)
	if err != nil {
		panic("skills: " + err.Error())
	}
	return string(data)
}
