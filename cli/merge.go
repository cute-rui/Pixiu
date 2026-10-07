package main

import (
	"fmt"
	"net/netip"
	"strings"

	"gopkg.in/yaml.v3"
)

// NetBirdInfo is the slice of the local daemon used to keep overlay control
// traffic out of Mihomo.
type NetBirdInfo struct {
	Interface string
	Endpoints []string
}

// MergeRuntime inserts DIRECT no-resolve rules for peer public ICE and relay
// addresses above MATCH, excludes those addresses from the tun, and pins
// overlay proxy servers to the NetBird interface. Overlay prefixes themselves
// are not excluded.
func MergeRuntime(cfg []byte, info NetBirdInfo) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(cfg, &doc); err != nil {
		return nil, err
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("runtime config must be a YAML mapping")
	}
	root := doc.Content[0]
	iface := info.Interface
	if iface == "" {
		iface = "wt0"
	}
	addrs := publicAddrs(info.Endpoints)
	insertDirectRules(root, addrs)
	excludeTun(root, addrs)
	pinOverlayProxies(root, iface)
	return yaml.Marshal(&doc)
}

func publicAddrs(endpoints []string) []netip.Addr {
	seen := map[netip.Addr]bool{}
	var out []netip.Addr
	for _, endpoint := range endpoints {
		for _, addr := range ipsIn(endpoint) {
			if seen[addr] || isOverlay(addr) {
				continue
			}
			seen[addr] = true
			out = append(out, addr)
		}
	}
	return out
}

func ipsIn(text string) []netip.Addr {
	cleaned := strings.NewReplacer("[", " ", "]", " ", ",", " ", "\"", " ").Replace(text)
	var out []netip.Addr
	for _, token := range strings.FieldsFunc(cleaned, func(r rune) bool {
		return r == ' ' || r == '\n' || r == '\t' || r == '/'
	}) {
		if addr, ok := ipFromToken(token); ok {
			out = append(out, addr)
		}
	}
	return out
}

func ipFromToken(token string) (netip.Addr, bool) {
	if i := strings.Index(token, "://"); i >= 0 {
		token = token[i+3:]
	}
	if addr, err := netip.ParseAddr(token); err == nil {
		return addr, true
	}
	host, port, ok := strings.Cut(token, ":")
	if !ok || port == "" || strings.Contains(host, ":") {
		return netip.Addr{}, false
	}
	for _, r := range port {
		if r < '0' || r > '9' {
			return netip.Addr{}, false
		}
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, false
	}
	return addr, true
}

func isOverlay(addr netip.Addr) bool {
	if addr.Is4() {
		b := addr.As4()
		return b[0] == 100 && b[1]&0xc0 == 0x40
	}
	b := addr.As16()
	return b[0]&0xfe == 0xfc
}

func cidr(addr netip.Addr) string {
	bits := 32
	if addr.Is6() {
		bits = 128
	}
	return fmt.Sprintf("%s/%d", addr, bits)
}

func directRule(addr netip.Addr) string {
	return "IP-CIDR," + cidr(addr) + ",DIRECT,no-resolve"
}

func insertDirectRules(root *yaml.Node, addrs []netip.Addr) {
	if len(addrs) == 0 {
		return
	}
	rules := mappingEnsure(root, "rules", &yaml.Node{Kind: yaml.SequenceNode})
	if rules.Kind != yaml.SequenceNode {
		return
	}
	var fresh []*yaml.Node
	for _, addr := range addrs {
		rule := directRule(addr)
		if sequenceHas(rules, rule) {
			continue
		}
		fresh = append(fresh, &yaml.Node{Kind: yaml.ScalarNode, Value: rule})
	}
	if len(fresh) == 0 {
		return
	}
	at := len(rules.Content)
	for i, item := range rules.Content {
		if strings.HasPrefix(item.Value, "MATCH") {
			at = i
			break
		}
	}
	rules.Content = append(rules.Content[:at], append(fresh, rules.Content[at:]...)...)
}

func excludeTun(root *yaml.Node, addrs []netip.Addr) {
	if len(addrs) == 0 {
		return
	}
	tun := mappingGet(root, "tun")
	if tun == nil || tun.Kind != yaml.MappingNode {
		tun = &yaml.Node{Kind: yaml.MappingNode}
		mappingSet(root, "tun", tun)
	}
	excludes := mappingGet(tun, "route-exclude-address")
	if excludes == nil || excludes.Kind != yaml.SequenceNode {
		excludes = &yaml.Node{Kind: yaml.SequenceNode}
		mappingSet(tun, "route-exclude-address", excludes)
	}
	for _, addr := range addrs {
		item := cidr(addr)
		if sequenceHas(excludes, item) {
			continue
		}
		excludes.Content = append(excludes.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: item})
	}
}

func pinOverlayProxies(root *yaml.Node, iface string) {
	proxies := mappingGet(root, "proxies")
	if proxies == nil || proxies.Kind != yaml.SequenceNode {
		return
	}
	for _, item := range proxies.Content {
		if item.Kind != yaml.MappingNode {
			continue
		}
		server := mappingGet(item, "server")
		if server == nil {
			continue
		}
		addr, err := netip.ParseAddr(server.Value)
		if err != nil || !isOverlay(addr) {
			continue
		}
		mappingSet(item, "interface-name", &yaml.Node{Kind: yaml.ScalarNode, Value: iface})
	}
}

func sequenceHas(seq *yaml.Node, value string) bool {
	for _, item := range seq.Content {
		if item.Value == value {
			return true
		}
	}
	return false
}

func mappingGet(mapping *yaml.Node, key string) *yaml.Node {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}

func mappingSet(mapping *yaml.Node, key string, value *yaml.Node) {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content[i+1] = value
			return
		}
	}
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: key},
		value,
	)
}

func mappingEnsure(mapping *yaml.Node, key string, value *yaml.Node) *yaml.Node {
	if existing := mappingGet(mapping, key); existing != nil {
		return existing
	}
	mappingSet(mapping, key, value)
	return value
}
