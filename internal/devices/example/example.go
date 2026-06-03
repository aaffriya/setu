// Package example is a TEMPLATE that shows exactly how to add a real device to
// Setu. It compiles and registers like a real brand, but implements no actual
// wire protocol — every network call is a documented stub. Copy this package to
// internal/devices/<brand>/, rename it, and fill in the protocol.
//
// The pattern it demonstrates:
//
//   - a brand "base" struct holding identity + the brand's transport, embedded
//     into each model (composition, not inheritance — principle 2);
//   - one exported type per model (here Bulb), implementing only the capability
//     interfaces that model supports;
//   - runtime MAC→IP resolution with caching and re-resolution on failure
//     (principle 5);
//   - state changes published to the event bus (principle 6);
//   - a Constructor matching config.Constructor and a Register function that
//     wires (brand, model) pairs into the factory.
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

// Brand and model identifiers. These are the exact strings used in config.yaml
// (brand:/model:) and in the factory registration below.
const (
	Brand     = "example"
	ModelBulb = "bulb"
)

// base is the shared brand foundation embedded by every model of this brand. A
// real brand puts its transport here — e.g. a *net.UDPConn for WiZ, or an HTTP
// client + token for a LAN API. It also owns identity metadata, the cached
// resolved IP, and the cached device State.
//
// All Device-metadata methods that are identical across models hang off base, so
// each model gets them for free by embedding base. Methods that differ per model
// (Model, Capabilities, and the capability methods) live on the model type.
type base struct {
	id      string
	name    string
	mac     string
	ipHint  string // optional config hint/fallback
	resolve resolver.Resolver
	bus     *events.Bus

	mu    sync.Mutex
	ip    net.IP       // cached resolved IP (nil until first resolve)
	state device.State // cached current state
}

// --- Device metadata shared by all models via embedding ---

func (b *base) ID() string    { return b.id }
func (b *base) Name() string  { return b.name }
func (b *base) Brand() string { return Brand }
func (b *base) MAC() string   { return b.mac }

// State returns the cached state. It must not do I/O (the poller refreshes it).
func (b *base) State() device.State {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}

// resolveIP returns the device's current IP, using the cached value if present
// and otherwise resolving it from the MAC via the Resolver. This is the
// MAC-primary addressing pattern every real device should follow.
func (b *base) resolveIP() (net.IP, error) {
	b.mu.Lock()
	cached := b.ip
	b.mu.Unlock()
	if cached != nil {
		return cached, nil
	}

	ip, err := b.resolve.Lookup(b.mac)
	if err != nil {
		// Fall back to the optional config hint if resolution fails.
		if b.ipHint != "" {
			if hint := net.ParseIP(b.ipHint); hint != nil {
				b.setIP(hint)
				return hint, nil
			}
		}
		return nil, fmt.Errorf("%s: resolve %s: %w", b.id, b.mac, err)
	}
	b.setIP(ip)
	return ip, nil
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

// Model and Capabilities live on the model type because they vary per model.
func (b *Bulb) Model() string { return ModelBulb }

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
		id:      spec.ID,
		name:    spec.Name,
		mac:     spec.MAC,
		ipHint:  spec.IP,
		resolve: deps.Resolver,
		bus:     deps.Bus,
		// Optimistic initial state; the first send/poll reconciles it.
		state: device.State{Online: true},
	}}
	return b, nil
}

// Register wires this brand's (brand, model) pairs into the factory. The
// composition root (cmd/setu/main.go) calls this once — that single call is the
// "register one factory line" step when adding a device.
func Register(f *config.Factory) {
	f.Register(Brand, ModelBulb, New)
	// A second model of the same brand would be added here, e.g.:
	//   f.Register(Brand, ModelPlug, NewPlug)
}

// ---------------------------------------------------------------------------
// CHECKLIST — adding a real device, by brand and model:
//
//  1. Copy this package to internal/devices/<brand>/ and set Brand/Model consts.
//  2. Put the brand's transport (UDP/TCP/HTTP client) in `base` and implement
//     `send` (and, for discovery brands, a resolver.Resolver).
//  3. For each model, define a type embedding base and implement Model,
//     Capabilities, and only the capability interfaces it supports. Update the
//     compile-time `var _ = ...` assertions to match.
//  4. Implement Poll to read real hardware state (or omit Pollable entirely).
//  5. Export New (a config.Constructor) and a Register(*config.Factory).
//  6. Call <brand>.Register(factory) once in cmd/setu/main.go.
//  7. Add a device entry to config.yaml (brand, model, id, name, mac).
// ---------------------------------------------------------------------------
