//go:build android

package pixiu

import (
	"context"
	"strings"

	"github.com/pixiu/mihomo-bridge"
)

// DiscoverSubscription looks up _pixiu._tcp for managementURL.
// dnsServers is one resolver address per line. The result is ProfileResult JSON
// and does not change the saved subscription.
func DiscoverSubscription(ctx context.Context, managementURL, dnsServers string) (string, error) {
	servers := splitDNS(dnsServers)
	mu.Lock()
	engine := controller.engine
	mu.Unlock()
	engine.SetProfileDNS(servers)
	return bridge.DiscoverJSON(ctx, managementURL, servers, engine.Protect())
}

func splitDNS(raw string) []string {
	return strings.FieldsFunc(raw, func(r rune) bool {
		return r == '\n' || r == '\r' || r == ',' || r == ' ' || r == '\t'
	})
}
