package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeRelease builds a download directory whose "binary" is a shell script
// recording its arguments, so install.sh can be run end to end locally.
func fakeRelease(t *testing.T, loginExit int) (base, log string) {
	t.Helper()
	base = t.TempDir()
	log = filepath.Join(t.TempDir(), "calls.log")
	arch := map[string]string{"amd64": "amd64", "arm64": "arm64"}[runtime.GOARCH]
	asset := "dropsite-" + runtime.GOOS + "-" + arch
	script := "#!/bin/sh\necho \"$@\" >> '" + log + "'\n" +
		"case \"$1\" in version) echo 'dropsite test';; login) exit " + itoa(loginExit) + ";; esac\n"
	if err := os.WriteFile(filepath.Join(base, asset), []byte(script), 0o755); err != nil { //nolint:gosec // test fixture must be executable
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(script))
	sums := hex.EncodeToString(sum[:]) + "  " + asset + "\n"
	if err := os.WriteFile(filepath.Join(base, "SHA256SUMS"), []byte(sums), 0o644); err != nil { //nolint:gosec // test fixture
		t.Fatal(err)
	}
	return base, log
}

func itoa(n int) string { return string(rune('0' + n)) }

func runInstall(t *testing.T, base string, env []string, args ...string) (string, error) {
	t.Helper()
	installDir := t.TempDir()
	cmd := exec.CommandContext(context.Background(), "sh", append([]string{"../../scripts/install.sh"}, args...)...)
	cmd.Env = append([]string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir(),
		"DROPSITE_DOWNLOAD_BASE=file://" + base, "DROPSITE_INSTALL_DIR=" + installDir}, env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestInstallScriptWithCode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("install.sh is for macOS and Linux")
	}
	base, log := fakeRelease(t, 0)
	out, err := runInstall(t, base, nil, "--server", "https://api.example.com", "--code", "ABCD-EFGH-JKMNP")
	if err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	calls, _ := os.ReadFile(log)
	want := "version\nlogin --server https://api.example.com --code ABCD-EFGH-JKMNP\nsetup\n"
	if !strings.Contains(string(calls), want) {
		t.Fatalf("calls:\n%s\noutput:\n%s", calls, out)
	}

	// The same through environment variables.
	base, log = fakeRelease(t, 0)
	if out, err := runInstall(t, base, []string{"DROPSITE_SERVER=https://api.example.com", "DROPSITE_CODE=X"}); err != nil {
		t.Fatalf("env install: %v\n%s", err, out)
	}
	if calls, _ := os.ReadFile(log); !strings.Contains(string(calls), "login --server https://api.example.com --code X") {
		t.Fatalf("env calls: %s", calls)
	}

	// Without a code it only installs.
	base, log = fakeRelease(t, 0)
	out, err = runInstall(t, base, nil)
	if calls, _ := os.ReadFile(log); err != nil || strings.Contains(string(calls), "login") || !strings.Contains(out, "Connect AI") {
		t.Fatalf("plain install: %v %s\n%s", err, calls, out)
	}
}

func TestInstallScriptFailures(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("install.sh is for macOS and Linux")
	}
	base, log := fakeRelease(t, 1)
	out, err := runInstall(t, base, nil, "--server", "https://api.example.com", "--code", "BAD")
	if err == nil || !strings.Contains(out, "sign-in failed") {
		t.Fatalf("failed login: %v\n%s", err, out)
	}
	if calls, _ := os.ReadFile(log); strings.Contains(string(calls), "setup") {
		t.Fatal("setup ran after a failed login")
	}
	for _, args := range [][]string{{"--code", "X"}, {"--bogus"}, {"--server"}} {
		if out, err := runInstall(t, base, nil, args...); err == nil {
			t.Errorf("%v accepted:\n%s", args, out)
		}
	}
}
