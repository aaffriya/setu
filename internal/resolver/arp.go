package resolver

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"strings"
)

// arpTablePath is the Linux kernel ARP table. It is a package var so tests can
// point it at a fixture file.
var arpTablePath = "/proc/net/arp"

// ARPResolver resolves MAC→IP by reading the kernel ARP/neighbour table
// (/proc/net/arp on Linux). It requires the process to share the host network
// namespace (run the container with host networking) and only knows about
// devices the host has talked to recently. This is the default resolver; it is
// intentionally simple — re-resolution and caching are handled per-device (see
// internal/devices/example).
//
// On non-Linux hosts (e.g. a macOS dev machine) /proc/net/arp does not exist
// and Lookup returns an error. That is fine for this phase: the default config
// ships zero devices, so nothing calls Lookup until a real device is added and
// deployed on the target router.
type ARPResolver struct{}

// NewARPResolver returns an ARPResolver.
func NewARPResolver() *ARPResolver { return &ARPResolver{} }

// Lookup scans the ARP table for mac and returns the matching IP.
func (r *ARPResolver) Lookup(mac string) (net.IP, error) {
	want, err := NormalizeMAC(mac)
	if err != nil {
		return nil, err
	}

	f, err := os.Open(arpTablePath)
	if err != nil {
		return nil, fmt.Errorf("resolver: open arp table: %w", err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	// The first line is a header: "IP address  HW type  Flags  HW address ...".
	if sc.Scan() {
		_ = sc.Text()
	}
	for sc.Scan() {
		// Columns: 0=IP 1=HWtype 2=Flags 3=HWaddress 4=Mask 5=Device.
		fields := strings.Fields(sc.Text())
		if len(fields) < 4 {
			continue
		}
		got, err := NormalizeMAC(fields[3])
		if err != nil || got != want {
			continue
		}
		ip := net.ParseIP(fields[0])
		if ip == nil {
			continue
		}
		return ip, nil
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("resolver: read arp table: %w", err)
	}
	return nil, fmt.Errorf("resolver: mac %s not found in arp table", want)
}

// NormalizeMAC reduces a 48-bit MAC to a canonical lowercase, separator-free
// hex string so addresses from different sources compare equal. It accepts the
// usual separated forms ("d8:a0:11:ff:5e:f0", "d8-a0-...", "d8a0.11ff.5ef0")
// and the bare 12-hex-digit form some devices report (e.g. WiZ: "d8a011ff5ef0").
func NormalizeMAC(mac string) (string, error) {
	var sb strings.Builder
	sb.Grow(12)
	for _, r := range strings.ToLower(strings.TrimSpace(mac)) {
		switch {
		case (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f'):
			sb.WriteRune(r)
		case r == ':' || r == '-' || r == '.':
			// separator — skip
		default:
			return "", fmt.Errorf("resolver: invalid mac %q", mac)
		}
	}
	if sb.Len() != 12 {
		return "", fmt.Errorf("resolver: invalid mac %q (want 48 bits)", mac)
	}
	return sb.String(), nil
}
