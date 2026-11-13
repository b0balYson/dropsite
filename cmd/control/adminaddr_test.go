package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/b0balYson/dropsite/internal/config"
)

func TestClassifyAdminHost(t *testing.T) {
	cases := map[string]adminExposure{
		"127.0.0.1":    exposureLoopback,
		"::1":          exposureLoopback,
		"[::1]":        exposureLoopback,
		"localhost":    exposureLoopback,
		"10.0.0.5":     exposurePrivate,
		"172.20.1.1":   exposurePrivate,
		"192.168.1.10": exposurePrivate,
		"100.64.1.1":   exposurePrivate,
		"fd00::10":     exposurePrivate,
		"203.0.113.10": exposurePublic,
		"0.0.0.0":      exposurePublic,
		"::":           exposurePublic,
		"":             exposurePublic,
		"admin.host":   exposurePublic,
		"100.128.0.1":  exposurePublic,
	}
	for host, want := range cases {
		if got := classifyAdminHost(host); got != want {
			t.Errorf("classifyAdminHost(%q) = %d, want %d", host, got, want)
		}
	}
}

func TestCheckAdminExposure(t *testing.T) {
	cases := []struct {
		name    string
		addr    string
		bindIP  string
		allow   bool
		wantErr bool
		warn    string // substring expected in the log; empty means no warning
	}{
		{name: "public bind refused", addr: ":8082", bindIP: "203.0.113.10", wantErr: true},
		{name: "all interfaces outside docker refused", addr: "0.0.0.0:8082", wantErr: true},
		{name: "empty host outside docker refused", addr: ":8082", wantErr: true},
		{name: "public bind allowed", addr: ":8082", bindIP: "203.0.113.10", allow: true, warn: "ALLOW_PUBLIC_ADMIN=1"},
		{name: "private network warns", addr: ":8082", bindIP: "10.0.0.5", warn: "plain HTTP"},
		{name: "tailscale warns", addr: ":8082", bindIP: "100.64.1.1", warn: "plain HTTP"},
		{name: "compose default is quiet", addr: ":8082", bindIP: "127.0.0.1"},
		{name: "binary default is quiet", addr: config.DefaultAdminAddr},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{AdminAddr: tc.addr, AdminBindIP: tc.bindIP, AllowPublicAdmin: tc.allow, UIURL: "http://127.0.0.1:8082"}
			var buf bytes.Buffer
			err := checkAdminExposure(cfg, slog.New(slog.NewTextHandler(&buf, nil)))
			if tc.wantErr {
				if !errors.Is(err, errPublicAdmin) {
					t.Fatalf("want public admin error, got %v", err)
				}
				for _, part := range []string{"SSH tunnel", "ALLOW_PUBLIC_ADMIN=1"} {
					if !strings.Contains(err.Error(), part) {
						t.Errorf("error %q does not mention %q", err, part)
					}
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if tc.warn == "" && buf.Len() > 0 {
				t.Fatalf("unexpected log: %s", buf.String())
			}
			if tc.warn != "" && (!strings.Contains(buf.String(), "level=WARN") || !strings.Contains(buf.String(), tc.warn)) {
				t.Fatalf("want warning with %q, got %q", tc.warn, buf.String())
			}
		})
	}
}

// The error names the variable that carries the public address.
func TestCheckAdminExposureNamesVariable(t *testing.T) {
	cfg := &config.Config{AdminAddr: ":8082", AdminBindIP: "203.0.113.10"}
	err := checkAdminExposure(cfg, quietLog())
	if err == nil || !strings.Contains(err.Error(), "ADMIN_BIND_IP=203.0.113.10") {
		t.Fatalf("got %v", err)
	}
}

// A leftover ADMIN_PASSWORD after the first administrator exists is reported
// once and does not stop the start.
func TestLeftoverAdminPasswordWarns(t *testing.T) {
	dir := t.TempDir()
	cfg := testConfig(t, dir)
	a, err := build(context.Background(), cfg, quietLog())
	if err != nil {
		t.Fatal(err)
	}
	_ = a.db.Close()

	var buf bytes.Buffer
	a, err = build(context.Background(), cfg, slog.New(slog.NewTextHandler(&buf, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer a.db.Close()
	if n := strings.Count(buf.String(), "ADMIN_PASSWORD is set although users already exist"); n != 1 {
		t.Fatalf("want one warning, got %d: %s", n, buf.String())
	}

	cfg.AdminPassword = ""
	buf.Reset()
	b, err := build(context.Background(), cfg, slog.New(slog.NewTextHandler(&buf, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer b.db.Close()
	if strings.Contains(buf.String(), "ADMIN_PASSWORD") {
		t.Fatalf("no warning expected without the variable: %s", buf.String())
	}
}
