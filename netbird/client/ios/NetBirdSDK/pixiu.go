//go:build ios

package NetBirdSDK

import (
	"context"

	"github.com/netbirdio/netbird/client/pixiu"
)

// PixiuUpdateSubscription fetches the single Mihomo subscription and returns
// a JSON node list. tvOS does not call this.
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
