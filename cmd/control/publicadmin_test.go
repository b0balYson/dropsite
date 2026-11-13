package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/b0balYson/dropsite/internal/config"
)

// The public admin listener is internal to the compose network: an
// all-interfaces PUBLIC_ADMIN_ADDR is not an ADMIN_BIND_IP violation.
func TestPublicAdminAddrIsNotAnExposure(t *testing.T) {
	cfg := &config.Config{AdminAddr: ":8082", AdminBindIP: "127.0.0.1", PublicAdminAddr: ":8084", UIURL: "http://127.0.0.1:8082"}
	if err := checkAdminExposure(cfg, quietLog()); err != nil {
		t.Fatal(err)
	}
	def, err := config.Load(func(k string) (string, bool) {
		return map[string]string{"BASE_DOMAIN": "pp.test"}[k], k == "BASE_DOMAIN"
	})
	if err != nil {
		t.Fatal(err)
	}
	if def.PublicAdminAddr != config.DefaultPublicAdminAddr || !strings.HasPrefix(def.PublicAdminAddr, "127.0.0.1:") {
		t.Fatalf("default PUBLIC_ADMIN_ADDR %q must stay on loopback", def.PublicAdminAddr)
	}
}

// The wired public listener answers 404 until public access is turned on,
// then serves the interface for the public admin host only.
func TestPublicAdminListenerWired(t *testing.T) {
	ctx := context.Background()
	a, err := build(ctx, testConfig(t, t.TempDir()), quietLog())
	if err != nil {
		t.Fatal(err)
	}
	defer a.db.Close()
	srv := httptest.NewServer(a.publicAdmin)
	defer srv.Close()
	get := func(host string) int {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/login", nil)
		req.Host = host
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if got := get("admin.pp.test"); got != http.StatusNotFound {
		t.Fatalf("off: %d", got)
	}
	if err := a.svc.SetPublicAdminCLI(ctx, true); err != nil {
		t.Fatal(err)
	}
	if got := get("admin.pp.test"); got != http.StatusOK {
		t.Fatalf("on: %d", got)
	}
	if got := get("pp.test"); got != http.StatusNotFound {
		t.Fatalf("other host: %d", got)
	}
	// The loopback listener is unchanged.
	loop := httptest.NewServer(a.admin)
	defer loop.Close()
	resp, err := http.Get(loop.URL + "/login")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("loopback: %d", resp.StatusCode)
	}
}

func TestPublicAdminCmd(t *testing.T) {
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
	for _, args := range [][]string{{"maybe"}, {"on", "off"}} {
		if err := publicAdminCmd(ctx, args, io.Discard, io.Discard); !errors.Is(err, errUsage) {
			t.Fatalf("%v: %v", args, err)
		}
	}
	marker := filepath.Join(cfg.SitesDir, "tls-hosts", "admin.pp.test")
	for _, tc := range []struct {
		args   []string
		out    string
		marker bool
	}{
		{nil, "off\n", false},
		{[]string{"on"}, "on\n", true},
		{[]string{"status"}, "on\n", true},
		{[]string{"off"}, "off\n", false},
	} {
		var out, notes bytes.Buffer
		if err := publicAdminCmd(ctx, tc.args, &out, &notes); err != nil {
			t.Fatal(err)
		}
		if out.String() != tc.out {
			t.Fatalf("%v: stdout %q", tc.args, out.String())
		}
		if _, err := os.Stat(marker); (err == nil) != tc.marker {
			t.Fatalf("%v: marker %v", tc.args, err)
		}
		if len(tc.args) == 1 && tc.args[0] == "on" && !strings.Contains(notes.String(), "https://admin.pp.test") {
			t.Fatalf("notes %q", notes.String())
		}
	}
}
