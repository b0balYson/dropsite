package skills

import "strings"

// composeSkill builds dropsite-publish/SKILL.md: the CLI steps, then the
// shared guide sections on site quality and forms, then the CLI errors.
func composeSkill() string {
	return join(
		read("parts/skill_head.md"),
		demote(read("guide/assets.md"), 1),
		demote(read("guide/form_html.md"), 1),
		read("parts/skill_tail.md"),
	)
}

// composeCodex builds codex/AGENTS.md, a snippet whose own heading is "##".
func composeCodex() string {
	return join(
		read("parts/codex_head.md"),
		demote(read("guide/assets.md"), 2),
		demote(read("guide/form_html.md"), 2),
		read("parts/codex_tail.md"),
	)
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
