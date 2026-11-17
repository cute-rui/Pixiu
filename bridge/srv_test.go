package bridge

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"net/netip"
	"net/url"
	"testing"
	"time"
)

func TestManagementHostStripsPortAndRejectsLiterals(t *testing.T) {
	host, kind := managementHost("https://api.example.com:33073")
	if host != "api.example.com" || kind != "" {
		t.Fatalf("host=%q kind=%q", host, kind)
	}
	host, kind = managementHost("https://api.netbird.io:443")
	if host != "api.netbird.io" || kind != "" {
		t.Fatalf("cloud host=%q kind=%q", host, kind)
	}
	host, kind = managementHost("https://API.Example.com/path")
	if host != "api.example.com" || kind != "" {
		t.Fatalf("case host=%q kind=%q", host, kind)
	}
	host, kind = managementHost("https://user:secret@api.example.com")
	if host != "api.example.com" || kind != "" {
		t.Fatalf("userinfo host=%q kind=%q", host, kind)
	}
	if _, kind = managementHost("https://100.64.0.1:443"); kind != kindMissing {
		t.Fatalf("ipv4 kind=%q", kind)
	}
	if _, kind = managementHost("https://[fc00::1]:33073"); kind != kindMissing {
		t.Fatalf("ipv6 kind=%q", kind)
	}
	if _, kind = managementHost(""); kind != kindTransient {
		t.Fatalf("empty kind=%q", kind)
	}
	if _, kind = managementHost("://"); kind != kindTransient {
		t.Fatalf("bad kind=%q", kind)
	}
	host, kind = managementHost("https://münchen.example")
	if host != "xn--mnchen-3ya.example" || kind != "" {
		t.Fatalf("idna host=%q kind=%q", host, kind)
	}
}

func TestSubscriptionURLOmits443(t *testing.T) {
	if got := subscriptionURL("profile.example.com", 443); got != "https://profile.example.com/pixiu.yaml" {
		t.Fatal(got)
	}
	if got := subscriptionURL("profile.example.com", 8443); got != "https://profile.example.com:8443/pixiu.yaml" {
		t.Fatal(got)
	}
}

func TestSRVOrderIsPriorityThenWeightThenNameThenPort(t *testing.T) {
	ordered := sortSRV([]srvRecord{
		{10, 50, 443, "a.example"},
		{0, 0, 443, "b.example"},
		{0, 5, 8443, "b.example"},
		{0, 5, 443, "b.example"},
		{0, 5, 443, "a.example"},
	})
	want := []srvRecord{
		{0, 5, 443, "a.example"},
		{0, 5, 443, "b.example"},
		{0, 5, 8443, "b.example"},
		{0, 0, 443, "b.example"},
		{10, 50, 443, "a.example"},
	}
	if len(ordered) != len(want) {
		t.Fatalf("len %d", len(ordered))
	}
	for i := range want {
		if ordered[i] != want[i] {
			t.Fatalf("index %d got %+v want %+v", i, ordered[i], want[i])
		}
	}
}

func TestRejectedProfileAddresses(t *testing.T) {
	reject := []string{
		"100.64.0.1", "100.127.255.1", "fc00::1", "fd12::1",
		"127.0.0.53", "0.0.0.0", "169.254.1.1", "224.0.0.1",
		"::1", "fe80::1", "ff02::1",
	}
	for _, raw := range reject {
		if !rejectedProfileIP(netip.MustParseAddr(raw)) {
			t.Fatalf("expected reject %s", raw)
		}
	}
	keep := []string{"100.63.0.1", "100.128.0.1", "8.8.8.8", "2001:db8::1"}
	for _, raw := range keep {
		if rejectedProfileIP(netip.MustParseAddr(raw)) {
			t.Fatalf("expected keep %s", raw)
		}
	}
	if !addressesRejected([]netip.Addr{netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("100.64.0.2")}) {
		t.Fatal("mixed addresses should be rejected")
	}
	if addressesRejected([]netip.Addr{netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("1.1.1.1")}) {
		t.Fatal("public addresses should be kept")
	}
}

func TestDiscoverSkipsBadTargetsAndKeepsOrder(t *testing.T) {
	result := discoverProfile(context.Background(), "https://api.example.com:33073", []string{"8.8.8.8:53"}, nil,
		func(context.Context, string, []string, ProtectFunc) ([]srvRecord, error) {
			return []srvRecord{
				{priority: 10, weight: 0, port: 443, target: "later.example"},
				{priority: 0, weight: 0, port: 0, target: "skip.example"},
				{priority: 0, weight: 1, port: 443, target: "192.0.2.10"},
				{priority: 0, weight: 5, port: 8443, target: "profile.example"},
				{priority: 0, weight: 5, port: 443, target: "profile.example"},
				{priority: 0, weight: 5, port: 443, target: "."},
			}, nil
		},
		func(_ context.Context, host string, _ []string, _ ProtectFunc) ([]netip.Addr, error) {
			switch host {
			case "profile.example":
				return []netip.Addr{netip.MustParseAddr("203.0.113.10")}, nil
			case "later.example":
				return []netip.Addr{netip.MustParseAddr("203.0.113.11")}, nil
			default:
				return nil, errors.New("unexpected host")
			}
		},
	)
	if result.Kind != kindURL || result.Host != "api.example.com" {
		t.Fatalf("result %+v", result)
	}
	want := []string{
		"https://profile.example/pixiu.yaml",
		"https://profile.example:8443/pixiu.yaml",
		"https://later.example/pixiu.yaml",
	}
	if len(result.URLs) != len(want) {
		t.Fatalf("urls %#v", result.URLs)
	}
	for i := range want {
		if result.URLs[i] != want[i] {
			t.Fatalf("url %d = %s", i, result.URLs[i])
		}
	}
}

func TestDiscoverRejectsOverlayAndLeavesNoURL(t *testing.T) {
	result := discoverProfile(context.Background(), "https://api.example.com", []string{"1.1.1.1"}, nil,
		func(context.Context, string, []string, ProtectFunc) ([]srvRecord, error) {
			return []srvRecord{{port: 443, target: "profile.example"}}, nil
		},
		func(context.Context, string, []string, ProtectFunc) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("203.0.113.5"), netip.MustParseAddr("100.64.0.8")}, nil
		},
	)
	if result.Kind != kindRejected || result.URL != "" || len(result.URLs) != 0 {
		t.Fatalf("result %+v", result)
	}
	if result.Message == "" {
		t.Fatal("missing reason")
	}
}

func TestDiscoverMissingRecordLeavesNoURL(t *testing.T) {
	result := discoverProfile(context.Background(), "https://api.example.com", []string{"1.1.1.1"}, nil,
		func(context.Context, string, []string, ProtectFunc) ([]srvRecord, error) {
			return nil, nil
		},
		func(context.Context, string, []string, ProtectFunc) ([]netip.Addr, error) {
			t.Fatal("should not resolve")
			return nil, nil
		},
	)
	if result.Kind != kindMissing || result.URL != "" {
		t.Fatalf("result %+v", result)
	}
}

func TestDiscoverIPManagementMatchesMissing(t *testing.T) {
	result := DiscoverProfile(context.Background(), "https://100.64.1.1", []string{"8.8.8.8"}, nil)
	if result.Kind != kindMissing || result.URL != "" {
		t.Fatalf("result %+v", result)
	}
}

func TestUsableDNSSkipsTunnelResolvers(t *testing.T) {
	got := usableDNS([]string{"127.0.0.53", "100.64.0.1", "8.8.8.8", "fe80::1", "1.1.1.1"})
	want := []string{"8.8.8.8:53", "1.1.1.1:53"}
	if len(got) != len(want) {
		t.Fatalf("got %#v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %#v", got)
		}
	}
}

func TestParsesSRVAnswerWithCompressionPointer(t *testing.T) {
	var packet []byte
	packet = append(packet, 0x00, 0x01, 0x81, 0x80, 0x00, 0x01, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00)
	if err := encodeName("_pixiu._tcp.api.example.com", &packet); err != nil {
		t.Fatal(err)
	}
	packet = append(packet, 0x00, 33, 0x00, 1)
	packet = append(packet, 0xc0, 0x0c)
	packet = append(packet, 0x00, 33, 0x00, 1)
	packet = append(packet, 0x00, 0x00, 0x01, 0x2c)
	rdata := []byte{0, 0, 0, 0, 0x01, 0xbb}
	if err := encodeName("profile.example.com", &rdata); err != nil {
		t.Fatal(err)
	}
	packet = append(packet, byte(len(rdata)>>8), byte(len(rdata)))
	packet = append(packet, rdata...)

	message, err := parseDNS(packet)
	if err != nil {
		t.Fatal(err)
	}
	if message.rcode != 0 || len(message.srv) != 1 {
		t.Fatalf("message %+v", message)
	}
	if message.srv[0].port != 443 || message.srv[0].priority != 0 || message.srv[0].target != "profile.example.com." {
		t.Fatalf("srv %+v", message.srv[0])
	}
}

func TestQuerySRVRoundTrip(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 1500)
		n, addr, err := pc.ReadFrom(buf)
		if err != nil {
			return
		}
		response := append([]byte{}, buf[:n]...)
		response[2], response[3] = 0x81, 0x80
		binary.BigEndian.PutUint16(response[6:8], 1)
		response = append(response, 0xc0, 0x0c)
		response = append(response, 0x00, 33, 0x00, 1, 0, 0, 0, 60)
		rdata := []byte{0, 0, 0, 10, 0x03, 0x5b}
		_ = encodeName("profile.example.com", &rdata)
		response = append(response, byte(len(rdata)>>8), byte(len(rdata)))
		response = append(response, rdata...)
		_, _ = pc.WriteTo(response, addr)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	records, err := querySRV(ctx, "api.example.com", []string{pc.LocalAddr().String()}, nil)
	<-done
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].port != 859 || records[0].weight != 10 {
		t.Fatalf("records %+v", records)
	}
	target, ip := canonicalTarget(records[0].target)
	if ip || target != "profile.example.com" {
		t.Fatalf("target %q ip %v", records[0].target, ip)
	}
}

func TestSubscriptionRedirectRejectsLeavingHTTPS(t *testing.T) {
	parsed, err := url.Parse("http://profile.example/pixiu.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err := subscriptionRedirect(parsed, 1); err == nil {
		t.Fatal("http redirect should fail")
	}
	parsed, err = url.Parse("https://127.0.0.1/pixiu.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err := subscriptionRedirect(parsed, 1); err == nil {
		t.Fatal("loopback redirect should fail")
	}
	parsed, err = url.Parse("https://profile.example/pixiu.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err := subscriptionRedirect(parsed, 1); err != nil {
		t.Fatal(err)
	}
}
