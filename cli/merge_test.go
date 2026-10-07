package main

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestMergeRuntime(t *testing.T) {
	in := []byte(`
proxies:
  - name: overlay
    type: ss
    server: 100.64.0.8
    port: 443
  - name: public
    type: ss
    server: 1.1.1.1
    port: 443
  - name: named
    type: ss
    server: exit.example
    port: 443
rules:
  - GEOIP,CN,DIRECT
  - MATCH,Proxy
tun:
  enable: true
`)
	info := NetBirdInfo{
		Interface: "nb0",
		Endpoints: []string{
			"8.8.8.8:51820",
			"100.64.1.1:51820",
			"fd12::1",
			"rels://1.2.3.4:443",
			"[2001:db8::1]:3478",
			"relay.example:443",
		},
	}
	out, err := MergeRuntime(in, info)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	rules := stringList(t, doc["rules"])
	joined := strings.Join(rules, "\n")
	for _, want := range []string{
		"IP-CIDR,8.8.8.8/32,DIRECT,no-resolve",
		"IP-CIDR,1.2.3.4/32,DIRECT,no-resolve",
		"IP-CIDR,2001:db8::1/128,DIRECT,no-resolve",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %s in\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "100.64") || strings.Contains(joined, "fd12") {
		t.Fatalf("overlay address was excluded:\n%s", joined)
	}
	match := -1
	direct := -1
	for i, rule := range rules {
		if strings.HasPrefix(rule, "MATCH") {
			match = i
		}
		if strings.HasPrefix(rule, "IP-CIDR,8.8.8.8/") {
			direct = i
		}
	}
	if direct < 0 || match < 0 || direct > match {
		t.Fatalf("DIRECT rule is not above MATCH: %v", rules)
	}

	tun := doc["tun"].(map[string]any)
	excluded := strings.Join(stringList(t, tun["route-exclude-address"]), "\n")
	if !strings.Contains(excluded, "8.8.8.8/32") || !strings.Contains(excluded, "2001:db8::1/128") {
		t.Fatalf("tun excludes: %s", excluded)
	}
	if strings.Contains(excluded, "100.64") || strings.Contains(excluded, "fd12") {
		t.Fatalf("tun excluded overlay: %s", excluded)
	}

	proxies := doc["proxies"].([]any)
	overlay := proxies[0].(map[string]any)
	if overlay["interface-name"] != "nb0" {
		t.Fatalf("overlay interface-name = %v", overlay["interface-name"])
	}
	public := proxies[1].(map[string]any)
	if _, ok := public["interface-name"]; ok {
		t.Fatalf("public proxy got interface-name: %v", public["interface-name"])
	}
	named := proxies[2].(map[string]any)
	if _, ok := named["interface-name"]; ok {
		t.Fatalf("hostname proxy got interface-name: %v", named["interface-name"])
	}
}

func TestMergeRuntimeDefaults(t *testing.T) {
	out, err := MergeRuntime([]byte("rules:\n  - DOMAIN,example,DIRECT\nproxies:\n  - {name: a, server: fd12::1}\n"), NetBirdInfo{
		Endpoints: []string{"9.9.9.9:1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	rules := stringList(t, doc["rules"])
	if rules[len(rules)-1] != "IP-CIDR,9.9.9.9/32,DIRECT,no-resolve" {
		t.Fatalf("rule was not appended: %v", rules)
	}
	tun := doc["tun"].(map[string]any)
	if _, ok := tun["enable"]; ok {
		t.Fatalf("created tun forced enable: %v", tun)
	}
	proxy := doc["proxies"].([]any)[0].(map[string]any)
	if proxy["interface-name"] != "wt0" {
		t.Fatalf("default iface = %v", proxy["interface-name"])
	}
}

func stringList(t *testing.T, v any) []string {
	t.Helper()
	raw, ok := v.([]any)
	if !ok {
		t.Fatalf("not a list: %#v", v)
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		s, ok := item.(string)
		if !ok {
			t.Fatalf("not a string: %#v", item)
		}
		out = append(out, s)
	}
	return out
}
