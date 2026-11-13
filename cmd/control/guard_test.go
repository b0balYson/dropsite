package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefenderAndBansCmd(t *testing.T) {
	ctx := context.Background()
	cfg := testConfig(t, t.TempDir())
	a, err := build(ctx, cfg, quietLog())
	if err != nil {
		t.Fatal(err)
	}
	_ = a.db.Close()
	t.Setenv("BASE_DOMAIN", "pp.test")
	t.Setenv("ADMIN_EMAIL", "admin@pp.test")
	t.Setenv("SITES_DIR", cfg.SitesDir)
	t.Setenv("STATE_DIR", cfg.StateDir)
	run := func(f func(context.Context, []string, io.Writer, io.Writer) error, args ...string) (string, error) {
		var out bytes.Buffer
		err := f(ctx, args, &out, io.Discard)
		return out.String(), err
	}
	off := filepath.Join(cfg.SitesDir, "flags", "defender-off")
	for _, tc := range []struct {
		args   []string
		out    string
		marker bool
	}{
		{nil, "on\nnormal\n", false},
		{[]string{"off"}, "off\nnormal\n", true},
		{[]string{"strict", "on"}, "off\nstrict\n", true},
		{[]string{"on"}, "on\nstrict\n", false},
		{[]string{"status"}, "on\nstrict\n", false},
	} {
		out, err := run(defenderCmd, tc.args...)
		if err != nil || out != tc.out {
			t.Fatalf("%v: %q %v", tc.args, out, err)
		}
		if _, err := os.Stat(off); (err == nil) != tc.marker {
			t.Fatalf("%v: marker %v", tc.args, err)
		}
	}
	if _, err := run(defenderCmd, "maybe"); !errors.Is(err, errUsage) {
		t.Fatalf("bad args: %v", err)
	}

	if out, err := run(bansCmd, "add", "203.0.113.7", "30"); err != nil || !strings.HasPrefix(out, "203.0.113.7\t1\t") {
		t.Fatalf("add: %q %v", out, err)
	}
	if _, err := os.Stat(filepath.Join(cfg.SitesDir, "bans", "203.0.113.7")); err != nil {
		t.Fatalf("ban marker: %v", err)
	}
	if out, err := run(bansCmd, "list"); err != nil || !strings.Contains(out, "203.0.113.7\t1\tmanual\t") {
		t.Fatalf("list: %q %v", out, err)
	}
	if _, err := run(bansCmd, "clear", "203.0.113.7"); err != nil {
		t.Fatal(err)
	}
	if out, _ := run(bansCmd); out != "" {
		t.Fatalf("after clear: %q", out)
	}
	if _, err := run(bansCmd, "add", "not-an-ip"); err == nil {
		t.Fatal("bad address accepted")
	}
	if _, err := run(bansCmd, "drop"); !errors.Is(err, errUsage) {
		t.Fatalf("bad action: %v", err)
	}
}
