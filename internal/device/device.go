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
	CapColorTemp  = "color_temp"
	CapScene      = "scene"
	CapVolume     = "volume"
	CapKey        = "key"
	CapApp        = "app"
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
	// Volume is 0–100 (meaningful for VolumeSetter devices). For a TV this is a
	// tracked estimate — the remote channel can't read the real level — kept
	// accurate by re-calibrating whenever the slider is taken fully to 0 or 100.
	Volume int `json:"volume"`
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

// Described is optional presentation metadata: a human-friendly product or
// series name (e.g. "AU7700") distinct from the Model driver key. A device opts
// in by implementing it (like the capability interfaces); the API omits the
// field when absent, so the UI simply falls back to the model.
type Described interface {
	Series() string
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

// ColorTempControl is implemented by tunable-white devices: set the white color
// temperature in Kelvin (e.g. ~2200 warm … 6500 cool). On many bulbs RGB color
// and white temperature are mutually exclusive modes.
type ColorTempControl interface {
	SetColorTemp(kelvin int) error
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

// Volume is implemented by devices with relative volume control (e.g. a TV,
// where the protocol exposes step-up/step-down/mute rather than an absolute
// level). Setu doesn't track an absolute volume value for these.
type Volume interface {
	VolumeUp() error
	VolumeDown() error
	ToggleMute() error
}

// VolumeSetter is implemented by Volume devices that also accept an absolute
// level (0–100), so the UI can show a position slider. A TV has no absolute
// volume on its remote channel, so the implementation tracks a level and steps
// to the target with up/down keys (State.Volume reflects the tracked level).
type VolumeSetter interface {
	SetVolume(pct int) error
}

// KeyControl is implemented by devices that accept named remote-control keys
// (e.g. a TV's "KEY_HOME", "KEY_UP"). It is the generic seam for D-pad,
// navigation, and media keys without inventing one capability per button.
type KeyControl interface {
	SendKey(key string) error
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
type Pollable interface {
	// Poll queries the device for its current state and returns it. The
	// implementation should also refresh its own cached State so that State()
	// stays consistent.
	Poll() (State, error)
}
