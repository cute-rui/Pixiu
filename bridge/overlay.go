package bridge

import (
	"net"
	"net/netip"
	"strings"
)

// Default overlay ranges. NetBird mesh addresses sit in 100.64.0.0/10.
// IPv6 ULA is fc00::/7.
var defaultOverlay = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("fc00::/7"),
}

// DefaultOverlay returns the prefixes that stay on the NetBird tunnel.
func DefaultOverlay() []netip.Prefix {
	out := make([]netip.Prefix, len(defaultOverlay))
	copy(out, defaultOverlay)
	return out
}

// Overlay reports whether addr is inside the overlay prefixes.
func Overlay(addr netip.Addr, extra []netip.Prefix) bool {
	if !addr.IsValid() {
		return false
	}
	addr = addr.Unmap()
	for _, p := range defaultOverlay {
		if p.Contains(addr) {
			return true
		}
	}
	for _, p := range extra {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// AllowDial is the fail-closed check for a Mihomo protocol-server dial.
// Overlay addresses are allowed. Everything else, including public IPs, is not.
// Loopback is allowed so the in-process controller can still talk to itself.
func AllowDial(addr netip.Addr, extra []netip.Prefix) bool {
	if !addr.IsValid() {
		return false
	}
	addr = addr.Unmap()
	if addr.IsLoopback() {
		return true
	}
	return Overlay(addr, extra)
}

// ValidateServer rejects a proxy server that is a literal non-overlay IP.
// Hostnames are accepted here; the dial itself fails closed if they do not
// resolve into the overlay.
func ValidateServer(server string, extra []netip.Prefix) error {
	server = strings.TrimSpace(server)
	if server == "" {
		return errNotOverlay
	}
	if ip, err := netip.ParseAddr(server); err == nil {
		if !Overlay(ip, extra) {
			return errNotOverlay
		}
		return nil
	}
	if ip := net.ParseIP(server); ip != nil {
		addr, ok := netip.AddrFromSlice(ip)
		if !ok || !Overlay(addr, extra) {
			return errNotOverlay
		}
	}
	return nil
}
