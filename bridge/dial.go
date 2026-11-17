package bridge

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"sync"
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

func fetchSubscription(ctx context.Context, rawURL string, protect ProtectFunc, extra []netip.Prefix, dnsServers []string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

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
	var (
		pinMu sync.Mutex
		pins  = map[string]netip.Addr{}
	)
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			pinMu.Lock()
			pinned, ok := pins[host]
			pinMu.Unlock()
			if ok {
				address = net.JoinHostPort(pinned.String(), port)
			}
			return dialer.DialContext(ctx, network, address)
		},
	}
	client := &http.Client{
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	current := rawURL
	for hop := 0; ; hop++ {
		parsed, err := url.Parse(current)
		if err != nil {
			return nil, fmt.Errorf("subscription request failed: %w", err)
		}
		if hop > 0 {
			if err := subscriptionRedirect(parsed, hop); err != nil {
				return nil, err
			}
		}
		if len(dnsServers) > 0 && parsed.Hostname() != "" {
			if ip, err := netip.ParseAddr(parsed.Hostname()); err == nil {
				if hop > 0 && rejectedProfileIP(ip) {
					return nil, fmt.Errorf("subscription request failed: redirect target %s was rejected", parsed.Hostname())
				}
			} else {
				addrs, err := resolveHost(ctx, parsed.Hostname(), dnsServers, protect)
				if err != nil {
					return nil, fmt.Errorf("subscription request failed: %w", err)
				}
				if hop > 0 && addressesRejected(addrs) {
					return nil, fmt.Errorf("subscription request failed: redirect target %s was rejected", parsed.Hostname())
				}
				if len(addrs) == 0 {
					return nil, fmt.Errorf("subscription request failed: %s did not resolve", parsed.Hostname())
				}
				pinMu.Lock()
				pins[parsed.Hostname()] = addrs[0]
				pinMu.Unlock()
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", "pixiu")
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			location := resp.Header.Get("Location")
			resp.Body.Close()
			if hop >= 3 {
				return nil, errors.New("subscription request failed: too many redirects")
			}
			if location == "" {
				return nil, errors.New("subscription request failed: redirect has no location")
			}
			next, err := parsed.Parse(location)
			if err != nil {
				return nil, fmt.Errorf("subscription request failed: %w", err)
			}
			current = next.String()
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			resp.Body.Close()
			return nil, fmt.Errorf("subscription request failed: %s", resp.Status)
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, (8<<20)+1))
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		if len(body) > 8<<20 {
			return nil, errors.New("subscription request failed: profile is larger than 8 MiB")
		}
		return body, nil
	}
}

func subscriptionRedirect(next *url.URL, hops int) error {
	if hops > 3 {
		return errors.New("subscription request failed: too many redirects")
	}
	if next.Scheme != "https" {
		return errors.New("subscription request failed: a redirect left HTTPS")
	}
	if next.User != nil {
		return errors.New("subscription request failed: the profile URL contains user info")
	}
	host := next.Hostname()
	if host == "" {
		return errors.New("subscription request failed: the profile URL has no host")
	}
	if ip, err := netip.ParseAddr(host); err == nil && rejectedProfileIP(ip) {
		return fmt.Errorf("subscription request failed: redirect target %s was rejected", host)
	}
	return nil
}
