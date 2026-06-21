// Package wol sends Wake-on-LAN magic packets. A magic packet is 6 bytes of
// 0xff followed by the target MAC repeated 16 times, broadcast over UDP so any
// host on the LAN segment that is listening can wake.
//
// It is shared by the "wol" brand (internal/devices/wol) and by brands whose
// devices also support WoL (e.g. Samsung TVs), so the wire format lives in one
// place. Callers add their own identity to the error with %w.
package wol

import (
	"encoding/hex"
	"fmt"
	"net"
	"syscall"

	"setu/internal/resolver"
)

// Send broadcasts a Wake-on-LAN magic packet to mac. It sprays the limited
// broadcast and every interface's directed broadcast (e.g. 192.168.0.255) on the
// two common WoL ports (9 and 7); directed broadcast is more reliable than
// 255.255.255.255 for same-subnet WoL. It returns an error if mac is malformed
// or no broadcast target could be reached.
func Send(mac string) error {
	norm, err := resolver.NormalizeMAC(mac)
	if err != nil {
		return err
	}
	hw, err := hex.DecodeString(norm)
	if err != nil || len(hw) != 6 {
		return fmt.Errorf("bad mac %q", mac)
	}
	packet := make([]byte, 0, 6+16*6)
	for i := 0; i < 6; i++ {
		packet = append(packet, 0xff)
	}
	for i := 0; i < 16; i++ {
		packet = append(packet, hw...)
	}

	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := enableBroadcast(conn); err != nil {
		return err
	}

	sent := 0
	for _, ip := range broadcastIPs() {
		for _, port := range []int{9, 7} {
			if _, err := conn.WriteToUDP(packet, &net.UDPAddr{IP: ip, Port: port}); err == nil {
				sent++
			}
		}
	}
	if sent == 0 {
		return fmt.Errorf("wake-on-lan: no broadcast target reachable")
	}
	return nil
}

// broadcastIPs returns the limited broadcast plus each non-loopback IPv4
// interface's directed broadcast address (host bits set to 1).
func broadcastIPs() []net.IP {
	out := []net.IP{net.IPv4bcast}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return out
	}
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		ip4 := ipnet.IP.To4()
		if ip4 == nil || ip4.IsLoopback() {
			continue
		}
		mask := ipnet.Mask
		if len(mask) == 16 {
			mask = mask[12:]
		}
		if len(mask) != 4 {
			continue
		}
		out = append(out, net.IP{
			ip4[0] | ^mask[0], ip4[1] | ^mask[1], ip4[2] | ^mask[2], ip4[3] | ^mask[3],
		})
	}
	return out
}

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
