package main

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"strings"

	"github.com/b0balYson/dropsite/internal/config"
)

// adminExposure says how far the admin listener can be reached from.
type adminExposure int

const (
	exposureLoopback adminExposure = iota // this machine only
	exposurePrivate                       // RFC 1918, ULA, CGNAT (Tailscale) or link-local
	exposurePublic                        // anything else, including all interfaces
)

// cgnat is 100.64.0.0/10 (RFC 6598), used by Tailscale and other overlays.
var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// classifyAdminHost classifies the host part of the admin address. An empty
// host, an unspecified address or a name that is not an IP counts as public:
// the listener may then be reachable on every interface.
func classifyAdminHost(host string) adminExposure {
	host = strings.Trim(strings.TrimSpace(host), "[]")
	if strings.EqualFold(host, "localhost") {
		return exposureLoopback
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return exposurePublic
	}
	addr = addr.Unmap()
	switch {
	case addr.IsLoopback():
		return exposureLoopback
	case addr.IsUnspecified():
		return exposurePublic
	case addr.IsPrivate(), cgnat.Contains(addr), addr.IsLinkLocalUnicast():
		return exposurePrivate
	}
	return exposurePublic
}

// adminHost returns where the admin UI is actually reachable and the variable
// that says so. In compose control must listen on :8082 inside the container,
// so the published ADMIN_BIND_IP is what matters; outside Docker it is the
// host of ADMIN_ADDR.
func adminHost(cfg *config.Config) (host, source string) {
	if cfg.AdminBindIP != "" {
		return cfg.AdminBindIP, "ADMIN_BIND_IP"
	}
	host, _, err := net.SplitHostPort(cfg.AdminAddr)
	if err != nil {
		return cfg.AdminAddr, "ADMIN_ADDR"
	}
	return host, "ADMIN_ADDR"
}

// errPublicAdmin is returned when the admin UI would start on a public address.
var errPublicAdmin = errors.New("the admin UI would be reachable from the internet over plain HTTP")

// checkAdminExposure refuses a public admin address unless ALLOW_PUBLIC_ADMIN
// is set and warns when the plain HTTP interface leaves this machine.
func checkAdminExposure(cfg *config.Config, log *slog.Logger) error {
	host, source := adminHost(cfg)
	shown := host
	if shown == "" {
		shown = "all interfaces"
	}
	exposure := classifyAdminHost(host)
	if exposure == exposurePublic && !cfg.AllowPublicAdmin {
		return fmt.Errorf("%w: %s=%s. Session cookies and passwords would travel unencrypted to anyone who can reach it. "+
			"Keep %s at 127.0.0.1 and open the UI through an SSH tunnel, or use an address in a private network "+
			"(VPN, Tailscale, WireGuard). To accept the risk, set ALLOW_PUBLIC_ADMIN=1",
			errPublicAdmin, source, shown, source)
	}
	if exposure == exposurePublic {
		log.Warn("the admin UI is published on a public address because ALLOW_PUBLIC_ADMIN=1; "+
			"it uses plain HTTP, put TLS in front of it or restrict access with a firewall",
			"variable", source, "address", shown)
		return nil
	}
	if exposure == exposurePrivate && strings.HasPrefix(strings.ToLower(cfg.UIURL), "http://") {
		log.Warn("the admin UI is served over plain HTTP outside this machine; keep the network private",
			"variable", source, "address", shown, "ui_url", cfg.UIURL)
	}
	return nil
}
