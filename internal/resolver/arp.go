package resolver

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"strconv"
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
	table, err := r.Neighbours()
	if err != nil {
		return nil, err
	}
	if ip, ok := table[want]; ok {
		return ip, nil
	}
	return nil, fmt.Errorf("resolver: mac %s not found in arp table", want)
}

// Neighbours returns every MAC the host currently has a resolved entry for,
// keyed by its normalized form. Incomplete entries — an address the kernel has
// asked about but not heard back from — are left out, so a stale probe never
// looks like a device that is present.
//
// It exists so a caller watching several addresses reads the table once instead
// of once per address; Lookup is that caller with one address.
func (r *ARPResolver) Neighbours() (map[string]net.IP, error) {
	f, err := os.Open(arpTablePath)
	if err != nil {
		return nil, fmt.Errorf("resolver: open arp table: %w", err)
	}
	defer f.Close()

	table := make(map[string]net.IP)
	sc := bufio.NewScanner(f)
	// The first line is a header: "IP address  HW type  Flags  HW address ...".
	if sc.Scan() {
		_ = sc.Text()
	}
	for sc.Scan() {
		// Columns: 0=IP 1=HWtype 2=Flags 3=HWaddress 4=Mask 5=Device.
		fields := strings.Fields(sc.Text())
		if len(fields) < 4 || !complete(fields[2]) {
			continue
		}
		mac, err := NormalizeMAC(fields[3])
		if err != nil {
			continue
		}
		ip := net.ParseIP(fields[0])
		if ip == nil {
			continue
		}
		// The first entry wins: a MAC reachable on two interfaces is still one
		// device, and the earlier row is the one Lookup has always returned.
		if _, seen := table[mac]; !seen {
			table[mac] = ip
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("resolver: read arp table: %w", err)
	}
	return table, nil
}

// complete reports whether an ARP flags column marks a resolved entry
// (ATF_COM, 0x2). An unparseable value is treated as complete, which is how
// this file behaved before flags were read at all.
func complete(flags string) bool {
	value, err := strconv.ParseUint(strings.TrimPrefix(flags, "0x"), 16, 32)
	if err != nil {
		return true
	}
	const atfCom = 0x2
	return value&atfCom != 0
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

// FormatMAC renders a MAC in the conventional colon-separated form for humans —
// config files and the UI. Comparison still goes through NormalizeMAC; this is
// presentation only. An unparseable input is returned unchanged, so a display
// path can never lose the raw value it was given.
func FormatMAC(mac string) string {
	norm, err := NormalizeMAC(mac)
	if err != nil {
		return mac
	}
	var sb strings.Builder
	sb.Grow(17)
	for i := 0; i < len(norm); i += 2 {
		if i > 0 {
			sb.WriteByte(':')
		}
		sb.WriteString(norm[i : i+2])
	}
	return sb.String()
}
