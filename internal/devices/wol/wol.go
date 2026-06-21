// Package wol is the Wake-on-LAN brand: the simplest possible device. A wol
// device is just a MAC address with one action — Wake — which broadcasts a
// magic packet to power the machine on (a PC, NAS, router…).
//
// Unlike a real brand it embeds no base and needs no transport: there is no IP
// to resolve (WoL is a layer-2 broadcast), no state to cache or poll, and no
// events to publish (waking is fire-and-forget — we can't confirm the machine
// actually came up). So it implements only Device + WakeOnLAN.
package wol

import (
	"fmt"

	"setu/internal/config"
	"setu/internal/device"
	// Aliased because this brand package is itself named "wol"; wolnet is the
	// shared magic-packet sender (internal/wol), used here and by other brands.
	wolnet "setu/internal/wol"
)

// Brand and model identifiers — the exact strings used in config.yaml.
const (
	Brand       = "wol"
	ModelDevice = "device"
)

// Device is a Wake-on-LAN target: identity plus the MAC to wake.
type Device struct{ id, name, mac string }

var (
	_ device.Device    = (*Device)(nil)
	_ device.WakeOnLAN = (*Device)(nil)
)

func (d *Device) ID() string             { return d.id }
func (d *Device) Name() string           { return d.name }
func (d *Device) Brand() string          { return Brand }
func (d *Device) Model() string          { return ModelDevice }
func (d *Device) MAC() string            { return d.mac }
func (d *Device) Capabilities() []string { return []string{device.CapWoL} }

// State is static: Online stays true so the card never dims and the Wake button
// stays enabled. We can't actually know whether the machine is reachable, and
// the action is always available to fire.
func (d *Device) State() device.State { return device.State{Online: true} }

// Wake broadcasts a Wake-on-LAN magic packet to the device's MAC.
func (d *Device) Wake() error {
	if err := wolnet.Send(d.mac); err != nil {
		return fmt.Errorf("wol %s: %w", d.id, err)
	}
	return nil
}

// New builds a wol device from its config entry. It needs only a MAC; the
// resolver and bus deps are unused (no IP resolution, no state events).
func New(spec config.DeviceSpec, _ config.Deps) (device.Device, error) {
	if spec.MAC == "" {
		return nil, fmt.Errorf("wol %s: mac is required", spec.ID)
	}
	return &Device{id: spec.ID, name: spec.Name, mac: spec.MAC}, nil
}

// Register wires the (brand, model) pair into the factory.
func Register(f *config.Factory) {
	f.Register(Brand, ModelDevice, New)
}
