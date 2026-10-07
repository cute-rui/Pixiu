package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/metacubex/mihomo/adapter/outboundgroup"
	"github.com/metacubex/mihomo/config"
	"github.com/metacubex/mihomo/hub/executor"
	"github.com/metacubex/mihomo/tunnel"
)

// Engine runs one in-process Mihomo and hot-reloads a single subscription.
type Engine struct {
	mu       sync.Mutex
	protect  ProtectFunc
	runtime  *Runtime
	selected string
	group    string
	running  bool
	packets  io.Writer
}

// SetProtectFunc installs the platform hook used while fetching a subscription
// whose URL is outside the overlay.
func (e *Engine) SetProtectFunc(fn ProtectFunc) {
	e.mu.Lock()
	e.protect = fn
	e.mu.Unlock()
}

// SetPacketWriter receives IP packets diverted off the system TUN.
func (e *Engine) SetPacketWriter(w io.Writer) {
	e.mu.Lock()
	e.packets = w
	e.mu.Unlock()
}

// WritePacket delivers one diverted IP packet to Mihomo.
func (e *Engine) WritePacket(packet []byte) error {
	e.mu.Lock()
	w := e.packets
	selected := e.selected
	e.mu.Unlock()
	if selected == "" {
		return errors.New("mihomo exit node is not selected")
	}
	if w == nil {
		return errors.New("mihomo packet writer is not set")
	}
	_, err := w.Write(packet)
	return err
}

// UpdateSubscription fetches one subscription and hot-reloads Mihomo.
// The previously selected node is kept when it is still present.
func (e *Engine) UpdateSubscription(ctx context.Context, rawURL string) (string, error) {
	e.mu.Lock()
	protect := e.protect
	prev := e.selected
	e.mu.Unlock()

	body, err := fetchSubscription(ctx, rawURL, protect, nil)
	if err != nil {
		return "", err
	}
	return e.ApplySubscription(body, prev)
}

// ApplySubscription hot-reloads Mihomo from subscription bytes already in memory.
func (e *Engine) ApplySubscription(body []byte, previous string) (string, error) {
	rt, err := ParseSubscription(body)
	if err != nil {
		return "", err
	}
	cfg, err := config.Parse(rt.YAML)
	if err != nil {
		return "", fmt.Errorf("invalid subscription: %w", err)
	}
	executor.ApplyConfig(cfg, true)

	e.mu.Lock()
	e.runtime = rt
	e.group = rt.Group
	e.running = true
	e.selected = ""
	e.mu.Unlock()

	if previous != "" && e.hasNode(previous) {
		if err := e.Select(previous); err != nil {
			e.Clear()
			return e.nodesJSON(), fmt.Errorf("selected node %s is no longer usable: %w", previous, err)
		}
	}
	installDialHook(nil)
	return e.nodesJSON(), nil
}

// Select switches the active proxy inside the subscription group.
// The server must be a NetBird address. The switch does not restart NetBird.
func (e *Engine) Select(name string) error {
	e.mu.Lock()
	rt := e.runtime
	group := e.group
	e.mu.Unlock()
	if rt == nil {
		return errors.New("subscription is not loaded")
	}
	var node *Node
	for i := range rt.Nodes {
		if rt.Nodes[i].Name == name {
			node = &rt.Nodes[i]
			break
		}
	}
	if node == nil {
		return fmt.Errorf("unknown node %s", name)
	}
	if err := ValidateServer(node.Server, nil); err != nil {
		return fmt.Errorf("refusing %s: %w", name, err)
	}

	proxy, ok := tunnel.Proxies()[group]
	if !ok {
		return fmt.Errorf("group %s is not loaded", group)
	}
	selector, ok := proxy.Adapter().(outboundgroup.SelectAble)
	if !ok {
		return fmt.Errorf("group %s is not a select group", group)
	}
	if err := selector.Set(name); err != nil {
		return err
	}
	installDialHook(nil)
	e.mu.Lock()
	e.selected = name
	e.mu.Unlock()
	return nil
}

// SelectedNode is empty until a node is chosen.
func (e *Engine) SelectedNode() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.selected
}

// Clear stops using Mihomo as the exit. It does not select a NetBird exit node.
func (e *Engine) Clear() {
	clearDialHook()
	e.mu.Lock()
	e.selected = ""
	e.mu.Unlock()
}

// Stop shuts Mihomo down.
func (e *Engine) Stop() {
	e.Clear()
	if e.running {
		executor.Shutdown()
	}
	e.mu.Lock()
	e.running = false
	e.runtime = nil
	e.mu.Unlock()
}

func (e *Engine) hasNode(name string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.runtime == nil {
		return false
	}
	for _, n := range e.runtime.Nodes {
		if n.Name == name {
			return true
		}
	}
	return false
}

func (e *Engine) nodesJSON() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.runtime == nil {
		return "[]"
	}
	buf, err := json.Marshal(e.runtime.Nodes)
	if err != nil {
		return "[]"
	}
	return string(buf)
}
