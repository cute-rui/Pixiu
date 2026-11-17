package bridge

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"net/url"
	"sort"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/net/idna"
)

const (
	kindURL       = "url"
	kindMissing   = "missing"
	kindTransient = "transient"
	kindRejected  = "rejected"

	dnsSRV   = 33
	dnsA     = 1
	dnsAAAA  = 28
	dnsCNAME = 5
	dnsIN    = 1
)

var queryID atomic.Uint32

// ProfileResult is one _pixiu._tcp lookup. URLs are the ordered candidates
// that survived target and address checks. Nothing is written here.
type ProfileResult struct {
	Kind    string   `json:"kind"`
	Host    string   `json:"host"`
	URL     string   `json:"url"`
	URLs    []string `json:"urls"`
	Message string   `json:"message"`
}

type srvRecord struct {
	priority uint16
	weight   uint16
	port     uint16
	target   string
}

type dnsMessage struct {
	rcode uint16
	srv   []srvRecord
	addrs []netip.Addr
	cname string
	trunc bool
}

type srvQuery func(context.Context, string, []string, ProtectFunc) ([]srvRecord, error)
type hostResolve func(context.Context, string, []string, ProtectFunc) ([]netip.Addr, error)

// DiscoverJSON looks up _pixiu._tcp for the management URL and returns ProfileResult JSON.
func DiscoverJSON(ctx context.Context, managementURL string, servers []string, protect ProtectFunc) (string, error) {
	buf, err := json.Marshal(DiscoverProfile(ctx, managementURL, servers, protect))
	if err != nil {
		return "", err
	}
	return string(buf), nil
}

// DiscoverProfile queries _pixiu._tcp.<management-hostname>. using the given
// resolvers. Servers on the overlay, loopback, link-local, unspecified, or
// multicast ranges are skipped.
func DiscoverProfile(ctx context.Context, managementURL string, servers []string, protect ProtectFunc) ProfileResult {
	return discoverProfile(ctx, managementURL, usableDNS(servers), protect, querySRV, resolveHost)
}

func discoverProfile(ctx context.Context, managementURL string, servers []string, protect ProtectFunc, query srvQuery, resolve hostResolve) ProfileResult {
	host, kind := managementHost(managementURL)
	switch kind {
	case kindMissing:
		return profileResult(kindMissing, "", nil, "The management address is an IP address, so _pixiu._tcp was not queried. The subscription was left unchanged.")
	case kindTransient:
		return profileResult(kindTransient, "", nil, "The management URL could not be read. The subscription was left unchanged.")
	}
	if len(servers) == 0 {
		return profileResult(kindTransient, host, nil, fmt.Sprintf("No DNS server outside the tunnel is available for %s. The subscription was left unchanged.", host))
	}
	records, err := query(ctx, host, servers, protect)
	if err != nil {
		return profileResult(kindTransient, host, nil, fmt.Sprintf("Could not look up _pixiu._tcp on %s: %s. The subscription was left unchanged.", host, err))
	}
	sorted := sortSRV(records)
	if len(sorted) == 0 {
		return missingProfile(host)
	}

	var candidates []srvRecord
	onlyUnavailable := true
	sawIP := false
	for _, record := range sorted {
		if record.port == 0 || record.target == "" || record.target == "." {
			continue
		}
		onlyUnavailable = false
		target, ipLiteral := canonicalTarget(record.target)
		if ipLiteral {
			sawIP = true
			continue
		}
		if target == "" {
			sawIP = true
			continue
		}
		candidates = append(candidates, srvRecord{
			priority: record.priority,
			weight:   record.weight,
			port:     record.port,
			target:   target,
		})
	}
	if len(candidates) == 0 {
		if onlyUnavailable && !sawIP {
			return missingProfile(host)
		}
		return rejectedProfile(host)
	}

	var urls []string
	sawRejected := sawIP
	sawTransient := false
	for _, record := range candidates {
		addrs, err := resolve(ctx, record.target, servers, protect)
		if err != nil {
			sawTransient = true
			continue
		}
		if addressesRejected(addrs) {
			sawRejected = true
			continue
		}
		urls = append(urls, subscriptionURL(record.target, record.port))
	}
	if len(urls) == 0 {
		if sawRejected && !sawTransient {
			return rejectedProfile(host)
		}
		return profileResult(kindTransient, host, nil, fmt.Sprintf("Could not look up _pixiu._tcp on %s. The subscription was left unchanged.", host))
	}
	return profileResult(kindURL, host, urls, "")
}

func profileResult(kind, host string, urls []string, message string) ProfileResult {
	if urls == nil {
		urls = []string{}
	}
	url := ""
	if len(urls) > 0 {
		url = urls[0]
	}
	return ProfileResult{Kind: kind, Host: host, URL: url, URLs: urls, Message: message}
}

func missingProfile(host string) ProfileResult {
	return profileResult(kindMissing, host, nil, fmt.Sprintf("No _pixiu._tcp record on %s. The subscription was left unchanged.", host))
}

func rejectedProfile(host string) ProfileResult {
	return profileResult(kindRejected, host, nil, fmt.Sprintf("The _pixiu._tcp target on %s was rejected because it points at an overlay, loopback, link-local, or other local address. The subscription was left unchanged.", host))
}

func managementHost(raw string) (string, string) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", kindTransient
	}
	if !strings.Contains(trimmed, "://") {
		trimmed = "https://" + trimmed
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", kindTransient
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return "", kindTransient
	}
	host := parsed.Hostname()
	host = strings.TrimSuffix(host, ".")
	if host == "" {
		return "", kindTransient
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return "", kindMissing
	}
	ascii, err := idna.Lookup.ToASCII(host)
	if err != nil || ascii == "" {
		return "", kindTransient
	}
	ascii = strings.ToLower(ascii)
	if _, err := netip.ParseAddr(ascii); err == nil {
		return "", kindMissing
	}
	return ascii, ""
}

// canonicalTarget returns the ASCII hostname. The bool is true when the
// target is an IP literal and must be rejected rather than treated as missing.
func canonicalTarget(target string) (string, bool) {
	target = strings.TrimSpace(target)
	target = strings.TrimSuffix(target, ".")
	if target == "" || target == "." {
		return "", false
	}
	if _, err := netip.ParseAddr(strings.Trim(target, "[]")); err == nil {
		return "", true
	}
	ascii, err := idna.Lookup.ToASCII(target)
	if err != nil || ascii == "" {
		return "", false
	}
	ascii = strings.ToLower(ascii)
	if _, err := netip.ParseAddr(ascii); err == nil {
		return "", true
	}
	if strings.Contains(ascii, " ") || strings.Contains(ascii, "/") {
		return "", false
	}
	return ascii, false
}

func subscriptionURL(host string, port uint16) string {
	if port == 443 {
		return "https://" + host + "/pixiu.yaml"
	}
	return fmt.Sprintf("https://%s:%d/pixiu.yaml", host, port)
}

func sortSRV(records []srvRecord) []srvRecord {
	sorted := append([]srvRecord(nil), records...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].priority != sorted[j].priority {
			return sorted[i].priority < sorted[j].priority
		}
		if sorted[i].weight != sorted[j].weight {
			return sorted[i].weight > sorted[j].weight
		}
		if sorted[i].target != sorted[j].target {
			return sorted[i].target < sorted[j].target
		}
		return sorted[i].port < sorted[j].port
	})
	return sorted
}

func addressesRejected(addrs []netip.Addr) bool {
	if len(addrs) == 0 {
		return true
	}
	for _, addr := range addrs {
		if rejectedProfileIP(addr) {
			return true
		}
	}
	return false
}

func rejectedProfileIP(addr netip.Addr) bool {
	if !addr.IsValid() {
		return true
	}
	addr = addr.Unmap()
	if Overlay(addr, nil) {
		return true
	}
	return addr.IsLoopback() || addr.IsUnspecified() || addr.IsMulticast() || addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast()
}

func usableDNS(servers []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, server := range servers {
		normalized, ok := normalizeDNS(server)
		if !ok || seen[normalized] {
			continue
		}
		seen[normalized] = true
		out = append(out, normalized)
	}
	return out
}

func normalizeDNS(server string) (string, bool) {
	server = strings.TrimSpace(server)
	if server == "" {
		return "", false
	}
	host := server
	port := "53"
	if h, p, err := net.SplitHostPort(server); err == nil {
		host, port = h, p
	} else if strings.HasPrefix(server, "[") && strings.Contains(server, "]") {
		return "", false
	}
	ip, err := netip.ParseAddr(strings.Trim(host, "[]"))
	if err != nil || rejectedProfileIP(ip) {
		return "", false
	}
	return net.JoinHostPort(ip.String(), port), true
}

func querySRV(ctx context.Context, host string, servers []string, protect ProtectFunc) ([]srvRecord, error) {
	message, err := queryName(ctx, "_pixiu._tcp."+host+".", dnsSRV, servers, protect)
	if err != nil {
		return nil, err
	}
	if message.rcode == 3 {
		return nil, nil
	}
	if message.rcode != 0 {
		return nil, fmt.Errorf("DNS rcode %d", message.rcode)
	}
	return message.srv, nil
}

func resolveHost(ctx context.Context, host string, servers []string, protect ProtectFunc) ([]netip.Addr, error) {
	name := strings.TrimSuffix(host, ".") + "."
	var last error
	for range 4 {
		v4, err4 := queryName(ctx, name, dnsA, servers, protect)
		v6, err6 := queryName(ctx, name, dnsAAAA, servers, protect)
		var addrs []netip.Addr
		cname := ""
		answered := false
		for _, item := range []struct {
			msg dnsMessage
			err error
		}{{v4, err4}, {v6, err6}} {
			if item.err != nil {
				last = item.err
				continue
			}
			if item.msg.rcode != 0 && item.msg.rcode != 3 {
				last = fmt.Errorf("DNS rcode %d", item.msg.rcode)
				continue
			}
			answered = true
			addrs = append(addrs, item.msg.addrs...)
			if cname == "" {
				cname = item.msg.cname
			}
		}
		if len(addrs) > 0 {
			sort.Slice(addrs, func(i, j int) bool { return addrs[i].Compare(addrs[j]) < 0 })
			out := addrs[:0]
			var prev netip.Addr
			for _, addr := range addrs {
				if addr == prev {
					continue
				}
				out = append(out, addr)
				prev = addr
			}
			return out, nil
		}
		if cname != "" && cname != name {
			name = cname
			continue
		}
		if answered {
			return nil, nil
		}
		if last == nil {
			last = errors.New("DNS lookup failed")
		}
		return nil, last
	}
	return nil, errors.New("DNS lookup failed")
}

func queryName(ctx context.Context, name string, qtype uint16, servers []string, protect ProtectFunc) (dnsMessage, error) {
	packet, err := buildQuery(name, qtype)
	if err != nil {
		return dnsMessage{}, err
	}
	var last error
	for _, server := range servers {
		buf, err := exchange(ctx, server, packet, protect)
		if err != nil {
			last = err
			continue
		}
		message, err := parseDNS(buf)
		if err != nil {
			last = err
			continue
		}
		if message.rcode == 2 {
			last = errors.New("DNS SERVFAIL")
			continue
		}
		return message, nil
	}
	if last == nil {
		last = errors.New("DNS lookup failed")
	}
	return dnsMessage{}, last
}

func exchange(ctx context.Context, server string, packet []byte, protect ProtectFunc) ([]byte, error) {
	buf, err := exchangeUDP(ctx, server, packet, protect)
	if err != nil {
		return nil, err
	}
	if dnsTruncated(buf) {
		return exchangeTCP(ctx, server, packet, protect)
	}
	return buf, nil
}

func exchangeUDP(ctx context.Context, server string, packet []byte, protect ProtectFunc) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	conn, err := protectedDialer(protect).DialContext(ctx, "udp", server)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn.Write(packet); err != nil {
		return nil, err
	}
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		return nil, err
	}
	buf = buf[:n]
	if len(buf) < 12 || binary.BigEndian.Uint16(buf[:2]) != binary.BigEndian.Uint16(packet[:2]) {
		return nil, errors.New("DNS response did not match the query")
	}
	return buf, nil
}

func exchangeTCP(ctx context.Context, server string, packet []byte, protect ProtectFunc) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	conn, err := protectedDialer(protect).DialContext(ctx, "tcp", server)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	var length [2]byte
	binary.BigEndian.PutUint16(length[:], uint16(len(packet)))
	if _, err := conn.Write(length[:]); err != nil {
		return nil, err
	}
	if _, err := conn.Write(packet); err != nil {
		return nil, err
	}
	if _, err := io.ReadFull(conn, length[:]); err != nil {
		return nil, err
	}
	size := int(binary.BigEndian.Uint16(length[:]))
	if size < 12 || size > 65535 {
		return nil, errors.New("DNS TCP response has a bad length")
	}
	buf := make([]byte, size)
	if _, err := io.ReadFull(conn, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

func protectedDialer(protect ProtectFunc) *net.Dialer {
	return &net.Dialer{
		Timeout: 3 * time.Second,
		Control: func(network, address string, c syscall.RawConn) error {
			if protect == nil {
				return nil
			}
			var fd int
			if err := c.Control(func(s uintptr) { fd = int(s) }); err != nil {
				return err
			}
			if !protect(fd) {
				return errors.New("failed to exclude DNS socket from the tunnel")
			}
			return nil
		},
	}
}

func buildQuery(name string, qtype uint16) ([]byte, error) {
	id := uint16(queryID.Add(1))
	packet := []byte{
		byte(id >> 8), byte(id),
		0x01, 0x00,
		0x00, 0x01,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	}
	if err := encodeName(name, &packet); err != nil {
		return nil, err
	}
	packet = append(packet, byte(qtype>>8), byte(qtype), 0x00, byte(dnsIN))
	return packet, nil
}

func encodeName(name string, out *[]byte) error {
	name = strings.TrimSuffix(name, ".")
	if name == "" {
		*out = append(*out, 0)
		return nil
	}
	for _, label := range strings.Split(name, ".") {
		if label == "" || len(label) > 63 {
			return errors.New("DNS name has an empty or oversized label")
		}
		*out = append(*out, byte(len(label)))
		*out = append(*out, label...)
	}
	*out = append(*out, 0)
	return nil
}

func dnsTruncated(packet []byte) bool {
	return len(packet) >= 4 && binary.BigEndian.Uint16(packet[2:4])&0x0200 != 0
}

func parseDNS(packet []byte) (dnsMessage, error) {
	if len(packet) < 12 {
		return dnsMessage{}, errors.New("DNS response is too short")
	}
	flags := binary.BigEndian.Uint16(packet[2:4])
	questions := int(binary.BigEndian.Uint16(packet[4:6]))
	answers := int(binary.BigEndian.Uint16(packet[6:8]))
	pos := 12
	for range questions {
		_, next, err := readName(packet, pos)
		if err != nil {
			return dnsMessage{}, err
		}
		pos = next + 4
		if pos > len(packet) {
			return dnsMessage{}, errors.New("DNS question is truncated")
		}
	}
	message := dnsMessage{rcode: flags & 0x000f, trunc: flags&0x0200 != 0}
	for range answers {
		_, next, err := readName(packet, pos)
		if err != nil {
			return dnsMessage{}, err
		}
		pos = next
		if pos+10 > len(packet) {
			return dnsMessage{}, errors.New("DNS answer is truncated")
		}
		qtype := binary.BigEndian.Uint16(packet[pos : pos+2])
		class := binary.BigEndian.Uint16(packet[pos+2 : pos+4])
		rdlen := int(binary.BigEndian.Uint16(packet[pos+8 : pos+10]))
		pos += 10
		if pos+rdlen > len(packet) {
			return dnsMessage{}, errors.New("DNS rdata is truncated")
		}
		rdata := packet[pos : pos+rdlen]
		if class == dnsIN {
			switch qtype {
			case dnsSRV:
				if rdlen >= 6 {
					target, _, err := readName(packet, pos+6)
					if err != nil {
						return dnsMessage{}, err
					}
					message.srv = append(message.srv, srvRecord{
						priority: binary.BigEndian.Uint16(rdata[0:2]),
						weight:   binary.BigEndian.Uint16(rdata[2:4]),
						port:     binary.BigEndian.Uint16(rdata[4:6]),
						target:   target,
					})
				}
			case dnsA:
				if rdlen == 4 {
					message.addrs = append(message.addrs, netip.AddrFrom4([4]byte{rdata[0], rdata[1], rdata[2], rdata[3]}))
				}
			case dnsAAAA:
				if rdlen == 16 {
					var octets [16]byte
					copy(octets[:], rdata)
					message.addrs = append(message.addrs, netip.AddrFrom16(octets))
				}
			case dnsCNAME:
				target, _, err := readName(packet, pos)
				if err != nil {
					return dnsMessage{}, err
				}
				if message.cname == "" {
					message.cname = target
				}
			}
		}
		pos += rdlen
	}
	return message, nil
}

func readName(packet []byte, pos int) (string, int, error) {
	var labels []string
	end := -1
	for range 16 {
		if pos >= len(packet) {
			return "", 0, errors.New("DNS name is truncated")
		}
		length := int(packet[pos])
		if length == 0 {
			if end < 0 {
				end = pos + 1
			}
			name := strings.Join(labels, ".")
			if name != "" {
				name += "."
			}
			return name, end, nil
		}
		if length&0xc0 == 0xc0 {
			if pos+1 >= len(packet) {
				return "", 0, errors.New("DNS name pointer is truncated")
			}
			if end < 0 {
				end = pos + 2
			}
			pos = (length&0x3f)<<8 | int(packet[pos+1])
			continue
		}
		if length&0xc0 != 0 {
			return "", 0, errors.New("DNS name has an unsupported label")
		}
		start := pos + 1
		stop := start + length
		if stop > len(packet) {
			return "", 0, errors.New("DNS name is truncated")
		}
		labels = append(labels, string(packet[start:stop]))
		pos = stop
	}
	return "", 0, errors.New("DNS name has too many labels")
}
