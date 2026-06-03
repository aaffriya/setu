// Package resolver maps a device's stable MAC address to its current IP.
//
// IoT devices keep a fixed MAC but their IP can change (DHCP), so Setu treats
// the MAC as the primary identity and resolves the IP at runtime (principle 5).
// The Resolver interface is the seam: the default ARPResolver reads the kernel
// ARP table, and future strategies (DHCP lease tables, per-brand UDP discovery)
// slot in behind the same interface without touching device code.
package resolver

import "net"

// Resolver looks up the current IP address for a MAC address.
type Resolver interface {
	// Lookup returns the current IP for mac, or an error if it cannot be
	// resolved (unknown MAC, empty table, unsupported platform, …).
	Lookup(mac string) (net.IP, error)
}
