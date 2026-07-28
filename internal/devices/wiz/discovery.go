package wiz

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"syscall"
	"time"

	"setu/internal/resolver"
)

// Discoverer finds WiZ bulbs by UDP broadcast. Both of Setu's address seams run
// over the same one-datagram-out, many-datagrams-back exchange:
//
//	Lookup — resolver.Resolver: broadcast getPilot and keep the reply whose MAC
//	         is the wanted one; that reply's source address is the bulb's IP.
//	         Works even when the host's ARP table has never seen the bulb (right
//	         after boot, or on a non-Linux host).
//	Scan   — resolver.Scanner: broadcast getSystemConfig and keep every reply, so
//	         bulbs that have not been added yet can be listed for the user.
type Discoverer struct {
	timeout time.Duration
	bcast   *net.UDPAddr // nil = the real broadcast address (tests point it at loopback)
}

// Probe counts. A bulb drops the odd broadcast datagram — observed on a real
// segment, where one bulb answered barely half of single-probe scans — so the
// request goes out several times inside the same reply window. Lookup needs
// fewer: it stops at the first matching reply, and its caller retries anyway.
const (
	lookupProbes = 2
	scanProbes   = 4
)

// NewDiscoverer returns a Discoverer with a sensible reply window.
func NewDiscoverer() *Discoverer {
	return &Discoverer{timeout: 2 * time.Second}
}

// sysConfigResponse is the getSystemConfig reply. moduleName identifies the
// light engine (e.g. "ESP01_SHRGB1C_31"), which is what tells a colour bulb
// apart from a tunable-white one — getPilot alone cannot.
type sysConfigResponse struct {
	Result *struct {
		Mac        string `json:"mac"`
		ModuleName string `json:"moduleName"`
	} `json:"result,omitempty"`
}

// Lookup broadcasts a getPilot and returns the IP of the bulb whose MAC matches.
func (d *Discoverer) Lookup(mac string) (net.IP, error) {
	want, err := resolver.NormalizeMAC(mac)
	if err != nil {
		return nil, err
	}

	var found net.IP
	err = d.broadcast(context.Background(), `{"method":"getPilot","params":{}}`, lookupProbes, func(ip net.IP, payload []byte) bool {
		var resp pilotResponse
		if json.Unmarshal(payload, &resp) != nil || resp.Result == nil {
			return false
		}
		if got, err := resolver.NormalizeMAC(resp.Result.Mac); err != nil || got != want {
			return false
		}
		found = append(net.IP(nil), ip...)
		return true
	})
	if err != nil {
		return nil, err
	}
	if found == nil {
		return nil, fmt.Errorf("wiz discovery: no bulb with mac %s replied", want)
	}
	return found, nil
}

// Scan implements resolver.Scanner: it lists every WiZ bulb that answers on this
// segment, whether or not it is configured. Replies are de-duplicated by MAC
// because a bulb may answer a broadcast more than once.
func (d *Discoverer) Scan(ctx context.Context) ([]resolver.Candidate, error) {
	var found []resolver.Candidate
	seen := make(map[string]struct{})
	err := d.broadcast(ctx, `{"method":"getSystemConfig","params":{}}`, scanProbes, func(ip net.IP, payload []byte) bool {
		var resp sysConfigResponse
		if json.Unmarshal(payload, &resp) != nil || resp.Result == nil {
			return false
		}
		mac, err := resolver.NormalizeMAC(resp.Result.Mac)
		if err != nil {
			return false
		}
		if _, dup := seen[mac]; dup {
			return false
		}
		seen[mac] = struct{}{}
		found = append(found, resolver.Candidate{
			Brand:  Brand,
			Driver: driverFor(resp.Result.ModuleName),
			Model:  resp.Result.ModuleName,
			MAC:    resolver.FormatMAC(mac),
			IP:     ip.String(),
		})
		return false // keep listening until the window closes
	})
	if err != nil {
		return nil, err
	}
	return found, nil
}

// driverFor maps a WiZ module name to the driver key registered with the
// factory. WiZ encodes the light engine in that string: "RGB" for full-colour
// bulbs, "TW" for tunable white. Anything else (plugs, dimmable-white-only
// bulbs, fans, …) has no driver here, so it returns "" — the candidate is
// reported as found but unsupported rather than driven by something that would
// command it wrongly.
//
// The module name itself travels on as the candidate's Model. It is not a
// marketing name — "ESP10_SOCKET_06" is the only hardware identifier WiZ
// offers — so it is passed through untouched and the user can replace it with
// whatever is printed on their bulb.
func driverFor(moduleName string) string {
	up := strings.ToUpper(moduleName)
	switch {
	case strings.Contains(up, "RGB"):
		return DriverColorBulb
	case strings.Contains(up, "TW"):
		return DriverTunableWhite
	default:
		return ""
	}
}

// broadcast sends the request to every WiZ bulb on the segment — repeated
// `probes` times, spread evenly across the reply window, because UDP loses the
// odd datagram — and hands each reply to visit until visit reports it is done,
// the window closes, or ctx ends. Duplicate replies are the caller's to filter.
func (d *Discoverer) broadcast(ctx context.Context, request string, probes int, visit func(net.IP, []byte) bool) error {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		return fmt.Errorf("wiz discovery: listen: %w", err)
	}
	defer conn.Close()
	if err := enableBroadcast(conn); err != nil {
		return fmt.Errorf("wiz discovery: %w", err)
	}

	target := d.bcast
	if target == nil {
		target = &net.UDPAddr{IP: net.IPv4bcast, Port: port}
	}

	deadline := time.Now().Add(d.timeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	// A cancelled caller must not keep the socket for the rest of the window:
	// an expired deadline unblocks the read immediately.
	defer context.AfterFunc(ctx, func() { _ = conn.SetReadDeadline(time.Now()) })()

	if probes < 1 {
		probes = 1
	}
	slot := d.timeout / time.Duration(probes)
	buf := make([]byte, 2048)
	for probe := 0; probe < probes; probe++ {
		if ctx.Err() != nil {
			return nil // the caller gave up: no further probes
		}
		if _, err := conn.WriteToUDP([]byte(request), target); err != nil {
			if probe == 0 {
				return fmt.Errorf("wiz discovery: send: %w", err)
			}
			return nil // a later probe failing still leaves the earlier replies
		}
		until := time.Now().Add(slot)
		if until.After(deadline) {
			until = deadline
		}
		_ = conn.SetReadDeadline(until)
		for {
			n, addr, err := conn.ReadFromUDP(buf)
			if err != nil {
				break // this probe's slot is over — send the next one
			}
			if visit(addr.IP, buf[:n]) {
				return nil
			}
		}
		if !time.Now().Before(deadline) {
			return nil // window closed, or ctx ended
		}
	}
	return nil
}

// enableBroadcast sets SO_BROADCAST so the socket may send to a broadcast
// address. Implemented via SyscallConn to stay within the standard library;
// the constants exist on both Linux (the deploy target) and macOS (dev).
func enableBroadcast(conn *net.UDPConn) error {
	raw, err := conn.SyscallConn()
	if err != nil {
		return err
	}
	var serr error
	if err := raw.Control(func(fd uintptr) {
		serr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_BROADCAST, 1)
	}); err != nil {
		return err
	}
	return serr
}
