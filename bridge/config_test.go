package bridge

import (
	"encoding/base64"
	"net/netip"
	"strings"
	"testing"

	"github.com/metacubex/mihomo/config"
	"gopkg.in/yaml.v3"
)

func TestBuildRuntimeUsesFirstSelectGroup(t *testing.T) {
	raw := []byte(`
proxies:
  - {name: a, type: ss, server: 100.64.0.2, port: 1}
  - {name: b, type: vmess, server: 100.64.0.3, port: 2}
proxy-groups:
  - {name: Other, type: url-test, proxies: [a]}
  - {name: Exit, type: select, proxies: [b, a]}
  - {name: Later, type: select, proxies: [a]}
rules:
  - MATCH,Exit
`)
	rt, err := ParseSubscription(raw)
	if err != nil {
		t.Fatal(err)
	}
	if rt.Group != "Exit" {
		t.Fatalf("group = %s", rt.Group)
	}
	if len(rt.Nodes) != 2 || rt.Nodes[0].Name != "b" || rt.Nodes[1].Name != "a" {
		t.Fatalf("nodes = %+v", rt.Nodes)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(rt.YAML, &doc); err != nil {
		t.Fatal(err)
	}
	tun := doc["tun"].(map[string]any)
	if tun["auto-route"] != false || tun["stack"] != "gvisor" {
		t.Fatalf("tun = %#v", tun)
	}
}

func TestBuildRuntimePixiuFallback(t *testing.T) {
	raw := []byte(`
proxies:
  - {name: one, type: trojan, server: 100.64.0.8, port: 443}
  - {name: two, type: vless, server: peer.mesh, port: 443}
`)
	rt, err := ParseSubscription(raw)
	if err != nil {
		t.Fatal(err)
	}
	if rt.Group != "Pixiu" {
		t.Fatalf("group = %s", rt.Group)
	}
	if len(rt.Nodes) != 2 {
		t.Fatalf("nodes = %+v", rt.Nodes)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(rt.YAML, &doc); err != nil {
		t.Fatal(err)
	}
	rules, _ := doc["rules"].([]any)
	if len(rules) != 1 || rules[0] != "MATCH,Pixiu" {
		t.Fatalf("rules = %#v", doc["rules"])
	}
}

func TestBuildRuntimeBase64(t *testing.T) {
	yamlDoc := []byte("proxies:\n  - {name: a, type: socks5, server: 100.64.0.9, port: 1080}\n")
	wrapped := []byte(base64.StdEncoding.EncodeToString(yamlDoc))
	rt, err := ParseSubscription(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	if rt.Group != "Pixiu" || rt.Nodes[0].Type != "socks5" {
		t.Fatalf("runtime = %+v", rt)
	}
}

func TestBuildRuntimeInvalid(t *testing.T) {
	if _, err := ParseSubscription([]byte("::: not yaml")); err == nil {
		t.Fatal("expected invalid subscription")
	}
}

func TestBuildRuntimeNoProxies(t *testing.T) {
	raw := []byte("proxies: []\nrules:\n  - MATCH,DIRECT\n")
	if _, err := ParseSubscription(raw); err == nil || !strings.Contains(err.Error(), "no proxies") {
		t.Fatalf("err = %v", err)
	}
}

func TestParsePinnedMihomo(t *testing.T) {
	raw := []byte(`
proxies:
  - {name: a, type: ss, server: 100.64.0.2, port: 1, cipher: aes-128-gcm, password: secret}
`)
	rt, err := ParseSubscription(raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := config.Parse(rt.YAML); err != nil {
		t.Fatalf("mihomo %s rejected runtime config: %v", MihomoVersion, err)
	}
}

func TestDialFailClosed(t *testing.T) {
	overlay := []string{"100.64.0.8", "fd12::1", "100.127.255.255"}
	closed := []string{"1.1.1.1", "8.8.8.8", "2001:db8::1", "192.168.1.1"}
	for _, s := range overlay {
		addr := netip.MustParseAddr(s)
		if !AllowDial(addr, nil) {
			t.Fatalf("overlay %s was refused", s)
		}
		if err := ValidateServer(s, nil); err != nil {
			t.Fatalf("validate %s: %v", s, err)
		}
	}
	for _, s := range closed {
		addr := netip.MustParseAddr(s)
		if AllowDial(addr, nil) {
			t.Fatalf("non-overlay %s was allowed", s)
		}
		if err := ValidateServer(s, nil); err == nil {
			t.Fatalf("validate %s: expected refusal", s)
		}
	}
	if err := ValidateServer("peer.example", nil); err != nil {
		t.Fatalf("hostname should be checked at dial time: %v", err)
	}
	if !AllowDial(netip.MustParseAddr("127.0.0.1"), nil) {
		t.Fatal("loopback should stay reachable")
	}
}
