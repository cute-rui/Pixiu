package pixiu

import "github.com/pixiu/mihomo-bridge"

// MihomoVersion is the Mihomo release the shared bridge compiles against.
func MihomoVersion() string {
	return bridge.MihomoVersion
}
