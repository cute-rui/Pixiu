package device

import (
	"net/netip"
)

// Default overlay ranges. Mesh addresses allocated by NetBird sit in
// 100.64.0.0/10. IPv6 unique local addresses are fc00::/7.
var defaultOverlayPrefixes = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("fc00::/7"),
}

// PacketSink receives IP packets that the system TUN should not hand to
// WireGuard. Mihomo's gVisor stack is the sink used when it is the exit.
type PacketSink interface {
	WritePacket(packet []byte) error
}

type divertState struct {
	sink     PacketSink
	prefixes []netip.Prefix
}

// SetDivert installs the Mihomo sink. A nil sink restores the normal path,
// where every packet is eligible for WireGuard.
// extra holds mesh prefixes outside the default overlay ranges.
func (d *FilteredDevice) SetDivert(sink PacketSink, extra []netip.Prefix) {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	if sink == nil {
		d.divert = nil
		return
	}
	prefixes := make([]netip.Prefix, 0, len(defaultOverlayPrefixes)+len(extra))
	prefixes = append(prefixes, defaultOverlayPrefixes...)
	prefixes = append(prefixes, extra...)
	d.divert = &divertState{sink: sink, prefixes: prefixes}
}

// Overlay reports whether addr belongs to the default NetBird overlay.
func Overlay(addr netip.Addr) bool {
	return overlayIn(addr, defaultOverlayPrefixes)
}

func overlayIn(addr netip.Addr, prefixes []netip.Prefix) bool {
	if !addr.IsValid() {
		return false
	}
	addr = addr.Unmap()
	for _, p := range prefixes {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// PacketDestination returns the destination address of an IPv4 or IPv6 packet.
func PacketDestination(packet []byte) (netip.Addr, bool) {
	if len(packet) < 1 {
		return netip.Addr{}, false
	}
	switch packet[0] >> 4 {
	case 4:
		if len(packet) < 20 {
			return netip.Addr{}, false
		}
		return netip.AddrFrom4([4]byte{packet[16], packet[17], packet[18], packet[19]}), true
	case 6:
		if len(packet) < 40 {
			return netip.Addr{}, false
		}
		var dst [16]byte
		copy(dst[:], packet[24:40])
		return netip.AddrFrom16(dst), true
	default:
		return netip.Addr{}, false
	}
}

func divertOutbound(bufs [][]byte, sizes []int, n, offset int, state *divertState) int {
	for i := 0; i < n; i++ {
		pkt := bufs[i][offset : offset+sizes[i]]
		dst, ok := PacketDestination(pkt)
		if ok && overlayIn(dst, state.prefixes) {
			continue
		}
		cloned := append([]byte(nil), pkt...)
		if err := state.sink.WritePacket(cloned); err != nil {
			// Fail closed: a packet that cannot enter Mihomo is dropped
			// rather than forwarded through WireGuard or the underlay.
		}
		copy(bufs[i:], bufs[i+1:n])
		copy(sizes[i:], sizes[i+1:n])
		n--
		i--
	}
	return n
}
