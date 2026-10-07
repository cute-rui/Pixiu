//go:build android || ios

package pixiu

import (
	"context"
	"sync"

	"github.com/pixiu/mihomo-bridge"

	"github.com/netbirdio/netbird/client/iface/device"
)

// Controller is the mobile Mihomo exit. NetBird keeps the system TUN.
type Controller struct {
	engine *bridge.Engine
	device *device.FilteredDevice
}

var (
	mu         sync.Mutex
	controller = &Controller{engine: &bridge.Engine{}}
)

func current() *Controller {
	return controller
}

// AttachDevice remembers the live TUN filter so a later node selection can
// divert non-overlay packets without restarting WireGuard.
func AttachDevice(dev *device.FilteredDevice) {
	mu.Lock()
	defer mu.Unlock()
	controller.device = dev
	controller.applyDivertLocked()
}

// SetProtectFunc excludes subscription fetches from the system TUN when the
// URL is not a NetBird address.
func SetProtectFunc(fn func(fd int) bool) {
	mu.Lock()
	defer mu.Unlock()
	if fn == nil {
		controller.engine.SetProtectFunc(nil)
		return
	}
	controller.engine.SetProtectFunc(bridge.ProtectFunc(fn))
}

// UpdateSubscription loads the single subscription and hot-reloads Mihomo.
func UpdateSubscription(ctx context.Context, rawURL string) (string, error) {
	mu.Lock()
	engine := controller.engine
	mu.Unlock()
	return engine.UpdateSubscription(ctx, rawURL)
}

// SelectNode enables one subscription node. Native default routes are not
// selected here; the platform installs them only after this returns nil.
func SelectNode(name string) error {
	mu.Lock()
	defer mu.Unlock()
	if err := controller.engine.Select(name); err != nil {
		return err
	}
	controller.applyDivertLocked()
	return nil
}

// ClearNode turns the Mihomo exit off. It does not reselect a NetBird exit node.
func ClearNode() {
	mu.Lock()
	defer mu.Unlock()
	controller.engine.Clear()
	controller.applyDivertLocked()
}

func (c *Controller) applyDivertLocked() {
	if c.device == nil {
		return
	}
	if c.engine.SelectedNode() == "" {
		c.device.SetDivert(nil, nil)
		return
	}
	c.device.SetDivert(packetSink{engine: c.engine}, nil)
}

type packetSink struct {
	engine *bridge.Engine
}

func (s packetSink) WritePacket(packet []byte) error {
	return s.engine.WritePacket(packet)
}
