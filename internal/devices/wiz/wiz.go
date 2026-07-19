// Package wiz controls Philips WiZ smart bulbs over their local UDP protocol
// (port 38899, JSON getPilot/setPilot) — no cloud, login, or local key needed.
//
// It is a worked instance of the blueprint in internal/devices/example: a brand
// `base` holding the transport, model types implementing only their hardware
// capabilities, MAC→IP resolution with caching + re-resolution, and a brand
// Resolver (UDP broadcast discovery, see discovery.go) that demonstrates the
// per-brand discovery seam the resolver package documents.
package wiz

import (
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"time"

	"setu/internal/config"
	"setu/internal/device"
	"setu/internal/events"
	"setu/internal/resolver"
)

const (
	Brand             = "WiZ"
	ModelColorBulb    = "color_bulb"
	ModelTunableWhite = "tunable_white"

	port           = 38899
	minDimming     = 10 // WiZ hardware floor; lower values are ignored by the bulb
	defaultTimeout = 2 * time.Second
)

// pilotResult is the WiZ getPilot/setPilot "result" object. Pointers let us tell
// "field absent" (an off bulb omits dimming/color) from a zero value.
type pilotResult struct {
	State   *bool  `json:"state,omitempty"`
	Dimming *int   `json:"dimming,omitempty"`
	R       *int   `json:"r,omitempty"`
	G       *int   `json:"g,omitempty"`
	B       *int   `json:"b,omitempty"`
	Temp    *int   `json:"temp,omitempty"`
	SceneID *int   `json:"sceneId,omitempty"`
	Speed   *int   `json:"speed,omitempty"`
	Mac     string `json:"mac,omitempty"`
	Success *bool  `json:"success,omitempty"`
}

type pilotResponse struct {
	Method string       `json:"method"`
	Result *pilotResult `json:"result,omitempty"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// base is the shared WiZ brand foundation: identity, the resolution strategy,
// and the UDP transport. Models embed it.
type base struct {
	id, name, series, mac string
	arp                   resolver.Resolver // injected fallback (ARP table)
	discoverer            *Discoverer       // brand-specific UDP discovery
	bus                   *events.Bus
	timeout               time.Duration

	mu    sync.Mutex
	ip    net.IP // cached resolved IP (nil until resolved)
	state device.State
}

func (b *base) ID() string     { return b.id }
func (b *base) Name() string   { return b.name }
func (b *base) Brand() string  { return Brand }
func (b *base) MAC() string    { return b.mac }
func (b *base) Series() string { return b.series }

func (b *base) State() device.State {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}

// resolveIP tries, in order: the cached IP, the injected ARP resolver (instant
// when the host knows the device), WiZ UDP broadcast discovery (brand-specific,
// works cross-platform).
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
	if ip, err := b.discoverer.Lookup(b.mac); err == nil {
		b.setIP(ip)
		return ip, nil
	}
	return nil, fmt.Errorf("wiz %s: cannot resolve ip for mac %s", b.id, b.mac)
}

func (b *base) setIP(ip net.IP) { b.mu.Lock(); b.ip = ip; b.mu.Unlock() }
func (b *base) invalidateIP()   { b.mu.Lock(); b.ip = nil; b.mu.Unlock() }

// rpc sends one JSON message to the bulb over UDP and returns the parsed result.
func (b *base) rpc(ip net.IP, method string, params map[string]any) (*pilotResult, error) {
	payload, err := json.Marshal(map[string]any{"method": method, "params": params})
	if err != nil {
		return nil, err
	}
	conn, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: ip, Port: port})
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(b.timeout))
	if _, err := conn.Write(payload); err != nil {
		return nil, err
	}
	buf := make([]byte, 2048)
	n, err := conn.Read(buf)
	if err != nil {
		return nil, err
	}
	var resp pilotResponse
	if err := json.Unmarshal(buf[:n], &resp); err != nil {
		return nil, fmt.Errorf("wiz %s: bad reply: %w", b.id, err)
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("wiz %s: %s", b.id, resp.Error.Message)
	}
	return resp.Result, nil
}

// setPilot resolves the IP, sends the command, and on a network failure clears
// the cached IP so the next call re-resolves (the bulb may have a new lease).
func (b *base) setPilot(params map[string]any) error {
	ip, err := b.resolveIP()
	if err != nil {
		b.applyState(func(s *device.State) { s.Online = false })
		return err
	}
	res, err := b.rpc(ip, "setPilot", params)
	if err != nil {
		b.invalidateIP()
		b.applyState(func(s *device.State) { s.Online = false })
		return err
	}
	if res != nil && res.Success != nil && !*res.Success {
		return fmt.Errorf("wiz %s: bulb rejected setPilot", b.id)
	}
	return nil
}

// applyState mutates the cached state and publishes a StateChanged event (used
// by command methods for immediate UI feedback). updateState mutates quietly —
// the poller is the publisher for polled changes, avoiding double events.
func (b *base) applyState(mutate func(*device.State)) {
	b.mu.Lock()
	mutate(&b.state)
	snap := b.state
	b.mu.Unlock()
	if b.bus != nil {
		b.bus.Publish(events.Event{Type: events.StateChanged, DeviceID: b.id, State: snap})
	}
}

func (b *base) updateState(mutate func(*device.State)) {
	b.mu.Lock()
	mutate(&b.state)
	b.mu.Unlock()
}

// The helpers below keep transport/state behavior shared while each concrete
// model exposes only the capability methods its hardware actually supports.
func (b *base) setPower(on bool) error {
	if err := b.setPilot(map[string]any{"state": on}); err != nil {
		return err
	}
	b.applyState(func(s *device.State) { s.Online = true; s.On = on })
	return nil
}

func (b *base) setBrightness(pct int) error {
	d := pct
	if d < minDimming {
		d = minDimming // WiZ ignores dimming below 10%
	}
	if d > 100 {
		d = 100
	}
	if err := b.setPilot(map[string]any{"state": true, "dimming": d}); err != nil {
		return err
	}
	b.applyState(func(s *device.State) { s.Online = true; s.On = true; s.Brightness = d })
	return nil
}

func (b *base) setColorTemp(kelvin, minimum int) error {
	k := kelvin
	if k < minimum {
		k = minimum
	}
	if k > maxKelvin {
		k = maxKelvin
	}
	// Setting temp puts the bulb in white mode (exclusive with color/scene).
	if err := b.setPilot(map[string]any{"state": true, "temp": k}); err != nil {
		return err
	}
	b.applyState(func(s *device.State) {
		s.Online = true
		s.On = true
		s.ColorTemp = k
		s.Scene = 0
	})
	return nil
}

func (b *base) setScene(supported []device.Scene, id int) error {
	for _, scene := range supported {
		if scene.ID != id {
			continue
		}
		if err := b.setPilot(map[string]any{"state": true, "sceneId": id}); err != nil {
			return err
		}
		b.applyState(func(s *device.State) {
			s.Online = true
			s.On = true
			s.Scene = id
		})
		return nil
	}
	return fmt.Errorf("wiz %s: scene %d is not supported by this model", b.id, id)
}

func (b *base) setSceneSpeed(speed int) error {
	sp := speed
	if sp < minSpeed {
		sp = minSpeed
	}
	if sp > maxSpeed {
		sp = maxSpeed
	}
	if err := b.setPilot(map[string]any{"speed": sp}); err != nil {
		return err
	}
	b.applyState(func(s *device.State) {
		s.Online = true
		s.SceneSpeed = sp
	})
	return nil
}

func (b *base) poll() (device.State, error) {
	ip, err := b.resolveIP()
	if err != nil {
		b.updateState(func(s *device.State) { s.Online = false })
		return b.State(), err
	}
	res, err := b.rpc(ip, "getPilot", map[string]any{})
	if err != nil {
		b.invalidateIP()
		b.updateState(func(s *device.State) { s.Online = false })
		return b.State(), err
	}
	b.updateState(func(s *device.State) {
		s.Online = true
		if res != nil {
			if res.State != nil {
				s.On = *res.State
			}
			if res.Dimming != nil {
				s.Brightness = *res.Dimming
			}
			if res.R != nil && res.G != nil && res.B != nil {
				s.Color = device.Color{R: clampByte(*res.R), G: clampByte(*res.G), B: clampByte(*res.B)}
			}
			// temp / sceneId are reset each poll so the UI can tell which mode is
			// active (0 = not in that mode). The bulb reports sceneId even when 0.
			s.ColorTemp = 0
			if res.Temp != nil {
				s.ColorTemp = *res.Temp
			}
			s.Scene = 0
			if res.SceneID != nil {
				s.Scene = *res.SceneID
			}
			s.SceneSpeed = 0
			if res.Speed != nil {
				s.SceneSpeed = *res.Speed
			}
		}
	})
	return b.State(), nil
}

// ---------------------------------------------------------------------------
// ColorBulb: a WiZ RGB + tunable-white bulb.
// ---------------------------------------------------------------------------

type ColorBulb struct {
	base
}

var (
	_ device.Device           = (*ColorBulb)(nil)
	_ device.Switchable       = (*ColorBulb)(nil)
	_ device.Dimmable         = (*ColorBulb)(nil)
	_ device.ColorControl     = (*ColorBulb)(nil)
	_ device.ColorTempControl = (*ColorBulb)(nil)
	_ device.SceneControl     = (*ColorBulb)(nil)
	_ device.Pollable         = (*ColorBulb)(nil)
)

func (b *ColorBulb) Model() string { return ModelColorBulb }

func (b *ColorBulb) Capabilities() []string {
	return []string{
		device.CapSwitch, device.CapBrightness,
		device.CapColor, device.CapColorTemp, device.CapScene,
	}
}

func (b *ColorBulb) On() error                   { return b.setPower(true) }
func (b *ColorBulb) Off() error                  { return b.setPower(false) }
func (b *ColorBulb) SetBrightness(pct int) error { return b.base.setBrightness(pct) }

func (b *ColorBulb) SetColor(c device.Color) error {
	// Setting r,g,b puts the bulb in color mode — mutually exclusive with white
	// temperature and scenes, so clear those in the local state too.
	if err := b.setPilot(map[string]any{"state": true, "r": c.R, "g": c.G, "b": c.B}); err != nil {
		return err
	}
	b.applyState(func(s *device.State) {
		s.Online = true
		s.On = true
		s.Color = c
		s.ColorTemp = 0
		s.Scene = 0
	})
	return nil
}

// --- ColorTempControl (tunable white) ---

func (b *ColorBulb) SetColorTemp(kelvin int) error { return b.base.setColorTemp(kelvin, minKelvin) }

func (b *ColorBulb) ColorTempRange() (int, int) { return minKelvin, maxKelvin }

// --- SceneControl (predefined scenes) ---

func (b *ColorBulb) Scenes() []device.Scene { return scenes }

func (b *ColorBulb) SetScene(id int) error { return b.base.setScene(scenes, id) }

// SetSceneSpeed sets the animation speed (10–200) of the active dynamic scene.
func (b *ColorBulb) SetSceneSpeed(speed int) error { return b.base.setSceneSpeed(speed) }

// Poll reads live state via getPilot and updates the cached state quietly (the
// state poller publishes any change).
func (b *ColorBulb) Poll() (device.State, error) { return b.base.poll() }

// ---------------------------------------------------------------------------
// TunableWhiteBulb: warm-to-cool white only. It deliberately does not satisfy
// device.ColorControl, so the frontend never renders an RGB picker.
// ---------------------------------------------------------------------------

type TunableWhiteBulb struct {
	base
}

var (
	_ device.Device           = (*TunableWhiteBulb)(nil)
	_ device.Switchable       = (*TunableWhiteBulb)(nil)
	_ device.Dimmable         = (*TunableWhiteBulb)(nil)
	_ device.ColorTempControl = (*TunableWhiteBulb)(nil)
	_ device.SceneControl     = (*TunableWhiteBulb)(nil)
	_ device.Pollable         = (*TunableWhiteBulb)(nil)
)

func (b *TunableWhiteBulb) Model() string { return ModelTunableWhite }

func (b *TunableWhiteBulb) Capabilities() []string {
	return []string{
		device.CapSwitch, device.CapBrightness,
		device.CapColorTemp, device.CapScene,
	}
}

func (b *TunableWhiteBulb) On() error  { return b.setPower(true) }
func (b *TunableWhiteBulb) Off() error { return b.setPower(false) }

func (b *TunableWhiteBulb) SetBrightness(pct int) error { return b.base.setBrightness(pct) }
func (b *TunableWhiteBulb) SetColorTemp(kelvin int) error {
	return b.base.setColorTemp(kelvin, tunableWhiteMinKelvin)
}

func (b *TunableWhiteBulb) ColorTempRange() (int, int) {
	return tunableWhiteMinKelvin, maxKelvin
}

func (b *TunableWhiteBulb) Scenes() []device.Scene { return tunableWhiteScenes }

func (b *TunableWhiteBulb) SetScene(id int) error { return b.base.setScene(tunableWhiteScenes, id) }

// All exposed tunable-white scenes are static, so speed has no effect.
func (b *TunableWhiteBulb) SetSceneSpeed(int) error { return nil }

func (b *TunableWhiteBulb) Poll() (device.State, error) { return b.base.poll() }

func clampByte(v int) uint8 {
	switch {
	case v < 0:
		return 0
	case v > 255:
		return 255
	default:
		return uint8(v)
	}
}

func newBase(spec config.DeviceSpec, deps config.Deps) base {
	return base{
		id:         spec.ID,
		name:       spec.Name,
		series:     spec.Series,
		mac:        spec.MAC,
		arp:        deps.Resolver,
		discoverer: NewDiscoverer(),
		bus:        deps.Bus,
		timeout:    defaultTimeout,
		// State is unknown until the first poll fills it in.
	}
}

// New builds a WiZ ColorBulb from its config entry (matches config.Constructor).
func New(spec config.DeviceSpec, deps config.Deps) (device.Device, error) {
	return &ColorBulb{base: newBase(spec, deps)}, nil
}

// NewTunableWhite builds a warm-to-cool-white WiZ bulb.
func NewTunableWhite(spec config.DeviceSpec, deps config.Deps) (device.Device, error) {
	return &TunableWhiteBulb{base: newBase(spec, deps)}, nil
}

// Register wires WiZ models into the factory (called from cmd/setu/main.go).
func Register(f *config.Factory) {
	f.Register(Brand, ModelColorBulb, New)
	f.Register(Brand, ModelTunableWhite, NewTunableWhite)
}
