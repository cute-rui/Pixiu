package device

import (
	"net/netip"
	"testing"

	"go.uber.org/mock/gomock"

	mocks "github.com/netbirdio/netbird/client/iface/mocks"
)

func TestOverlay(t *testing.T) {
	inside := []string{"100.64.0.1", "100.127.255.254", "fd12:3456::1", "fc00::2"}
	outside := []string{"100.63.255.255", "100.128.0.1", "1.1.1.1", "192.168.0.1", "8.8.8.8", "2001:db8::1"}
	for _, s := range inside {
		if !Overlay(netip.MustParseAddr(s)) {
			t.Fatalf("%s should stay on WireGuard", s)
		}
	}
	for _, s := range outside {
		if Overlay(netip.MustParseAddr(s)) {
			t.Fatalf("%s should leave the overlay", s)
		}
	}
}

func TestPacketDestination(t *testing.T) {
	v4 := make([]byte, 20)
	v4[0] = 0x45
	copy(v4[16:20], []byte{1, 2, 3, 4})
	dst, ok := PacketDestination(v4)
	if !ok || dst.String() != "1.2.3.4" {
		t.Fatalf("v4 dst = %s ok=%v", dst, ok)
	}

	v6 := make([]byte, 40)
	v6[0] = 0x60
	addr := netip.MustParseAddr("2001:db8::5")
	copy(v6[24:40], addr.AsSlice())
	dst, ok = PacketDestination(v6)
	if !ok || dst != addr {
		t.Fatalf("v6 dst = %s ok=%v", dst, ok)
	}

	if _, ok := PacketDestination([]byte{0x45, 0x00}); ok {
		t.Fatal("truncated packet should not yield a destination")
	}
}

type recordSink struct {
	packets [][]byte
	err     error
}

func (r *recordSink) WritePacket(packet []byte) error {
	if r.err != nil {
		return r.err
	}
	r.packets = append(r.packets, append([]byte(nil), packet...))
	return nil
}

func ipv4(dst [4]byte) []byte {
	pkt := make([]byte, 20)
	pkt[0] = 0x45
	copy(pkt[16:20], dst[:])
	return pkt
}

func TestReadDiverts(t *testing.T) {
	ctrl := gomock.NewController(t)
	tun := mocks.NewMockDevice(ctrl)

	overlayPkt := ipv4([4]byte{100, 64, 0, 8})
	publicPkt := ipv4([4]byte{1, 1, 1, 1})
	offset := 16

	tun.EXPECT().Read(gomock.Any(), gomock.Any(), offset).DoAndReturn(
		func(bufs [][]byte, sizes []int, offset int) (int, error) {
			bufs[0] = append(make([]byte, offset), overlayPkt...)
			sizes[0] = len(overlayPkt)
			bufs[1] = append(make([]byte, offset), publicPkt...)
			sizes[1] = len(publicPkt)
			return 2, nil
		},
	)

	sink := &recordSink{}
	wrapped := newDeviceFilter(tun)
	wrapped.SetDivert(sink, nil)

	bufs := [][]byte{{}, {}}
	sizes := []int{0, 0}
	n, err := wrapped.Read(bufs, sizes, offset)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("wireguard packet count = %d, want 1", n)
	}
	got, ok := PacketDestination(bufs[0][offset : offset+sizes[0]])
	if !ok || got.String() != "100.64.0.8" {
		t.Fatalf("kept packet dest = %s ok=%v", got, ok)
	}
	if len(sink.packets) != 1 {
		t.Fatalf("diverted %d packets", len(sink.packets))
	}
	div, ok := PacketDestination(sink.packets[0])
	if !ok || div.String() != "1.1.1.1" {
		t.Fatalf("diverted dest = %s ok=%v", div, ok)
	}
}
