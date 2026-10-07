//go:build !android && !ios

package pixiu

import "github.com/netbirdio/netbird/client/iface/device"

// AttachDevice is a no-op outside the mobile clients. Desktop keeps Mihomo
// in Verge and does not divert the NetBird TUN.
func AttachDevice(_ *device.FilteredDevice) {}
