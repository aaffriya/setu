// Package device defines the core device abstractions for Setu: the Device
// interface, the small capability interfaces that implementations opt into,
// and the value types (State, Color) that flow across the API and event bus.
//
// Capabilities are modelled as separate, single-concern interfaces rather than
// one fat interface. A concrete device (see internal/devices/example) embeds a
// brand base and implements Device plus whichever capability interfaces its
// hardware supports. The API layer discovers what a device can do with type
// assertions (e.g. dev.(Dimmable)), so adding a new capability never forces a
// change to devices that don't have it. This is principle 3 in practice:
// interfaces only at the seams that actually vary.
package device

// Capability identifiers reported by Device.Capabilities and sent to the
// frontend so it knows which controls to render for a device. Keep these
// constants in sync with the capability interfaces below.
const (
	CapSwitch     = "switch"
	CapBrightness = "brightness"
	CapColor      = "color"
)

// Color is a 24-bit RGB color; each channel is 0–255.
type Color struct {
	R uint8 `json:"r"`
	G uint8 `json:"g"`
	B uint8 `json:"b"`
}

// State is a snapshot of a device's current, user-visible condition. It is
// returned by Device.State, carried on events.StateChanged, and serialized
// directly to JSON for the API and WebSocket. Fields that don't apply to a
// given device (e.g. Color on a plain switch) simply keep their zero value.
type State struct {
	// Online reports whether Setu can currently reach the device.
	Online bool `json:"online"`
	// On reports power state (meaningful for Switchable devices).
	On bool `json:"on"`
	// Brightness is 0–100 (meaningful for Dimmable devices).
	Brightness int `json:"brightness"`
	// Color is the current RGB color (meaningful for ColorControl devices).
	Color Color `json:"color"`
}

// Device is the minimal contract every device implementation must satisfy. It
// exposes stable identity/metadata plus a cached State snapshot. Behaviour
// (turning on, dimming, …) lives in the capability interfaces below, which a
// device implements only for the features its hardware actually has.
type Device interface {
	ID() string             // stable, unique instance id (from config)
	Name() string           // human-friendly label
	Brand() string          // e.g. "wiz"
	Model() string          // e.g. "color_bulb"
	MAC() string            // primary identity; IP is resolved at runtime
	Capabilities() []string // e.g. ["switch","brightness","color"]
	State() State           // cheap, cached snapshot (must not do I/O)
}

// Switchable is implemented by devices that can be powered on and off.
type Switchable interface {
	On() error
	Off() error
}

// Dimmable is implemented by devices with adjustable brightness (0–100).
type Dimmable interface {
	SetBrightness(pct int) error
}

// ColorControl is implemented by devices with an adjustable RGB color.
type ColorControl interface {
	SetColor(c Color) error
}

// Pollable is implemented by devices whose current state can be re-read from
// the hardware. It is an internal refresh mechanism used by the state poller
// (see internal/manager) to detect out-of-band changes — e.g. someone flipping
// a physical switch — and is deliberately NOT a user-facing capability, so it
// is not reported by Capabilities. Devices that can't be polled simply omit it.
type Pollable interface {
	// Poll queries the device for its current state and returns it. The
	// implementation should also refresh its own cached State so that State()
	// stays consistent.
	Poll() (State, error)
}
