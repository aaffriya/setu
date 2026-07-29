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

import "errors"

// Capability identifiers reported by Device.Capabilities and sent to the
// frontend so it knows which controls to render for a device. Keep these
// constants in sync with the capability interfaces below.
const (
	CapSwitch     = "switch"
	CapBrightness = "brightness"
	CapColor      = "color"
	CapColorTemp  = "color_temp"
	CapScene      = "scene"
	CapSpeed      = "speed"
	CapSleep      = "sleep"
	CapTimer      = "timer"
	CapLight      = "light"
	CapVolume     = "volume"
	CapKey        = "key"
	CapKeyHold    = "key_hold"
	CapApp        = "app"
	CapText       = "text"
	CapWoL        = "wol"
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
	// ColorTemp is the white color temperature in Kelvin (meaningful for
	// ColorTempControl devices); 0 when the device isn't in white mode.
	ColorTemp int `json:"color_temp"`
	// Scene is the active preset id (meaningful for SceneControl devices); 0
	// when no scene is active.
	Scene int `json:"scene"`
	// SceneSpeed is the animation speed of a dynamic scene (meaningful for
	// SceneControl devices); 0 when not reported.
	SceneSpeed int `json:"scene_speed"`
	// Speed is the current discrete step (meaningful for SpeedControl devices,
	// e.g. a fan's 1–6); 0 when not reported. Distinct from Brightness, which a
	// fan uses for its light.
	Speed int `json:"speed"`
	// Sleep reports whether sleep/night mode is active (meaningful for SleepMode
	// devices).
	Sleep bool `json:"sleep"`
	// Light reports whether a secondary light is lit (meaningful for LightSwitch
	// devices). Distinct from On, which is the device's own power.
	Light bool `json:"light"`
	// TimerHours is the running auto-off timer in hours (meaningful for
	// TimerControl devices); 0 when no timer is set. TimerElapsedMins is how far
	// into it the device has run, when the protocol reports it.
	TimerHours       int `json:"timer_hours"`
	TimerElapsedMins int `json:"timer_elapsed_mins"`
	// Volume is 0–100 (meaningful for VolumeSetter devices). For a TV this is
	// the real level read back over UPnP (RenderingControl GetVolume).
	Volume int `json:"volume"`
	// Muted reports whether audio is muted (meaningful for Volume devices whose
	// protocol can read it back, e.g. a TV over UPnP GetMute).
	Muted bool `json:"muted"`
	// TextActive reports whether a text-input field is currently focused on the
	// device, and TextValue its live contents (meaningful for TextInput devices
	// whose protocol reports it, e.g. a TV's IME events).
	TextActive bool   `json:"text_active"`
	TextValue  string `json:"text_value"`
}

// Device is the minimal contract every device implementation must satisfy. It
// exposes stable identity/metadata plus a cached State snapshot. Behaviour
// (turning on, dimming, …) lives in the capability interfaces below, which a
// device implements only for the features its hardware actually has.
//
// Three words describe a device, each with exactly one job:
//
//   - Brand is the vendor, in the one spelling that brand package chose.
//   - Driver is which code drives it. With Brand it is identity: the pair picks
//     a constructor out of the factory. It is a key, not prose, and is never
//     shown to a user — the factory carries a human category and label for each
//     pair.
//   - Model is the hardware itself, as the device reported it or as the user
//     typed it. It is presentation only: nothing branches on it, and it is
//     empty whenever nothing has said what the hardware is.
type Device interface {
	ID() string             // stable, unique instance id (from config)
	Name() string           // human-friendly label
	Brand() string          // vendor, e.g. "Samsung"
	Driver() string         // driver key within the brand, e.g. "tizen"
	Model() string          // the hardware, e.g. "UE43AU7700"; "" when unknown
	MAC() string            // primary identity; IP is resolved at runtime
	Capabilities() []string // e.g. ["switch","brightness","color"]
	State() State           // cheap, cached snapshot (must not do I/O)
}

// Closer is implemented by devices that hold something open between calls (for
// example a TV's event socket). The manager closes such a device when it is
// removed or rebuilt, and when Setu shuts down. A device with nothing to
// release simply does not implement it.
type Closer interface {
	Close() error
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

// WakeOnLAN is implemented by devices woken with a Wake-on-LAN magic packet
// (e.g. a PC or NAS). It is fire-and-forget: there is no readable state, so such
// a device implements neither Switchable nor Pollable — Wake just broadcasts the
// packet to the device's MAC.
type WakeOnLAN interface {
	Wake() error
}

// ColorControl is implemented by devices with an adjustable RGB color.
type ColorControl interface {
	SetColor(c Color) error
}

// ColorTempControl is implemented by tunable-white devices: set the white color
// temperature in Kelvin and report the hardware's supported range so clients do
// not offer values the device will only clamp or ignore. On many bulbs RGB
// color and white temperature are mutually exclusive modes.
type ColorTempControl interface {
	SetColorTemp(kelvin int) error
	ColorTempRange() (minKelvin, maxKelvin int)
}

// Scene is a named preset a device can activate. Dynamic marks animated scenes
// whose animation speed can be adjusted (SetSceneSpeed); static scenes ignore
// speed, and the UI only shows a speed control for dynamic ones.
type Scene struct {
	ID      int    `json:"id"`
	Name    string `json:"name"`
	Dynamic bool   `json:"dynamic"`
}

// SceneControl is implemented by devices with named built-in scenes. Scenes
// lists what's available (so the UI can render a picker); SetScene activates one
// by id; SetSceneSpeed adjusts the animation speed of dynamic scenes (devices
// without an adjustable speed may no-op it).
type SceneControl interface {
	Scenes() []Scene
	SetScene(id int) error
	SetSceneSpeed(speed int) error
}

// SpeedControl is implemented by devices with discrete speed steps — a fan,
// where "half speed" is not a thing the hardware can do. It reports its own
// range for the same reason ColorTempControl does: so clients never offer a
// step the device would only clamp or ignore. Steps are contiguous ints.
type SpeedControl interface {
	SetSpeed(step int) error
	SpeedRange() (min, max int)
}

// SleepMode is implemented by devices with a sleep/night mode — a single toggle
// that is independent of power, so it cannot ride on Switchable.
type SleepMode interface {
	SetSleep(on bool) error
}

// LightSwitch is implemented by devices with a secondary light that only
// switches on and off — a ceiling fan's lamp. Switchable is already spoken for
// by the device's main power, and a light with no levels is not Dimmable, so
// this is its own small capability. A light that dims uses Dimmable instead.
type LightSwitch interface {
	SetLight(on bool) error
}

// TimerControl is implemented by devices with a built-in auto-off timer.
// TimerOptions lists the hour values the hardware accepts, always including 0
// (cancel); SetTimer takes hours from that list, not a vendor index — the
// driver owns any translation to the wire.
type TimerControl interface {
	SetTimer(hours int) error
	TimerOptions() []int
}

// Volume is implemented by devices with relative volume control (e.g. a TV).
// ToggleMute flips mute; implementations that can read the real mute state
// keep State.Muted current.
type Volume interface {
	VolumeUp() error
	VolumeDown() error
	ToggleMute() error
}

// VolumeSetter is implemented by Volume devices that also accept an absolute
// level (0–100), so the UI can show a position slider (e.g. a TV over UPnP
// RenderingControl SetVolume; State.Volume reflects the level read back).
type VolumeSetter interface {
	SetVolume(pct int) error
}

// KeyControl is implemented by devices that accept named remote-control keys
// (e.g. a TV's "KEY_HOME", "KEY_UP"). It is the generic seam for D-pad,
// navigation, and media keys without inventing one capability per button.
type KeyControl interface {
	SendKey(key string) error
}

// KeyHold is implemented by KeyControl devices whose protocol distinguishes
// pressing a key from releasing it (e.g. holding a D-pad key to fast-scroll).
// Safety contract: every press MUST eventually be released — on a Samsung TV a
// stuck Press freezes the whole remote channel — so implementations guarantee
// the release themselves (watchdog timer, release-before-next-key), never
// trusting the client to send ReleaseKey.
type KeyHold interface {
	PressKey(key string) error
	ReleaseKey(key string) error
}

// TextInput is implemented by devices that accept typed text into a focused
// on-device input field (e.g. a TV search box). State.TextActive/TextValue
// mirror the device-side field when the protocol reports it (a TV's IME
// events), so the UI can show when the device is ready for text.
type TextInput interface {
	SendText(text string) error
}

// App is a launchable application on a device (e.g. a TV streaming app). ID is
// the platform's launch identifier; Name is the human-friendly label the UI
// shows on the shortcut button.
type App struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// AppControl is implemented by devices that can launch named applications (e.g.
// a smart TV's streaming apps) over the platform's app-launch transport. This
// is distinct from KeyControl: apps are launched by id, not pressed as a key.
// Apps lists the launchable set (so the UI can render shortcut buttons);
// LaunchApp opens one by id.
type AppControl interface {
	Apps() []App
	LaunchApp(id string) error
}

// Pollable is implemented by devices whose current state can be re-read from
// the hardware. It is an internal refresh mechanism used by the state poller
// (see internal/manager) to detect out-of-band changes — e.g. someone flipping
// a physical switch — and is deliberately NOT a user-facing capability, so it
// is not reported by Capabilities. Devices that can't be polled simply omit it.
//
// ErrPollNoResponse may be wrapped when a driver has a meaningful fallback
// state (for example, a TV that remains wakeable by MAC) but received no live
// reply. The manager retains that state while diagnostics still report the
// failed contact.
var ErrPollNoResponse = errors.New("device did not respond")

type Pollable interface {
	// Poll queries the device for its current state and returns it. The
	// implementation should also refresh its own cached State so that State()
	// stays consistent.
	Poll() (State, error)
}

// ReachabilityReporter is implemented by pollable devices that can drive
// offline/recovery automations. Pollable alone is not enough: most drivers'
// State.Online already means live hardware contact succeeded, which is
// sufficient by itself, but a driver whose Online carries a different meaning
// — a TV, which remains controllable by MAC (Wake-on-LAN) even when it did
// not answer the poll — must additionally implement LiveReachability to
// supply that missing signal; see LiveOnline.
//
// Reachability-based automations deliberately require this explicit opt-in so
// a newly added driver cannot silently offer rules its state cannot support.
type ReachabilityReporter interface {
	ReportsReachability() bool
}

// ReportsReachability says whether d can drive offline/recovery automations.
func ReportsReachability(d Device) bool {
	_, pollable := d.(Pollable)
	reporter, reports := d.(ReachabilityReporter)
	return pollable && reports && reporter.ReportsReachability()
}

// LiveReachability is implemented by a ReachabilityReporter whose State.Online
// does not itself mean live contact succeeded — for example a TV, which stays
// Online for Wake-on-LAN control (see samsung.TV.Poll) regardless of whether
// the last poll actually reached it. Such a device reports the true
// live-contact result here instead. A device without this interface doesn't
// need it: its Online already carries that meaning.
//
// Like State(), Reachable must return a cheap, already-cached value and must
// not do I/O: LiveOnline below is called from inside the automation engine's
// locked sections, and a blocking Reachable would stall it.
type LiveReachability interface {
	Reachable() bool
}

// LiveOnline reports whether d made live contact on its last poll, for
// offline/recovery automations to key off. It defers to LiveReachability when
// d both implements it and opts in via ReportsReachability — the same
// explicit-opt-in gate validateTrigger enforces, so a device can never be
// trusted here that automations were never allowed to target — and to
// state.Online otherwise.
func LiveOnline(d Device, state State) bool {
	if lr, ok := d.(LiveReachability); ok && ReportsReachability(d) {
		return lr.Reachable()
	}
	return state.Online
}
