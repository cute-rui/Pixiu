package bridge

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

var (
	errNotOverlay = errors.New("proxy server is not a NetBird address")
	errNoProxies  = errors.New("subscription has no proxies")
	errInvalidSub = errors.New("invalid subscription")
)

// Node is one outbound proxy the UI can select.
type Node struct {
	Name   string `json:"name"`
	Type   string `json:"type"`
	Server string `json:"server"`
	Port   int    `json:"port"`
}

// Runtime is the Mihomo config produced from one subscription.
type Runtime struct {
	YAML  []byte
	Group string
	Nodes []Node
}

// ParseSubscription decodes one Clash/Mihomo subscription (raw YAML, or YAML
// wrapped in base64) into a runtime config.
//
// The first type:select group becomes the node list. When the document has no
// select group, proxies are placed in a group named Pixiu and the only rule is
// MATCH,Pixiu.
func ParseSubscription(raw []byte) (*Runtime, error) {
	body, err := decodeSubscription(raw)
	if err != nil {
		return nil, err
	}
	var doc map[string]any
	if err := yaml.Unmarshal(body, &doc); err != nil || doc == nil {
		return nil, errInvalidSub
	}
	return buildRuntime(doc)
}

func decodeSubscription(raw []byte) ([]byte, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, errInvalidSub
	}
	if isYAMLDocument(trimmed) {
		return trimmed, nil
	}
	compact := bytes.Map(func(r rune) rune {
		switch r {
		case ' ', '\n', '\r', '\t':
			return -1
		default:
			return r
		}
	}, trimmed)
	decoded, err := base64.StdEncoding.DecodeString(string(compact))
	if err != nil {
		decoded, err = base64.RawStdEncoding.DecodeString(string(compact))
	}
	if err != nil || !isYAMLDocument(bytes.TrimSpace(decoded)) {
		return nil, errInvalidSub
	}
	return decoded, nil
}

func isYAMLDocument(b []byte) bool {
	var doc map[string]any
	if err := yaml.Unmarshal(b, &doc); err != nil || doc == nil {
		return false
	}
	_, proxies := doc["proxies"]
	_, groups := doc["proxy-groups"]
	return proxies || groups
}

func buildRuntime(doc map[string]any) (*Runtime, error) {
	proxies := mapSlice(doc["proxies"])
	concrete := concreteProxies(proxies)
	if len(concrete) == 0 {
		return nil, errNoProxies
	}

	groupName := ""
	var nodes []Node
	groups := mapSlice(doc["proxy-groups"])
	for _, g := range groups {
		if !strings.EqualFold(str(g["type"]), "select") {
			continue
		}
		groupName = str(g["name"])
		if groupName == "" {
			continue
		}
		nodes = nodesForNames(stringList(g["proxies"]), concrete)
		break
	}

	if groupName == "" || len(nodes) == 0 {
		if groupName != "" && len(nodes) == 0 {
			return nil, errNoProxies
		}
		names := make([]any, 0, len(concrete))
		nodes = make([]Node, 0, len(concrete))
		for _, p := range concrete {
			names = append(names, p.Name)
			nodes = append(nodes, p)
		}
		groups = append(groups, map[string]any{
			"name":    "Pixiu",
			"type":    "select",
			"proxies": names,
		})
		doc["proxy-groups"] = groups
		doc["rules"] = []any{"MATCH,Pixiu"}
		groupName = "Pixiu"
	} else if len(stringList(doc["rules"])) == 0 && len(mapSlice(doc["rules"])) == 0 {
		doc["rules"] = []any{"MATCH," + groupName}
	}

	tun := asMap(doc["tun"])
	tun["enable"] = false
	tun["stack"] = "gvisor"
	tun["auto-route"] = false
	tun["auto-detect-interface"] = false
	doc["tun"] = tun
	if str(doc["mode"]) == "" {
		doc["mode"] = "rule"
	}
	doc["geo-auto-update"] = false

	out, err := yaml.Marshal(doc)
	if err != nil {
		return nil, err
	}
	return &Runtime{YAML: out, Group: groupName, Nodes: nodes}, nil
}

func concreteProxies(items []map[string]any) []Node {
	var nodes []Node
	for _, item := range items {
		typ := strings.ToLower(str(item["type"]))
		switch typ {
		case "", "select", "url-test", "fallback", "load-balance", "relay":
			continue
		}
		name := str(item["name"])
		if name == "" {
			continue
		}
		nodes = append(nodes, Node{
			Name:   name,
			Type:   typ,
			Server: str(item["server"]),
			Port:   asInt(item["port"]),
		})
	}
	return nodes
}

func nodesForNames(names []string, concrete []Node) []Node {
	byName := make(map[string]Node, len(concrete))
	for _, n := range concrete {
		byName[n.Name] = n
	}
	var nodes []Node
	for _, name := range names {
		if n, ok := byName[name]; ok {
			nodes = append(nodes, n)
		}
	}
	return nodes
}

func mapSlice(v any) []map[string]any {
	switch s := v.(type) {
	case []any:
		out := make([]map[string]any, 0, len(s))
		for _, item := range s {
			if m, ok := item.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	case []map[string]any:
		return s
	default:
		return nil
	}
}

func asMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

func stringList(v any) []string {
	switch s := v.(type) {
	case []any:
		out := make([]string, 0, len(s))
		for _, item := range s {
			if str, ok := item.(string); ok {
				out = append(out, str)
			}
		}
		return out
	case []string:
		return s
	default:
		return nil
	}
}

func str(v any) string {
	switch s := v.(type) {
	case string:
		return s
	case fmt.Stringer:
		return s.String()
	default:
		return ""
	}
}

func asInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case uint64:
		return int(n)
	case float64:
		return int(n)
	default:
		return 0
	}
}
