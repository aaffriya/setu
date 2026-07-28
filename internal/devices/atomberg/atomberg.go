// Package atomberg controls Atomberg BLDC ceiling fans over their local UDP
// protocol — plain JSON to port 5600, state broadcast back on port 5625, with
// no cloud, login or key. Atomberg documents this LAN protocol themselves; the
// cloud API exists but is capped at 100 calls a day, which cannot support
// polling, so it is deliberately not used.
//
// Two properties shape the driver and make it unlike the other brands:
//
//   - A fan broadcasts a beacon carrying its MAC every second, so address
//     resolution needs no ARP and a DHCP change corrects itself (see
//     discovery in listener.go).
//   - A fan broadcasts its full state after any change, including one made
//     with the physical remote or the vendor app. Those arrive as push events
//     rather than being discovered by the next poll.
//
// Because the beacon port is fixed, one listener is shared by every fan (see
// Discoverer) instead of the per-exchange socket a request/response brand uses.
package atomberg

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
	Brand = "Atomberg"
	// DriverFan is a fan whose light, if it has one, is on/off only.
	DriverFan = "fan"
	// DriverFanLight is a fan with a dimmable light and warm/cool/daylight modes.
	DriverFanLight = "fan_light"

	// Speed steps. Step 6 is the "boost" step on the physical remote.
	minSpeed = 1
	maxSpeed = 6

	// Brightness bounds for the fan's light. The hardware floor is 10%; 0 is
	// not a dim level but the instruction to switch the light off.
	minBrightness  = 10
	fullBrightness = 100

	// pollWait bounds how long a poll waits for the state broadcast its nudge
	// asked for. Replies are same-segment and near-instant; this is headroom.
	pollWait = 2 * time.Second
)

// base is the shared Atomberg foundation: identity, the shared listener that
// provides both addressing and state, and the cached state. Models embed it.
type base struct {
	id, name, model, mac string
	arp                  resolver.Resolver // injected fallback (ARP table)
	listener             *Discoverer       // beacons, state, and addressing
	bus                  *events.Bus
	unwatch              func()

	mu    sync.Mutex
	ip    net.IP // cached resolved IP (nil until resolved)
	state device.State
}

func (b *base) ID() string    { return b.id }
func (b *base) Name() string  { return b.name }
func (b *base) Brand() string { return Brand }
func (b *base) MAC() string   { return b.mac }
func (b *base) Model() string { return b.model }

func (b *base) State() device.State {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}

// Close stops this fan receiving pushed state. The shared listener itself is
// process-wide and stays up for the other fans.
func (b *base) Close() error {
	if b.unwatch != nil {
		b.unwatch()
	}
	return nil
}

// resolveIP prefers the fan's latest fresh beacon, then falls back to the cached
// IP and finally ARP. Checking the beacon before the cache is what lets a DHCP
// change correct itself: UDP writes to an unused old LAN address can still
// succeed locally, so a transport error is not guaranteed to invalidate it.
func (b *base) resolveIP() (net.IP, error) {
	if ip, ok := b.listener.addressOf(watchKey(b.mac)); ok {
		b.setIP(ip)
		return ip, nil
	}
	b.mu.Lock()
	cached := b.ip
	b.mu.Unlock()
	if cached != nil {
		return cached, nil
	}
	// With no usable cache, wait briefly for the first beacon. This keeps a
	// command issued immediately after process start from failing needlessly.
	if ip, err := b.listener.Lookup(b.mac); err == nil {
		b.setIP(ip)
		return ip, nil
	}
	if b.arp != nil {
		if ip, err := b.arp.Lookup(b.mac); err == nil {
			b.setIP(ip)
			return ip, nil
		}
	}
	return nil, fmt.Errorf("atomberg %s: cannot resolve ip for mac %s", b.id, b.mac)
}

func (b *base) setIP(ip net.IP) { b.mu.Lock(); b.ip = ip; b.mu.Unlock() }
func (b *base) invalidateIP()   { b.mu.Lock(); b.ip = nil; b.mu.Unlock() }

// dispatch resolves the fan and puts one command JSON on the wire, touching no
// state. There is no reply to wait for: the protocol is fire-and-forget, and
// the fan's subsequent state broadcast is what confirms the change.
func (b *base) dispatch(cmd map[string]any) error {
	payload, err := json.Marshal(cmd)
	if err != nil {
		return err
	}
	ip, err := b.resolveIP()
	if err != nil {
		return err
	}
	if err := send(ip, payload); err != nil {
		// The lease may have moved; the next attempt re-resolves.
		b.invalidateIP()
		return fmt.Errorf("atomberg %s: send: %w", b.id, err)
	}
	return nil
}

// command is dispatch for a user-initiated change: it publishes, so the UI
// reflects the command immediately, and marks the fan offline when the send
// fails. Poll deliberately uses dispatch instead — the manager publishes polled
// changes itself, so publishing here too would double every poll's events.
func (b *base) command(cmd map[string]any) error {
	if err := b.dispatch(cmd); err != nil {
		b.markOffline()
		return err
	}
	b.applyState(func(s *device.State) { s.Online = true })
	return nil
}

// applyState mutates the cached state and publishes it. Used by commands, so
// the UI reflects a change immediately rather than at the next poll.
func (b *base) applyState(mutate func(s *device.State)) {
	b.mu.Lock()
	mutate(&b.state)
	snapshot := b.state
	b.mu.Unlock()
	b.publish(snapshot)
}

// updateState mutates the cached state quietly. Used by Poll, because the
// manager publishes polled changes itself — publishing here as well would
// double every event.
func (b *base) updateState(mutate func(s *device.State)) {
	b.mu.Lock()
	mutate(&b.state)
	b.mu.Unlock()
}

func (b *base) markOffline() { b.applyState(func(s *device.State) { s.Online = false }) }

func (b *base) publish(state device.State) {
	if b.bus == nil {
		return
	}
	b.bus.Publish(events.Event{Type: events.StateChanged, DeviceID: b.id, State: state})
}

// poll asks the fan to re-broadcast its state and reads the result.
//
// A fan only broadcasts after something changes, so a passive read would sit
// empty forever. {"speedDelta":0} is the documented way out: it is a no-op the
// hardware still answers with a full state broadcast.
func (b *base) poll(light lightSupport) (device.State, error) {
	_, previousRev, _ := b.listener.latest(b.mac)

	if err := b.dispatch(map[string]any{"speedDelta": 0}); err != nil {
		b.updateState(func(s *device.State) { s.Online = false })
		return b.State(), fmt.Errorf("atomberg %s: %w: %w", b.id, device.ErrPollNoResponse, err)
	}

	deadline := time.Now().Add(pollWait)
	for {
		state, rev, ok := b.listener.latest(b.mac)
		if ok && rev != previousRev {
			b.updateState(func(s *device.State) { *s = state.deviceState(light) })
			return b.State(), nil
		}
		if !time.Now().Before(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// No fresh broadcast. A beacon still proves the fan is powered and on the
	// network, so keep the last known state and report it as online; only a fan
	// that has gone quiet entirely is offline. Either way the contact failed, so
	// ErrPollNoResponse is wrapped: the manager keeps the state we return here
	// while diagnostics still record the miss.
	online := b.listener.present(b.mac)
	b.updateState(func(s *device.State) { s.Online = online })
	return b.State(), fmt.Errorf("atomberg %s: %w: no state broadcast", b.id, device.ErrPollNoResponse)
}

// --- shared capability helpers (the wire protocol, once) ---

func (b *base) setPower(on bool) error {
	if err := b.command(map[string]any{"power": on}); err != nil {
		return err
	}
	b.applyState(func(s *device.State) { s.On = on })
	return nil
}

func (b *base) setSpeed(step int) error {
	if step < minSpeed || step > maxSpeed {
		return fmt.Errorf("atomberg %s: speed %d out of range %d–%d", b.id, step, minSpeed, maxSpeed)
	}
	if err := b.command(map[string]any{"speed": step}); err != nil {
		return err
	}
	b.applyState(func(s *device.State) {
		s.Speed = step
		// Setting a speed starts the fan; the hardware powers on to serve it.
		s.On = true
	})
	return nil
}

func (b *base) setSleep(on bool) error {
	if err := b.command(map[string]any{"sleep": on}); err != nil {
		return err
	}
	b.applyState(func(s *device.State) { s.Sleep = on })
	return nil
}

// timerIndex maps the hours a user picks to the index the fan expects. The two
// are NOT the same number — index 4 means six hours — which is the single
// easiest thing to get wrong in this protocol.
var timerIndex = map[int]int{0: 0, 1: 1, 2: 2, 3: 3, 6: 4}

// timerHours is the set of durations the hardware accepts, in order. Kept
// beside timerIndex so the two cannot drift apart.
var timerHours = []int{0, 1, 2, 3, 6}

func (b *base) setTimer(hours int) error {
	index, ok := timerIndex[hours]
	if !ok {
		return fmt.Errorf("atomberg %s: timer %dh is not one of %v", b.id, hours, timerHours)
	}
	if err := b.command(map[string]any{"timer": index}); err != nil {
		return err
	}
	b.applyState(func(s *device.State) {
		s.TimerHours = hours
		s.TimerElapsedMins = 0
	})
	return nil
}

// setLight switches the fan's lamp. This is the whole light control on a model
// that cannot dim.
func (b *base) setLight(on bool) error {
	if err := b.command(map[string]any{"led": on}); err != nil {
		return err
	}
	b.applyState(func(s *device.State) { s.Light = on })
	return nil
}

// setBrightness drives a dimmable light, where 0 means off. Any level turns the
// lamp on first: the fan ignores a brightness change while its light is off.
func (b *base) setBrightness(pct int) error {
	if pct < 0 || pct > fullBrightness {
		return fmt.Errorf("atomberg %s: brightness %d out of range 0–100", b.id, pct)
	}
	if pct == 0 {
		if err := b.command(map[string]any{"led": false}); err != nil {
			return err
		}
		b.applyState(func(s *device.State) { s.Brightness = 0 })
		return nil
	}
	if err := b.command(map[string]any{"led": true}); err != nil {
		return err
	}
	level := max(pct, minBrightness) // the hardware floor
	if err := b.command(map[string]any{"brightness": level}); err != nil {
		return err
	}
	b.applyState(func(s *device.State) { s.Brightness = level })
	return nil
}

// --- light modes as scenes -------------------------------------------------
// The light has three fixed colour modes, not a continuous range, so they are
// exposed as named presets rather than a temperature slider that would only
// snap to three positions.

const (
	sceneWarm = iota + 1
	sceneCool
	sceneDaylight
)

var lightScenes = []device.Scene{
	{ID: sceneWarm, Name: "Warm"},
	{ID: sceneCool, Name: "Cool"},
	{ID: sceneDaylight, Name: "Daylight"},
}

var sceneModes = map[int]string{
	sceneWarm:     lightWarm,
	sceneCool:     lightCool,
	sceneDaylight: lightDaylight,
}

// sceneForMode is the reverse mapping, for decoding state.
func sceneForMode(mode string) int {
	switch mode {
	case lightWarm:
		return sceneWarm
	case lightCool:
		return sceneCool
	case lightDaylight:
		return sceneDaylight
	default:
		return 0
	}
}

// ---------------------------------------------------------------------------
// Fan — a fan with no light control beyond on/off (models R1, R2, R3, K1, I2…).
// ---------------------------------------------------------------------------

type Fan struct {
	base
}

var (
	_ device.Device       = (*Fan)(nil)
	_ device.Switchable   = (*Fan)(nil)
	_ device.SpeedControl = (*Fan)(nil)
	_ device.SleepMode    = (*Fan)(nil)
	_ device.TimerControl = (*Fan)(nil)
	_ device.LightSwitch  = (*Fan)(nil)
	_ device.Pollable     = (*Fan)(nil)
	_ device.Closer       = (*Fan)(nil)
)

func (f *Fan) Driver() string { return DriverFan }

func (f *Fan) Capabilities() []string {
	return []string{
		device.CapSwitch, device.CapSpeed, device.CapSleep,
		device.CapTimer, device.CapLight,
	}
}

func (f *Fan) On() error                   { return f.setPower(true) }
func (f *Fan) Off() error                  { return f.setPower(false) }
func (f *Fan) SetSpeed(step int) error     { return f.setSpeed(step) }
func (f *Fan) SpeedRange() (int, int)      { return minSpeed, maxSpeed }
func (f *Fan) SetSleep(on bool) error      { return f.setSleep(on) }
func (f *Fan) SetTimer(hours int) error    { return f.setTimer(hours) }
func (f *Fan) TimerOptions() []int         { return append([]int(nil), timerHours...) }
func (f *Fan) SetLight(on bool) error      { return f.setLight(on) }
func (f *Fan) Poll() (device.State, error) { return f.poll(lightOnOff) }

// ---------------------------------------------------------------------------
// FanLight — a fan whose light dims and has colour modes (models I1, I5, M1,
// S1, S2). Same transport, a larger capability surface.
// ---------------------------------------------------------------------------

type FanLight struct {
	base
}

var (
	_ device.Device       = (*FanLight)(nil)
	_ device.Switchable   = (*FanLight)(nil)
	_ device.SpeedControl = (*FanLight)(nil)
	_ device.SleepMode    = (*FanLight)(nil)
	_ device.TimerControl = (*FanLight)(nil)
	_ device.Dimmable     = (*FanLight)(nil)
	_ device.SceneControl = (*FanLight)(nil)
	_ device.Pollable     = (*FanLight)(nil)
	_ device.Closer       = (*FanLight)(nil)
)

func (f *FanLight) Driver() string { return DriverFanLight }

func (f *FanLight) Capabilities() []string {
	return []string{
		device.CapSwitch, device.CapSpeed, device.CapSleep,
		device.CapTimer, device.CapBrightness, device.CapScene,
	}
}

func (f *FanLight) On() error                   { return f.setPower(true) }
func (f *FanLight) Off() error                  { return f.setPower(false) }
func (f *FanLight) SetSpeed(step int) error     { return f.setSpeed(step) }
func (f *FanLight) SpeedRange() (int, int)      { return minSpeed, maxSpeed }
func (f *FanLight) SetSleep(on bool) error      { return f.setSleep(on) }
func (f *FanLight) SetTimer(hours int) error    { return f.setTimer(hours) }
func (f *FanLight) TimerOptions() []int         { return append([]int(nil), timerHours...) }
func (f *FanLight) SetBrightness(pct int) error { return f.setBrightness(pct) }
func (f *FanLight) Scenes() []device.Scene      { return append([]device.Scene(nil), lightScenes...) }
func (f *FanLight) Poll() (device.State, error) { return f.poll(lightDimmable) }

func (f *FanLight) SetScene(id int) error {
	mode, ok := sceneModes[id]
	if !ok {
		return fmt.Errorf("atomberg %s: unknown light mode %d", f.id, id)
	}
	if err := f.command(map[string]any{"light_mode": mode}); err != nil {
		return err
	}
	f.applyState(func(s *device.State) { s.Scene = id })
	return nil
}

// SetSceneSpeed is a no-op: the light modes are static, with no animation to
// pace. Implemented because SceneControl asks for it, like the tunable-white
// WiZ bulb.
func (f *FanLight) SetSceneSpeed(int) error { return nil }

// ---------------------------------------------------------------------------
// Construction & registration
// ---------------------------------------------------------------------------

// driverFor maps the model code a fan announces in its beacon to the driver key
// registered with the factory. Models with a dimmable, colour-capable light get
// the richer driver; the rest get the plain fan. An unknown model returns "" —
// reported as found but unsupported, never guessed at, because commanding a fan
// through the wrong driver would silently do the wrong thing.
func driverFor(model string) string {
	switch model {
	case "I1", "I5", "M1", "S1", "S2":
		return DriverFanLight
	case "R1", "R2", "R3", "K1", "I2", "I3", "I4", "M2":
		return DriverFan
	default:
		return ""
	}
}

func newBase(spec config.DeviceSpec, deps config.Deps) base {
	listener := NewDiscoverer()
	// Start listening as soon as a fan exists rather than on the first command.
	// Beacons are the only source of addresses, and they cannot be requested —
	// the socket has to already be open when they arrive. A bind failure is not
	// fatal here: it surfaces on the first command or poll, with a message that
	// says the port could not be opened.
	_ = listener.ensure()
	return base{
		id:       spec.ID,
		name:     spec.Name,
		model:    spec.Model,
		mac:      spec.MAC,
		arp:      deps.Resolver,
		listener: listener,
		bus:      deps.Bus,
		// Optimistic until the first beacon or poll reconciles it, matching how
		// the other brands start.
		state: device.State{Online: true},
	}
}

// New builds a plain Fan (matches config.Constructor).
func New(spec config.DeviceSpec, deps config.Deps) (device.Device, error) {
	f := &Fan{base: newBase(spec, deps)}
	f.unwatch = f.listener.watchFan(f.mac, f.base.pushState(lightOnOff))
	return f, nil
}

// NewFanLight builds a FanLight (matches config.Constructor).
func NewFanLight(spec config.DeviceSpec, deps config.Deps) (device.Device, error) {
	f := &FanLight{base: newBase(spec, deps)}
	f.unwatch = f.listener.watchFan(f.mac, f.base.pushState(lightDimmable))
	return f, nil
}

// pushState returns the listener callback that adopts and publishes a state the
// fan broadcast on its own — a change made with the physical remote or the
// vendor app, which is the whole reason this brand pushes rather than only
// polling.
func (b *base) pushState(light lightSupport) func(Snapshot) {
	return func(snapshot Snapshot) {
		b.applyState(func(s *device.State) { *s = snapshot.State.deviceState(light) })
	}
}

// Register wires Atomberg's drivers into the factory (called from cmd/setu).
func Register(f *config.Factory) {
	f.Register(Brand, DriverFan, "Fan", New)
	f.Register(Brand, DriverFanLight, "Fan with light", NewFanLight)
}
