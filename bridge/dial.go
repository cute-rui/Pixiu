package bridge

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"syscall"
	"time"

	"github.com/metacubex/mihomo/component/dialer"
)

func parseAddr(s string) (netip.Addr, error) {
	return netip.ParseAddr(s)
}

// installDialHook makes Mihomo refuse any protocol-server dial that is not
// inside the overlay. Those dials would otherwise leave the host, or loop
// back into Mihomo once the default route points at the tunnel.
func installDialHook(extra []netip.Prefix) {
	dialer.DefaultSocketHook = func(network, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			host = address
		}
		ip, err := netip.ParseAddr(host)
		if err != nil {
			return fmt.Errorf("refusing non-overlay dial to %s", address)
		}
		if !AllowDial(ip, extra) {
			return fmt.Errorf("refusing non-overlay dial to %s", ip)
		}
		return nil
	}
}

func clearDialHook() {
	dialer.DefaultSocketHook = nil
}

// ProtectFunc excludes a socket from the system TUN. Android passes
// VpnService.protect. A nil func dials normally.
type ProtectFunc func(fd int) bool

func fetchSubscription(ctx context.Context, rawURL string, protect ProtectFunc, extra []netip.Prefix) ([]byte, error) {
	dialer := &net.Dialer{
		Timeout: 20 * time.Second,
		Control: func(network, address string, c syscall.RawConn) error {
			if protect == nil {
				return nil
			}
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return nil
			}
			if ip, err := netip.ParseAddr(host); err == nil && Overlay(ip, extra) {
				return nil
			}
			var fd int
			if err := c.Control(func(s uintptr) { fd = int(s) }); err != nil {
				return err
			}
			if !protect(fd) {
				return errors.New("failed to exclude subscription socket from the tunnel")
			}
			return nil
		},
	}
	client := &http.Client{Transport: &http.Transport{DialContext: dialer.DialContext}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "pixiu")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("subscription request failed: %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	return body, nil
}
