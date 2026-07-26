package example

import (
	"context"
	"net"

	"setu/internal/resolver"
)

// The seam, both ways round: where a configured MAC is now, and what is out
// there that is not configured yet.
var (
	_ resolver.Resolver = (*Discoverer)(nil)
	_ resolver.Scanner  = (*Discoverer)(nil)
)

// Discoverer is this brand's own address-resolution strategy — the second of
// Setu's three seams (see internal/resolver). Every real brand needs one,
// because the injected ARP resolver only knows devices the host has talked to
// recently, and it reads /proc/net/arp, which does not exist off Linux.
//
// A real implementation asks the network and matches the reply against the
// wanted MAC, for example:
//
//	WiZ     — UDP broadcast getPilot; the reply carrying the MAC came from the
//	          bulb's IP (see internal/devices/wiz/discovery.go).
//	Samsung — SSDP M-SEARCH, then fetch each responder's description and compare
//	          its MAC (see internal/devices/samsung/discovery.go).
//
// The template has no protocol to broadcast, so it answers with loopback. That
// keeps the blueprint runnable anywhere — add an `example` device to a config
// and its commands actually succeed, which is what makes this package usable as
// a stand-in device while developing the UI or the API.
type Discoverer struct{}

// NewDiscoverer returns the template's stub discoverer.
func NewDiscoverer() *Discoverer { return &Discoverer{} }

// Lookup validates the MAC and returns the address to talk to. A real brand
// returns the address it actually discovered, and an error when nothing on the
// network claimed that MAC — never a guess, or the driver would happily command
// the wrong device.
func (d *Discoverer) Lookup(mac string) (net.IP, error) {
	if _, err := resolver.NormalizeMAC(mac); err != nil {
		return nil, err
	}
	return net.IPv4(127, 0, 0, 1), nil
}

// Scan is the same seam used the other way round: Lookup answers "where is this
// MAC?" for a configured device, Scan answers "what is out there?" for one that
// has not been added yet. The API exposes it at POST /api/discovery/scan and
// the UI turns each result into a ready-to-paste config entry.
//
// A real brand reuses the transport it already has — the WiZ discoverer
// broadcasts getSystemConfig and keeps every reply; the Samsung one keeps every
// SSDP responder that answers /api/v2/ — and returns one Candidate per device,
// with Model set to the key it registered with the factory, or "" when the
// reply names hardware this package has no driver for. Never invent a device
// you did not hear from, and never guess a model.
//
// The template has no protocol to broadcast, so it finds nothing.
func (d *Discoverer) Scan(context.Context) ([]resolver.Candidate, error) {
	return nil, nil
}
