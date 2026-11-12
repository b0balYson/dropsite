package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/b0balYson/dropsite/internal/brand"
	"github.com/b0balYson/dropsite/skills"
)

const (
	skillDirName  = brand.Name + "-publish"
	skillFileName = "SKILL.md"
	desktopConfig = "claude_desktop_config.json"
	mcpServersKey = "mcpServers"
)

// Setup statuses.
const (
	statusInstalled = "installed"
	statusUpdated   = "updated"
	statusUnchanged = "unchanged"
	statusRemoved   = "removed"
	statusAbsent    = "absent" // --remove: nothing of ours was there
	statusNotFound  = "not_found"
	statusSkipped   = "skipped"
	statusError     = "error"
)

// integration is one assistant setup can connect.
type integration struct {
	Name    string `json:"name"`
	Kind    string `json:"kind"` // skill or mcp
	Status  string `json:"status"`
	Path    string `json:"path,omitempty"`
	Restart bool   `json:"restart"`
	Message string `json:"message,omitempty"`

	detect string // settings directory whose presence means "installed"
}

type setupOutput struct {
	Integrations []integration `json:"integrations"`
	LoggedIn     bool          `json:"logged_in"`
}

func cmdSetup(_ context.Context, a *app, args []string) error {
	fs := a.newFlagSet("setup", "[--remove] [--json]",
		"Find AI assistants on this computer and connect them to "+brand.Name+":\n"+
			"the publish skill for Claude Code, Codex and Cursor, and the local MCP\n"+
			"server for Claude Desktop. Running it again changes nothing; --remove\n"+
			"takes out only what setup added.")
	remove := fs.Bool("remove", false, "remove the integrations instead of installing them")
	asJSON := fs.Bool("json", false, "print one JSON object")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if err := wantArgs(fs, pos, 0, 0); err != nil {
		return err
	}
	list, err := a.integrations()
	if err != nil {
		return err
	}
	for i := range list {
		a.apply(&list[i], *remove)
	}
	cfg, _ := a.loadConfig()
	out := setupOutput{Integrations: list, LoggedIn: cfg.Server != "" && cfg.Token != ""}
	if *asJSON {
		return writeJSON(a.stdout, out)
	}
	a.printSetup(out, *remove)
	return nil
}

// integrations lists the assistants and where their files live.
func (a *app) integrations() ([]integration, error) {
	home, err := a.homeDir()
	if err != nil {
		return nil, fmt.Errorf("locate home directory: %w", err)
	}
	skill := func(dir string) string { return filepath.Join(dir, skillDirName, skillFileName) }
	claudeDir := a.getenv("CLAUDE_CONFIG_DIR")
	if claudeDir == "" {
		claudeDir = filepath.Join(home, ".claude")
	}
	codexDir := a.getenv("CODEX_HOME")
	if codexDir == "" {
		codexDir = filepath.Join(home, ".codex")
	}
	cursorDir := filepath.Join(home, ".cursor")
	list := []integration{
		{Name: "Claude Code", Kind: "skill", detect: claudeDir, Path: skill(filepath.Join(claudeDir, "skills"))},
		// Codex reads user skills from ~/.agents/skills ($CODEX_HOME/skills is deprecated).
		{Name: "Codex", Kind: "skill", detect: codexDir, Path: skill(filepath.Join(home, ".agents", "skills"))},
		{Name: "Cursor", Kind: "skill", detect: cursorDir, Path: skill(filepath.Join(cursorDir, "skills"))},
	}
	desktop := integration{Name: "Claude Desktop", Kind: "mcp"}
	switch a.goos {
	case "darwin":
		desktop.detect = filepath.Join(home, "Library", "Application Support", "Claude")
	case "windows":
		appData := a.getenv("APPDATA")
		if appData == "" {
			appData = filepath.Join(home, "AppData", "Roaming")
		}
		desktop.detect = filepath.Join(appData, "Claude")
	default:
		desktop.Status, desktop.Message = statusSkipped, "Claude Desktop is not available on this system"
	}
	if desktop.detect != "" {
		desktop.Path = filepath.Join(desktop.detect, desktopConfig)
	}
	return append(list, desktop), nil
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

func (a *app) apply(it *integration, remove bool) {
	if it.Status == statusSkipped {
		return
	}
	if !isDir(it.detect) {
		it.Status = statusNotFound
		if remove {
			it.Status = statusAbsent
		}
		return
	}
	var err error
	switch {
	case it.Kind == "skill" && remove:
		it.Status, err = removeSkill(it.Path)
	case it.Kind == "skill":
		it.Status, err = installFile(it.Path, []byte(skills.Publish))
	case remove:
		it.Status, err = editDesktopConfig(it.Path, nil)
	default:
		var entry []byte
		if entry, err = a.desktopEntry(); err == nil {
			it.Status, err = editDesktopConfig(it.Path, entry)
		}
	}
	if err != nil {
		it.Status, it.Message = statusError, err.Error()
		return
	}
	it.Restart = it.Status != statusUnchanged && it.Status != statusAbsent
}

// installFile writes data atomically when the file is missing or different.
func installFile(path string, data []byte) (string, error) {
	old, err := os.ReadFile(path) //nolint:gosec // path is built from the user's own settings directories
	switch {
	case err == nil && bytes.Equal(old, data):
		return statusUnchanged, nil
	case err != nil && !errors.Is(err, fs.ErrNotExist):
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	status := statusInstalled
	if err == nil {
		status = statusUpdated
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { //nolint:gosec // skill directories are readable by the assistant
		return "", fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	if err := writeFileAtomic(path, data, 0o644); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	return status, nil
}

// removeSkill deletes our skill file and its directory if nothing else is in it.
func removeSkill(path string) (string, error) {
	if err := os.Remove(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return statusAbsent, nil
		}
		return "", fmt.Errorf("remove %s: %w", path, err)
	}
	_ = os.Remove(filepath.Dir(path)) // fails, as intended, if the user added files
	return statusRemoved, nil
}

// desktopEntry is the mcpServers entry that starts this binary.
func (a *app) desktopEntry() ([]byte, error) {
	exe, err := a.executable()
	if err != nil {
		return nil, fmt.Errorf("locate the %s binary: %w", brand.Name, err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	if abs, err := filepath.Abs(exe); err == nil {
		exe = abs
	}
	return json.Marshal(map[string]any{"command": exe, "args": []string{"mcp"}})
}

// editDesktopConfig sets (entry != nil) or deletes mcpServers.dropsite in the
// Claude Desktop config. Every other key is kept as is; a file that is not
// valid JSON is never touched.
func editDesktopConfig(path string, entry []byte) (string, error) {
	data, err := os.ReadFile(path) //nolint:gosec // the user's own Claude Desktop config
	mode := os.FileMode(0o600)
	exists := err == nil
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if entry == nil {
			return statusAbsent, nil
		}
		data = []byte("{}")
	case err != nil:
		return "", fmt.Errorf("read %s: %w", path, err)
	default:
		if fi, err := os.Stat(path); err == nil {
			mode = fi.Mode().Perm()
		}
	}
	top := map[string]json.RawMessage{}
	if len(bytes.TrimSpace(data)) > 0 {
		if err := json.Unmarshal(data, &top); err != nil || top == nil {
			return "", fmt.Errorf("%s is not valid JSON, so it was left untouched; fix or delete it and run '%s setup' again", path, brand.Name)
		}
	}
	servers := map[string]json.RawMessage{}
	if raw, ok := top[mcpServersKey]; ok && string(raw) != "null" {
		if err := json.Unmarshal(raw, &servers); err != nil || servers == nil {
			return "", fmt.Errorf("%q in %s is not an object, so the file was left untouched", mcpServersKey, path)
		}
	}
	old, had := servers[brand.Name]
	switch {
	case entry == nil && !had:
		return statusAbsent, nil
	case entry != nil && had && jsonEqual(old, entry):
		return statusUnchanged, nil
	}
	status := statusRemoved
	if entry == nil {
		delete(servers, brand.Name)
	} else {
		servers[brand.Name] = entry
		status = statusInstalled
		if had {
			status = statusUpdated
		}
	}
	rawServers, err := json.Marshal(servers)
	if err != nil {
		return "", fmt.Errorf("encode %s: %w", mcpServersKey, err)
	}
	top[mcpServersKey] = rawServers
	out, err := json.MarshalIndent(top, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode %s: %w", path, err)
	}
	if !exists {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return "", fmt.Errorf("create %s: %w", filepath.Dir(path), err)
		}
	}
	if err := writeFileAtomic(path, append(out, '\n'), mode); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	return status, nil
}

func jsonEqual(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	xa, _ := json.Marshal(x)
	ya, _ := json.Marshal(y)
	return bytes.Equal(xa, ya)
}

var statusText = map[string]string{
	statusInstalled: "connected",
	statusUpdated:   "updated",
	statusUnchanged: "already connected",
	statusRemoved:   "removed",
	statusAbsent:    "nothing to remove",
	statusNotFound:  "not found on this computer",
	statusSkipped:   "skipped",
	statusError:     "FAILED",
}

func (a *app) printSetup(out setupOutput, remove bool) {
	var restart []string
	for _, it := range out.Integrations {
		line := fmt.Sprintf("  %-15s %s", it.Name, statusText[it.Status])
		if it.Path != "" && it.Status != statusNotFound && it.Status != statusAbsent {
			line += " (" + it.Path + ")"
		}
		fmt.Fprintln(a.stdout, line)
		if it.Message != "" {
			fmt.Fprintf(a.stderr, "    %s\n", it.Message)
		}
		if it.Restart {
			restart = append(restart, it.Name)
		}
	}
	if len(restart) > 0 {
		fmt.Fprintf(a.stdout, "\nRestart to apply: %s (Claude Code and Codex: start a new session).\n", joinNames(restart))
	}
	if !remove && len(restart) > 0 {
		fmt.Fprintln(a.stdout, `Then ask the assistant: "publish this site".`)
	}
	if !remove && !out.LoggedIn {
		fmt.Fprintf(a.stderr, "\nNote: %s is not signed in yet. Press Connect on the Connect AI page and run the command shown there.\n", brand.Name)
	}
}

func joinNames(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	}
	s := names[0]
	for _, n := range names[1 : len(names)-1] {
		s += ", " + n
	}
	return s + " and " + names[len(names)-1]
}
