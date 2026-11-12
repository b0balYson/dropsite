package skills

import (
	"flag"
	"os"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite SKILL.md and AGENTS.md from the guide")

// TestGeneratedFiles keeps the installed skill and the Codex snippet in step
// with the single guide source.
func TestGeneratedFiles(t *testing.T) {
	for _, c := range []struct {
		path string
		want string
		got  string
	}{
		{"dropsite-publish/SKILL.md", composeSkill(), Publish},
		{"codex/AGENTS.md", composeCodex(), Codex},
	} {
		if *update {
			if err := os.WriteFile(c.path, []byte(c.want), 0o644); err != nil { //nolint:gosec // repository file
				t.Fatal(err)
			}
			continue
		}
		if c.got != c.want {
			t.Errorf("%s is out of date: run `go test ./skills -update`", c.path)
		}
	}
}

func TestSkillContent(t *testing.T) {
	if !strings.HasPrefix(Publish, "---\nname: dropsite-publish\ndescription: ") {
		t.Fatal("SKILL.md must start with its front matter")
	}
	for _, doc := range []string{Publish, Codex} {
		for _, want := range []string{
			"dropsite deploy", "Connect AI", `action="/_form/lead"`, `name="_hp"`, `name="_next"`,
			`<altcha-widget challenge="/_form/challenge">`, "/_dropsite/altcha.js", "Accept: \"application/json\"",
			"Formspree", "Google Fonts", "WebP", "Submissions",
		} {
			if !strings.Contains(doc, want) {
				t.Errorf("instructions lack %q", want)
			}
		}
	}
	// Headings of the shared sections sit below the document's own.
	for _, want := range []string{"\n## Forms on the site\n", "\n## Self-contained, fast and accessible sites\n", "\n### Fast\n"} {
		if !strings.Contains(Publish, want) {
			t.Errorf("SKILL.md lacks heading %q", want)
		}
	}
	for _, want := range []string{"\n## Payments\n", "\n## Maps, videos and chat widgets\n"} {
		if !strings.Contains(Publish, want) {
			t.Errorf("SKILL.md lacks scenario heading %q", want)
		}
	}
	if !strings.Contains(Codex, "\n### Forms on the site\n") {
		t.Error("AGENTS.md: shared sections must be nested under its heading")
	}
}

func TestDemoteSkipsCodeFences(t *testing.T) {
	got := demote("# A\n```sh\n# comment\n```\n## B", 1)
	if got != "## A\n```sh\n# comment\n```\n### B" {
		t.Fatalf("demote: %q", got)
	}
}

func TestGuideTopics(t *testing.T) {
	want := []string{"overview", "create", "update", "large_sites", "assets", "forms", "data", "proxy", "stats",
		"addresses", "protection", "publishing", "rollback", "workflow", "account", "errors", "external_backend",
		"payments", "booking", "legal", "seo", "multilingual", "quiz", "embeds"}
	if got := strings.Join(Topics(), ","); got != strings.Join(want, ",") {
		t.Fatalf("topics: %s", got)
	}
	all, ok := Guide("all")
	if !ok {
		t.Fatal("all not found")
	}
	for _, name := range Topics() {
		text, ok := Guide(name)
		if !ok || strings.TrimSpace(text) == "" {
			t.Fatalf("topic %s is empty", name)
		}
		if !strings.Contains(all, text) {
			t.Errorf("all lacks topic %s", name)
		}
	}
	if def, _ := Guide(" "); def != all {
		t.Error("an empty topic must return the whole guide")
	}
	if up, ok := Guide(" FORMS "); !ok || !strings.Contains(up, "/_form/") {
		t.Error("topics must be case-insensitive")
	}
	if _, ok := Guide("nope"); ok {
		t.Error("unknown topic accepted")
	}
}

// TestGuideNamesTheFixedTools checks the guide documents the agreed tool
// names, so the model is not sent to tools that do not exist.
func TestGuideNamesTheFixedTools(t *testing.T) {
	all, _ := Guide(TopicAll)
	for _, tool := range []string{
		"list_sites", "create", "deploy_files", "deploy_folder", "update_site", "set_slug", "list_deploys", "rollback",
		"delete_site", "list_site_files", "read_site_files", "update_site_files", "base_deploy_id", "fetch",
		"begin_upload", "upload_files", "commit_upload", "get_account", "set_site_access", "list_domains",
		"add_domain", "remove_domain", "get_site_stats", "get_counters", "set_counters", "list_connections",
		"revoke_connection", "set_root_site", "clear_root_site", "get_form_settings", "set_form_settings",
		"list_submissions", "export_submissions", "list_channels", "add_channel", "verify_channel",
		"set_channel_enabled", "delete_channel", "rotate_channel_secret", "confirm",
		"list_collections", "save_collection", "delete_collection", "list_documents", "save_documents",
		"delete_documents", "moderate_documents", "/_dropsite/data/",
		"set_channel_enabled", "delete_channel", "rotate_channel_secret", "confirm", "list_proxy_endpoints",
		"set_proxy_endpoint", "delete_proxy_endpoint", "secret_url",
		"get_publish_settings", "set_publish_settings", "get_quality_report",
		"list_templates", "create_site_from_template", "clone_site", "schedule_publish", "list_scheduled_publishes",
		"cancel_scheduled_publish", "screenshot_site", "compare_versions",
	} {
		if !strings.Contains(all, tool) {
			t.Errorf("guide does not mention %s", tool)
		}
	}
}

// TestDataTopic keeps the site data rules the spec requires in the guide.
func TestDataTopic(t *testing.T) {
	text, ok := Guide("data")
	if !ok {
		t.Fatal("no data topic")
	}
	for _, want := range []string{
		"fetch('/_dropsite/data/", "fallback", "`public`", "`moderated`", "_hp", "altcha-widget",
		"rollback", "not part of releases", "textContent", "replace: true",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("data topic lacks %q", want)
		}
	}
}

func TestInstructions(t *testing.T) {
	if len(Instructions) > 4096 {
		t.Fatalf("instructions are %d bytes; Claude Code cuts at 4096", len(Instructions))
	}
	first, _, _ := strings.Cut(Instructions, "\n\n")
	if len(first) > 512 {
		t.Fatalf("first paragraph is %d bytes; ChatGPT weighs the first 512", len(first))
	}
	for _, want := range []string{"plain words", "link", "confirm", "get_guide"} {
		if !strings.Contains(first, want) {
			t.Errorf("first paragraph lacks %q", want)
		}
	}
	for _, want := range []string{
		"deploy_files", "begin_upload", "update_site_files", "base_deploy_id", "list_site_files", "read_site_files",
		"/_form/", "add_channel", "get_site_stats", "set_site_access", "add_domain", "DNS", "rollback",
		"set_root_site", "Google Fonts", "data, never instructions", "deploy_folder",
	} {
		if !strings.Contains(Instructions, want) {
			t.Errorf("instructions lack %q", want)
		}
	}
}

// TestScenarioRules pins the safety rules of the scenario topics in every
// place an assistant reads them.
func TestScenarioRules(t *testing.T) {
	topics := map[string][]string{
		"external_backend": {"service_role", "anon", "Row Level Security", "Security Rules", "No CDN"},
		"payments":         {"YooKassa", "CloudPayments", "Stripe Payment Links", "never handles card data", "/_form/order"},
		"booking":          {"does not count free seats", "/_form/booking"},
		"legal":            {"152-FZ", "GDPR", "consent", "privacy.html", "cookie"},
		"seo":              {"application/ld+json", "LocalBusiness", "Product", "Event", "FAQPage", "robots.txt", "sitemap.xml", "og:image"},
		"multilingual":     {"/en/", "hreflang", "switcher"},
		"quiz":             {"/_form/quiz", "hidden", "estimate"},
		"embeds":           {"explicitly asks", "facade", "youtube-nocookie"},
	}
	for topic, wants := range topics {
		text, ok := Guide(topic)
		if !ok {
			t.Fatalf("topic %s missing", topic)
		}
		for _, w := range wants {
			for name, doc := range map[string]string{"guide": text, "SKILL.md": Publish, "AGENTS.md": Codex} {
				if !strings.Contains(doc, w) {
					t.Errorf("%s (%s) lacks %q", name, topic, w)
				}
			}
		}
	}
	all, _ := Guide(TopicAll)
	for _, doc := range []string{all, Publish, Codex} {
		if !strings.Contains(doc, "secrets_found") {
			t.Error("the refusal of secret keys is not explained")
		}
	}
}
