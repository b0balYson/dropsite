package skills

import "strings"

// scenarioFiles are the guide sections on common site scenarios, shared by
// the MCP guide, the publish skill and the Codex snippet.
var scenarioFiles = []string{
	"external_backend.md", "payments.md", "booking.md", "legal.md", "seo.md", "multilingual.md", "quiz.md",
	"embeds.md",
}

// shared returns the guide sections every assistant gets, with headings
// demoted by levels: site quality, forms, then the scenarios.
func shared(levels int) []string {
	out := []string{demote(read("guide/assets.md"), levels), demote(read("guide/form_html.md"), levels)}
	for _, f := range scenarioFiles {
		out = append(out, demote(read("guide/"+f), levels))
	}
	return out
}

// composeSkill builds dropsite-publish/SKILL.md: the CLI steps, then the
// shared guide sections, then the CLI errors.
func composeSkill() string {
	parts := append([]string{read("parts/skill_head.md")}, shared(1)...)
	return join(append(parts, read("parts/skill_tail.md"))...)
}

// composeCodex builds codex/AGENTS.md, a snippet whose own heading is "##".
func composeCodex() string {
	parts := append([]string{read("parts/codex_head.md")}, shared(2)...)
	return join(append(parts, read("parts/codex_tail.md"))...)
}

func join(parts ...string) string {
	for i, p := range parts {
		parts[i] = strings.TrimSpace(p)
	}
	return strings.Join(parts, "\n\n") + "\n"
}

// demote adds levels to every Markdown heading outside code fences.
func demote(md string, levels int) string {
	lines := strings.Split(md, "\n")
	fence := false
	for i, l := range lines {
		if strings.HasPrefix(l, "```") {
			fence = !fence
			continue
		}
		if !fence && strings.HasPrefix(l, "#") {
			lines[i] = strings.Repeat("#", levels) + l
		}
	}
	return strings.Join(lines, "\n")
}
