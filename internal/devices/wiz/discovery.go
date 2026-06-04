package wiz

import (
	"encoding/json"
	"fmt"
	"net"
	"syscall"
	"time"

	"setu/internal/resolver"
)

// Discoverer resolves a WiZ bulb's current IP by UDP broadcast: it sends a
// getPilot to the broadcast address; every WiZ bulb on the segment replies, and
// the reply that carries the wanted MAC came from the bulb's IP (the UDP source
// address). This implements resolver.Resolver — the per-brand discovery seam
// the resolver package documents — and works even when the host's ARP table
// doesn't yet know the bulb (e.g. right after boot, or on a non-Linux host).
type Discoverer struct {
	timeout time.Duration
}

// NewDiscoverer returns a Discoverer with a sensible reply window.
func NewDiscoverer() *Discoverer {
	return &Discoverer{timeout: 1500 * time.Millisecond}
}

// Lookup broadcasts a getPilot and returns the IP of the bulb whose MAC matches.
func (d *Discoverer) Lookup(mac string) (net.IP, error) {
	want, err := resolver.NormalizeMAC(mac)
	if err != nil {
		return nil, err
	}

	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		return nil, fmt.Errorf("wiz discovery: listen: %w", err)
	}
	defer conn.Close()
	if err := enableBroadcast(conn); err != nil {
		return nil, fmt.Errorf("wiz discovery: %w", err)
	}

	msg := []byte(`{"method":"getPilot","params":{}}`)
	if _, err := conn.WriteToUDP(msg, &net.UDPAddr{IP: net.IPv4bcast, Port: port}); err != nil {
		return nil, fmt.Errorf("wiz discovery: send: %w", err)
	}

	_ = conn.SetReadDeadline(time.Now().Add(d.timeout))
	buf := make([]byte, 2048)
	for {
		n, addr, err := conn.ReadFromUDP(buf)
		if err != nil {
			break // read deadline reached
		}
		var resp pilotResponse
		if json.Unmarshal(buf[:n], &resp) != nil || resp.Result == nil {
			continue
		}
		if got, err := resolver.NormalizeMAC(resp.Result.Mac); err == nil && got == want {
			return addr.IP, nil
		}
	}
	return nil, fmt.Errorf("wiz discovery: no bulb with mac %s replied", want)
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
