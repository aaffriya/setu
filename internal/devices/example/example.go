// Package example is a TEMPLATE that shows exactly how to add a real device to
// Setu. It compiles and registers like a real brand, but implements no actual
// wire protocol — every network call is a documented stub. Copy this package to
// internal/devices/<brand>/, rename it, and fill in the protocol.
//
// The pattern it demonstrates:
//
//   - a brand "base" struct holding identity + the brand's transport, embedded
//     into each driver type (composition, not inheritance — principle 2);
//   - one exported type per driver (here Bulb), implementing only the capability
//     interfaces that hardware supports;
//   - runtime MAC→IP resolution with caching and re-resolution on failure
//     (principle 5);
//   - state changes published to the event bus (principle 6);
//   - a Constructor matching config.Constructor and a Register function that
//     wires (brand, driver) pairs into the factory.
//
// A step-by-step checklist for adding a real device is at the bottom of the file
// (and in the README's "Adding a device" section).
package example

import (
	"fmt"
	"net"
	"sync"

	"setu/internal/config"
	"setu/internal/device"
	"setu/internal/events"
	"setu/internal/resolver"
)

// Brand and driver identifiers. These are the exact strings a stored device
// spec carries and the ones used in the factory registration below. Brand is
// also the vendor name the UI prints, so it is spelled the way the vendor does;
// a driver key never reaches a screen (Register gives it a label for that).
const (
	Brand      = "Example"
	DriverBulb = "bulb"
)

// base is the shared brand foundation embedded by every driver of this brand. A
// real brand puts its transport here — e.g. a *net.UDPConn for WiZ, or an HTTP
// client + token for a LAN API. It also owns identity metadata, the cached
// resolved IP, and the cached device State.
//
// All Device-metadata methods that are identical across drivers hang off base,
// so each driver gets them for free by embedding base. Methods that differ per
// driver (Driver, Capabilities, and the capability methods) live on the driver
// type.
type base struct {
	id         string
	name       string
	model      string
	mac        string
	arp        resolver.Resolver // injected fallback (the host's ARP table)
	discoverer resolver.Resolver // this brand's own discovery (see discovery.go)
	bus        *events.Bus

	mu    sync.Mutex
	ip    net.IP       // cached resolved IP (nil until first resolve)
	state device.State // cached current state
}

// --- Device metadata shared by all models via embedding ---

func (b *base) ID() string    { return b.id }
func (b *base) Name() string  { return b.name }
func (b *base) Brand() string { return Brand }
func (b *base) MAC() string   { return b.mac }

// Model is instance data, not a property of this driver: it is whatever the
// scan read off the hardware or the user typed, and it is empty when nobody has
// said. Nothing may branch on it — Driver is what decides behaviour.
func (b *base) Model() string { return b.model }

// State returns the cached state. It must not do I/O (the poller refreshes it).
func (b *base) State() device.State {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}

// resolveIP returns the device's current IP, trying in order: the cached value,
// the injected ARP resolver (instant when the host has talked to the device
// recently), then this brand's own discovery. This is the MAC-primary addressing
// pattern every real device should follow — and the fallback order matters,
// because ARP alone is empty right after boot and unavailable off Linux.
func (b *base) resolveIP() (net.IP, error) {
	b.mu.Lock()
	cached := b.ip
	b.mu.Unlock()
	if cached != nil {
		return cached, nil
	}
	if b.arp != nil {
		if ip, err := b.arp.Lookup(b.mac); err == nil {
			b.setIP(ip)
			return ip, nil
		}
	}
	if b.discoverer != nil {
		if ip, err := b.discoverer.Lookup(b.mac); err == nil {
			b.setIP(ip)
			return ip, nil
		}
	}
	return nil, fmt.Errorf("%s: cannot resolve ip for mac %s", b.id, b.mac)
}

func (b *base) setIP(ip net.IP) {
	b.mu.Lock()
	b.ip = ip
	b.mu.Unlock()
}

// invalidateIP clears the cached IP so the next resolveIP re-resolves. Call this
// whenever a send/connect fails: the device may have a new DHCP lease.
func (b *base) invalidateIP() {
	b.mu.Lock()
	b.ip = nil
	b.mu.Unlock()
}

// send is where the brand's wire protocol lives. For the template it is a stub.
// A real implementation marshals payload, dials resolveIP(), writes it, and on
// any network error calls invalidateIP() so the next attempt re-resolves.
func (b *base) send(payload any) error {
	ip, err := b.resolveIP()
	if err != nil {
		b.markOffline()
		return err
	}
	_ = ip
	_ = payload
	// TODO(real device): perform the actual protocol exchange here, e.g.
	//
	//   addr := net.JoinHostPort(ip.String(), "38899")
	//   conn, err := net.DialTimeout("udp", addr, 2*time.Second)
	//   if err != nil { b.invalidateIP(); b.markOffline(); return err }
	//   defer conn.Close()
	//   ... write payload, read reply ...
	//
	// On success, mark the device online.
	b.markOnline()
	return nil
}

// applyState mutates the cached state under lock and publishes a StateChanged
// event so the UI updates live. Every capability method funnels through here.
func (b *base) applyState(mutate func(s *device.State)) {
	b.mu.Lock()
	mutate(&b.state)
	snapshot := b.state
	b.mu.Unlock()
	b.publish(snapshot)
}

func (b *base) markOnline()  { b.applyState(func(s *device.State) { s.Online = true }) }
func (b *base) markOffline() { b.applyState(func(s *device.State) { s.Online = false }) }

func (b *base) publish(state device.State) {
	if b.bus == nil {
		return
	}
	b.bus.Publish(events.Event{
		Type:     events.StateChanged,
		DeviceID: b.id,
		State:    state,
	})
}

// ---------------------------------------------------------------------------
// Bulb is one model of this brand: a dimmable color bulb. It supports power,
// brightness, and color, so it implements Switchable, Dimmable, and
// ColorControl. A different model of the same brand (say a plain smart plug)
// would embed the same base but implement only Switchable — that is how
// "different models of the same brand behave differently".
// ---------------------------------------------------------------------------

// Bulb is the example color bulb model.
type Bulb struct {
	base
}

// Compile-time proof that *Bulb satisfies Device and the capabilities it claims.
// Add or remove a line here when a model gains or loses a capability.
var (
	_ device.Device       = (*Bulb)(nil)
	_ device.Switchable   = (*Bulb)(nil)
	_ device.Dimmable     = (*Bulb)(nil)
	_ device.ColorControl = (*Bulb)(nil)
	_ device.Pollable     = (*Bulb)(nil)
)

// Driver and Capabilities live on the driver type because they vary per driver.
func (b *Bulb) Driver() string { return DriverBulb }

func (b *Bulb) Capabilities() []string {
	return []string{device.CapSwitch, device.CapBrightness, device.CapColor}
}

// --- Switchable ---

func (b *Bulb) On() error {
	if err := b.send(map[string]any{"state": true}); err != nil {
		return err
	}
	b.applyState(func(s *device.State) { s.On = true })
	return nil
}

func (b *Bulb) Off() error {
	if err := b.send(map[string]any{"state": false}); err != nil {
		return err
	}
	b.applyState(func(s *device.State) { s.On = false })
	return nil
}

// --- Dimmable ---

func (b *Bulb) SetBrightness(pct int) error {
	if pct < 0 || pct > 100 {
		return fmt.Errorf("%s: brightness %d out of range 0–100", b.id, pct)
	}
	if err := b.send(map[string]any{"dimming": pct}); err != nil {
		return err
	}
	b.applyState(func(s *device.State) {
		s.Brightness = pct
		if pct > 0 {
			s.On = true
		}
	})
	return nil
}

// --- ColorControl ---

func (b *Bulb) SetColor(c device.Color) error {
	if err := b.send(map[string]any{"r": c.R, "g": c.G, "b": c.B}); err != nil {
		return err
	}
	b.applyState(func(s *device.State) { s.Color = c })
	return nil
}

// --- Pollable (internal refresh; see device.Pollable) ---

// Poll re-reads hardware state. The template has no hardware, so it returns the
// cached state (the poller therefore emits no spurious changes). A real
// implementation would query the device and update b.state to match.
//
// IMPORTANT — the one contract that is easy to get wrong: when the read fails
// but the state you return is still meaningful (an unreachable device is
// offline; a TV that is off can still be woken by MAC), wrap
// device.ErrPollNoResponse:
//
//	if err != nil {
//	    b.invalidateIP()
//	    b.markOffline()
//	    return b.State(), fmt.Errorf("%s: %w: %w", b.id, device.ErrPollNoResponse, err)
//	}
//
// The manager DISCARDS the state returned with any other error, so a plain error
// leaves the read model showing the last good state: the device keeps rendering
// as online and controllable until something else corrects it. Return a bare
// error only when the state you produced is genuinely garbage.
func (b *Bulb) Poll() (device.State, error) {
	return b.State(), nil
}

// ---------------------------------------------------------------------------
// Construction & registration
// ---------------------------------------------------------------------------

// New builds a Bulb from its config entry. Its signature matches
// config.Constructor, so it registers with the factory directly.
func New(spec config.DeviceSpec, deps config.Deps) (device.Device, error) {
	if deps.Resolver == nil {
		return nil, fmt.Errorf("%s: resolver is required", spec.ID)
	}
	b := &Bulb{base: base{
		id:         spec.ID,
		name:       spec.Name,
		model:      spec.Model,
		mac:        spec.MAC,
		arp:        deps.Resolver,
		discoverer: NewDiscoverer(),
		bus:        deps.Bus,
		// Optimistic initial state; the first send/poll reconciles it.
		state: device.State{Online: true},
	}}
	return b, nil
}

// Register wires this brand's drivers into the factory. The composition root
// (cmd/setu/main.go) calls this once — that single call is the "register one
// factory line" step when adding a device.
//
// The third argument is the label the UI shows wherever a person picks a driver
// (the manual add form, an unnamed scan result). Write it as the shortest thing
// a user would recognise on the box, not as the key.
func Register(f *config.Factory) {
	f.Register(Brand, DriverBulb, "Bulb", New)
	// A second driver of the same brand would be added here, e.g.:
	//   f.Register(Brand, DriverPlug, "Smart plug", NewPlug)
}

// ---------------------------------------------------------------------------
// CHECKLIST — adding a real device, by brand and model:
//
//  1. Copy this package to internal/devices/<brand>/ and set the Brand/Driver
//     consts.
//  2. Put the brand's transport (UDP/TCP/HTTP client) in `base` and implement
//     `send`; replace discovery.go's stub Lookup with the brand's real discovery,
//     and its Scan if the brand can enumerate its devices (the UI's device scan).
//  3. For each driver, define a type embedding base and implement Driver,
//     Capabilities, and only the capability interfaces it supports. Update the
//     compile-time `var _ = ...` assertions to match.
//  4. Implement Poll to read real hardware state (or omit Pollable entirely).
//     Wrap device.ErrPollNoResponse on a failed read whose state is still
//     meaningful — see the Poll doc comment; getting this wrong makes an
//     unreachable device render as a healthy one.
//  5. Export New (a config.Constructor) and a Register(*config.Factory), giving
//     every driver a human label there.
//  6. Call <brand>.Register(factory) once in cmd/setu/main.go — and add the
//     discoverer to the `scanners` slice there if it implements Scan.
//  7. Add the device from Settings → Devices: a network scan finds it, or it
//     is typed in by hand. Nothing to edit on disk.
// ---------------------------------------------------------------------------
