package main

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func (e *testEnv) app() *app {
	return &app{
		stdout: io.Discard, stderr: io.Discard, stdin: strings.NewReader(""),
		getenv:     func(k string) string { return e.env[k] },
		configDir:  func() (string, error) { return e.cfgDir, nil },
		isTerminal: func() bool { return false },
		homeDir:    func() (string, error) { return e.home, nil },
		goos:       e.goos, executable: func() (string, error) { return e.exe, nil },
		hostname: func() (string, error) { return "h", nil },
	}
}

func mcpClient(t *testing.T, a *app) *sdk.ClientSession {
	t.Helper()
	ct, st := sdk.NewInMemoryTransports()
	ss, err := a.mcpServer().Connect(context.Background(), st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "t", Version: "1"}, nil).Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func callTool(t *testing.T, cs *sdk.ClientSession, name string, args map[string]any) (map[string]any, string) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if res.IsError {
		return nil, res.Content[0].(*sdk.TextContent).Text
	}
	var out map[string]any
	b, _ := json.Marshal(res.StructuredContent)
	_ = json.Unmarshal(b, &out)
	return out, ""
}

func TestLocalMCPNotConnected(t *testing.T) {
	e := newTestEnv(t)
	cs := mcpClient(t, e.app())
	if _, msg := callTool(t, cs, "list_sites", map[string]any{}); !strings.Contains(msg, "Connect AI") {
		t.Fatalf("not connected: %q", msg)
	}
	tools, _ := cs.ListTools(context.Background(), nil)
	names := []string{}
	for _, tl := range tools.Tools {
		names = append(names, tl.Name)
	}
	if !strings.Contains(strings.Join(names, " "), "deploy_folder") || strings.Contains(strings.Join(names, " "), "deploy_files") {
		t.Fatalf("tools: %v", names)
	}
}

func TestLocalMCPTools(t *testing.T) {
	e := newTestEnv(t)
	e.login()
	cs := mcpClient(t, e.app())
	dir := siteDir(t, nil)

	out, msg := callTool(t, cs, "deploy_folder", map[string]any{"path": dir, "title": "Mine"})
	if msg != "" || !strings.HasPrefix(out["url"].(string), "https://") {
		t.Fatalf("deploy_folder: %v %q", out, msg)
	}
	if _, err := os.Stat(filepath.Join(dir, ".dropsite.json")); err != nil {
		t.Fatal("project file not written")
	}
	site := out["site_id"].(string)
	if _, msg := callTool(t, cs, "deploy_folder", map[string]any{"path": "relative/dir"}); !strings.Contains(msg, "not absolute") {
		t.Fatalf("relative: %q", msg)
	}
	if _, msg := callTool(t, cs, "deploy_folder", map[string]any{"path": "~/missing"}); msg == "" {
		t.Fatal("missing folder accepted")
	}
	if _, msg := callTool(t, cs, "deploy_folder", map[string]any{"path": dir}); msg != "" {
		t.Fatalf("redeploy: %q", msg)
	}
	out, _ = callTool(t, cs, "list_sites", map[string]any{})
	if len(out["sites"].([]any)) != 1 {
		t.Fatalf("list: %v", out)
	}
	out, _ = callTool(t, cs, "get_site", map[string]any{"site": site})
	if out["current_deploy"] == nil {
		t.Fatalf("get: %v", out)
	}
	if _, msg := callTool(t, cs, "get_site", map[string]any{"site": "nope"}); !strings.Contains(msg, "list_sites") {
		t.Fatalf("missing: %q", msg)
	}
	out, _ = callTool(t, cs, "set_slug", map[string]any{"site": site, "slug": "mine"})
	if out["slug"] != "mine" {
		t.Fatalf("slug: %v", out)
	}
	if _, msg := callTool(t, cs, "set_slug", map[string]any{"site": site, "slug": "www"}); !strings.Contains(msg, "reserved") {
		t.Fatalf("reserved: %q", msg)
	}
	out, _ = callTool(t, cs, "list_deploys", map[string]any{"site": site})
	if len(out["deploys"].([]any)) != 2 {
		t.Fatalf("deploys: %v", out)
	}
	if out, msg := callTool(t, cs, "rollback", map[string]any{"site": site}); msg != "" || out["deploy_id"] == "" {
		t.Fatalf("rollback: %v %q", out, msg)
	}
	out, _ = callTool(t, cs, "create_site", map[string]any{"title": "Second"})
	if out["id"] == "" {
		t.Fatalf("create: %v", out)
	}
	if out, msg := callTool(t, cs, "delete_site", map[string]any{"site": site, "confirm": "mine"}); msg != "" || out["deleted"] != site {
		t.Fatalf("delete: %v %q", out, msg)
	}

	// An expired login is reported as "not connected".
	e.env[envToken] = "ds_revoked"
	if _, msg := callTool(t, cs, "list_sites", map[string]any{}); !strings.Contains(msg, "Connect AI") {
		t.Fatalf("revoked: %q", msg)
	}
}

func TestMCPCommandOverStdio(t *testing.T) {
	e := newTestEnv(t)
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	a := e.app()
	a.stdin, a.stdout = inR, outW
	done := make(chan int, 1)
	go func() { done <- a.run(context.Background(), []string{"mcp"}) }()
	go func() {
		_, _ = io.WriteString(inW, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`+"\n")
	}()
	line, err := bufio.NewReader(outR).ReadString('\n')
	if err != nil || !strings.Contains(line, `"serverInfo"`) || !strings.Contains(line, "deploy_folder") {
		t.Fatalf("stdio: %v %s", err, line)
	}
	_ = inW.Close()
	go func() { _, _ = io.Copy(io.Discard, outR) }()
	<-done
	expectCode(t, e.run("mcp", "extra"), 2)
}
