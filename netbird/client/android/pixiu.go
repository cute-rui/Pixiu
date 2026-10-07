//go:build android

package android

import (
	"context"

	"github.com/netbirdio/netbird/client/pixiu"
)

// Protector excludes a socket from the VPN. Android passes VpnService.protect.
type Protector interface {
	Protect(fd int) bool
}

// SetPixiuProtector installs the VPN protect hook used for subscription fetches
// that are not addressed to the overlay.
func (c *Client) SetPixiuProtector(p Protector) {
	if p == nil {
		pixiu.SetProtectFunc(nil)
		return
	}
	pixiu.SetProtectFunc(p.Protect)
}

// PixiuUpdateSubscription fetches the single Mihomo subscription and returns
// a JSON node list. Mihomo hot-reloads; NetBird is not restarted.
func (c *Client) PixiuUpdateSubscription(url string) (string, error) {
	return pixiu.UpdateSubscription(context.Background(), url)
}

// PixiuSelectNode switches the Mihomo exit to name.
func (c *Client) PixiuSelectNode(name string) error {
	return pixiu.SelectNode(name)
}

// PixiuClearNode disables the Mihomo exit without selecting a NetBird exit node.
func (c *Client) PixiuClearNode() {
	pixiu.ClearNode()
}
