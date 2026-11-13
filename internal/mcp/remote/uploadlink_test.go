package remote

import (
	"strings"
	"testing"
)

func TestRemoteBeginUploadGivesLink(t *testing.T) {
	r := newRig(t)
	cs := r.session(r.env.Token(r.env.User("alice@pp.test"), "mcp"))
	out, msg := call(t, cs, "begin_upload", map[string]any{"title": "Frames"})
	if msg != "" {
		t.Fatal(msg)
	}
	link, _ := out["upload_url"].(string)
	cmd, _ := out["upload_command"].(string)
	next, _ := out["next_steps"].(string)
	if !strings.HasPrefix(link, "https://") || !strings.Contains(link, "/u/") || out["upload_url_expires_at"] == "" {
		t.Fatalf("link: %v", out)
	}
	if !strings.Contains(cmd, "tar -C <folder> -cf - .") || !strings.Contains(cmd, "'"+link+"'") ||
		!strings.Contains(cmd, "application/x-tar") {
		t.Fatalf("command: %q", cmd)
	}
	for _, want := range []string{"upload_command", "upload_url", "commit_upload", "Never use tunnels", "drag"} {
		if !strings.Contains(next, want) {
			t.Errorf("next_steps lacks %q", want)
		}
	}
	// Later results do not repeat the link.
	out, msg = call(t, cs, "upload_files", map[string]any{"upload_id": out["upload_id"],
		"files": []map[string]string{{"path": "index.html", "content": "x"}}})
	if msg != "" || out["upload_url"] != nil {
		t.Fatalf("upload_files: %v %q", out, msg)
	}
}

func TestRemoteTooLargeAdvisesUploadLink(t *testing.T) {
	r := newRig(t)
	cs := r.session(r.env.Token(r.env.User("alice@pp.test"), "mcp"))
	big := strings.Repeat("a", 16<<20+1)
	_, msg := call(t, cs, "deploy_files", map[string]any{"files": []map[string]string{{"path": "index.html", "content": big}}})
	for _, want := range []string{"begin_upload", "upload_command", "upload_url", "Never use tunnels"} {
		if !strings.Contains(msg, want) {
			t.Errorf("too-large error lacks %q: %s", want, msg)
		}
	}
}
