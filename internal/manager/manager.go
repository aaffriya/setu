// Package manager is Setu's device registry and read model. It holds the
// instantiated devices keyed by id, hands them out for command routing, and
// maintains a fast, event-driven snapshot of device state for the API.
package manager

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"setu/internal/control"
	"setu/internal/device"
	"setu/internal/events"
)

// Manager owns the set of devices and a cache of their latest state. The cache
// is kept current by subscribing to the event bus (principle 6: the manager is
// an event consumer), so building an API snapshot never has to touch devices or
// take their locks.
type Manager struct {
	bus *events.Bus

	mu      sync.RWMutex
	order   []string                 // device ids in config order
	devices map[string]device.Device // id → device
	latest  map[string]device.State  // id → most recent state (event-driven)
	ops     map[string]*deviceOperation
	health  map[string]DeviceDiagnostics

	unsubscribe func()
	done        chan struct{}
	closed      bool
}

// deviceOperation owns the device instance used by serialized hardware work.
// A caller may retain this handle while Remove or Replace waits for its mutex;
// active/device are therefore checked only after locking it.
type deviceOperation struct {
	mu     sync.Mutex
	device device.Device
	active bool
}

func newDeviceOperation(d device.Device) *deviceOperation {
	return &deviceOperation{device: d, active: true}
}

// lockDevice returns the current live instance while keeping the operation
// locked. A stale handle captured before removal is rejected rather than being
// allowed to command a device that has already been closed.
func (op *deviceOperation) lockDevice() (device.Device, bool) {
	op.mu.Lock()
	if !op.active {
		op.mu.Unlock()
		return nil, false
	}
	return op.device, true
}

func (op *deviceOperation) unlock() { op.mu.Unlock() }

// New creates a Manager over the given devices and starts consuming state
// events. It works correctly with zero devices. Call Close to stop it.
func New(bus *events.Bus, devices []device.Device) *Manager {
	m := &Manager{
		bus:     bus,
		devices: make(map[string]device.Device, len(devices)),
		latest:  make(map[string]device.State, len(devices)),
		ops:     make(map[string]*deviceOperation, len(devices)),
		health:  make(map[string]DeviceDiagnostics, len(devices)),
		done:    make(chan struct{}),
	}
	for _, d := range devices {
		m.order = append(m.order, d.ID())
		m.devices[d.ID()] = d
		m.latest[d.ID()] = d.State() // seed cache from initial device state
		m.ops[d.ID()] = newDeviceOperation(d)
		_, pollable := d.(device.Pollable)
		m.health[d.ID()] = DeviceDiagnostics{ID: d.ID(), Pollable: pollable}
	}

	sub, resync, unsub := bus.SubscribeRecoverable()
	m.unsubscribe = unsub
	go m.consume(sub, resync)
	return m
}

// consume keeps the latest-state cache current from the event bus.
func (m *Manager) consume(sub <-chan events.Event, resync <-chan struct{}) {
	for {
		select {
		case ev, ok := <-sub:
			if !ok {
				return
			}
			if ev.Type == events.StateChanged {
				m.mu.Lock()
				if _, known := m.devices[ev.DeviceID]; known {
					m.latest[ev.DeviceID] = ev.State
				}
				m.mu.Unlock()
			}
		case _, ok := <-resync:
			if !ok {
				return
			}
			// A full subscriber buffer is no longer a complete history. Discard
			// those stale entries and replace the cache with each device's current
			// in-memory state while publication is paused, so an older event cannot
			// overwrite the recovery snapshot afterwards.
			alive := true
			m.bus.Resync(func() {
				alive = drainPendingEvents(sub)
				if alive {
					m.resyncLatest()
				}
			})
			if !alive {
				return
			}
		case <-m.done:
			return
		}
	}
}

func drainPendingEvents(stream <-chan events.Event) bool {
	for range cap(stream) {
		select {
		case _, ok := <-stream:
			if !ok {
				return false
			}
		default:
			return true
		}
	}
	return true
}

func (m *Manager) resyncLatest() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, dev := range m.devices {
		m.latest[id] = dev.State()
	}
}

// Close stops the manager's event consumer and releases whatever the devices
// hold open. It waits for each in-flight command/poll before closing that
// device, and rejects new operations as soon as shutdown starts.
func (m *Manager) Close() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	ops := make([]*deviceOperation, 0, len(m.order))
	for _, id := range m.order {
		if op := m.ops[id]; op != nil {
			ops = append(ops, op)
		}
	}
	unsubscribe := m.unsubscribe
	m.unsubscribe = nil
	m.mu.Unlock()

	close(m.done)
	if unsubscribe != nil {
		unsubscribe()
	}
	for _, op := range ops {
		dev, active := op.lockDevice()
		if !active {
			continue
		}
		op.active = false
		op.device = nil
		closeDevice(dev)
		op.unlock()
	}
}

// Add registers a device the user just added. It is rejected if the id is
// already taken — ids are the handle every command, automation and preference
// uses, so two devices may never share one.
func (m *Manager) Add(d device.Device) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return fmt.Errorf("manager: closed")
	}
	id := d.ID()
	if _, exists := m.devices[id]; exists {
		return fmt.Errorf("manager: device %q already exists", id)
	}
	m.order = append(m.order, id)
	m.devices[id] = d
	m.latest[id] = d.State()
	m.ops[id] = newDeviceOperation(d)
	_, pollable := d.(device.Pollable)
	m.health[id] = DeviceDiagnostics{ID: id, Pollable: pollable}
	return nil
}

// Remove drops a device and releases what it held open. An in-flight command or
// poll for it finishes first: they hold the device's operation lock, and this
// takes it before letting go of the device.
func (m *Manager) Remove(id string) bool {
	m.mu.RLock()
	if m.closed {
		m.mu.RUnlock()
		return false
	}
	op := m.ops[id]
	m.mu.RUnlock()
	if op == nil {
		return false
	}

	dev, active := op.lockDevice()
	if !active {
		return false
	}
	defer op.unlock()

	m.mu.Lock()
	if m.ops[id] != op {
		m.mu.Unlock()
		return false
	}
	delete(m.devices, id)
	delete(m.latest, id)
	delete(m.ops, id)
	delete(m.health, id)
	m.order = withoutID(m.order, id)
	op.active = false
	op.device = nil
	m.mu.Unlock()

	closeDevice(dev)
	return true
}

// Replace swaps in a rebuilt device with the same id, keeping its position in
// the list and its cached state: editing a device's name must not blank its
// card until the next poll.
func (m *Manager) Replace(d device.Device) bool {
	id := d.ID()
	m.mu.RLock()
	if m.closed {
		m.mu.RUnlock()
		return false
	}
	op := m.ops[id]
	m.mu.RUnlock()
	if op == nil {
		return false
	}

	previous, active := op.lockDevice()
	if !active {
		return false
	}
	defer op.unlock()

	m.mu.Lock()
	if m.ops[id] != op {
		m.mu.Unlock()
		return false
	}
	op.device = d
	m.devices[id] = d
	_, pollable := d.(device.Pollable)
	entry := m.health[id]
	entry.Pollable = pollable
	m.health[id] = entry
	m.mu.Unlock()

	closeDevice(previous)
	return true
}

func withoutID(ids []string, id string) []string {
	out := ids[:0]
	for _, existing := range ids {
		if existing != id {
			out = append(out, existing)
		}
	}
	return out
}

// closeDevice releases a device's long-lived resources, if it has any.
func closeDevice(d device.Device) {
	if c, ok := d.(device.Closer); ok {
		_ = c.Close()
	}
}

// Device returns the device with the given id.
func (m *Manager) Device(id string) (device.Device, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	d, ok := m.devices[id]
	return d, ok
}

// Command serializes a command with hardware polling for the same device. This
// prevents a poll response that started before the command from arriving later
// and replacing the successful command with stale state. Different devices are
// still fully concurrent.
func (m *Manager) Command(id string, req control.Request) (DeviceView, bool, error) {
	m.mu.RLock()
	if m.closed {
		m.mu.RUnlock()
		return DeviceView{}, false, nil
	}
	op := m.ops[id]
	m.mu.RUnlock()
	if op == nil {
		return DeviceView{}, false, nil
	}

	dev, active := op.lockDevice()
	if !active {
		return DeviceView{}, false, nil
	}
	defer op.unlock()
	if err := control.Execute(dev, req); err != nil {
		m.recordCommand(id, req.Action, err)
		// Invalid input never reached the transport, so there is nothing to
		// reconcile. A transport error is ambiguous, though: the device may have
		// applied the command and lost only its reply. Re-read this device while
		// still holding its operation lock so callers can restore authoritative
		// state instead of guessing from the HTTP failure.
		var inputErr control.InputError
		if !errors.As(err, &inputErr) {
			state, pollable, _, pollErr := m.pollLocked(id, dev)
			if pollable && (pollErr == nil || errors.Is(pollErr, device.ErrPollNoResponse)) {
				view := ViewOf(dev)
				view.State = state
				return view, true, err
			}
		}
		return DeviceView{}, true, err
	}
	m.recordCommand(id, req.Action, nil)
	view := ViewOf(dev)
	// Command events update this cache asynchronously too, but writing the fresh
	// state here makes an immediate snapshot authoritative even if a subscriber
	// was briefly backlogged.
	m.mu.Lock()
	m.latest[id] = view.State
	m.mu.Unlock()
	return view, true, nil
}

// Poll serializes one hardware read with commands for this device, updates the
// read model synchronously, and publishes only when the authoritative state
// changed. The returned pollable flag is false for devices without Pollable.
func (m *Manager) Poll(id string) (state device.State, pollable, changed bool, err error) {
	m.mu.RLock()
	if m.closed {
		m.mu.RUnlock()
		return device.State{}, false, false, nil
	}
	op := m.ops[id]
	m.mu.RUnlock()
	if op == nil {
		return device.State{}, false, false, nil
	}
	dev, active := op.lockDevice()
	if !active {
		return device.State{}, false, false, nil
	}
	defer op.unlock()
	return m.pollLocked(id, dev)
}

// pollLocked performs one authoritative read. The caller must hold this
// device's operation lock so a command and poll cannot overlap.
func (m *Manager) pollLocked(id string, dev device.Device) (state device.State, pollable, changed bool, err error) {
	pd, ok := dev.(device.Pollable)
	if !ok {
		return device.State{}, false, false, nil
	}
	state, err = pd.Poll()
	m.recordPoll(id, err)
	// Some devices remain controllable without a live reply (for example, a TV
	// that can still be woken by MAC). Their Poll returns a meaningful fallback
	// state with ErrPollNoResponse: retain and publish that state, while the
	// diagnostics record above still marks the contact failure.
	if err != nil && !errors.Is(err, device.ErrPollNoResponse) {
		return state, true, false, err
	}
	m.mu.Lock()
	previous := m.latest[id]
	changed = previous != state
	m.latest[id] = state
	m.mu.Unlock()
	if changed {
		m.bus.Publish(events.Event{Type: events.StateChanged, DeviceID: id, State: state})
	}
	return state, true, changed, err
}

// DeviceDiagnostics is a bounded, in-memory summary of the latest hardware
// operations for one device. It deliberately keeps no history and is reset when
// Setu restarts.
type DeviceDiagnostics struct {
	ID                string `json:"id"`
	Pollable          bool   `json:"pollable"`
	LastPollAt        int64  `json:"last_poll_at,omitempty"`
	LastPollError     string `json:"last_poll_error,omitempty"`
	LastCommandAt     int64  `json:"last_command_at,omitempty"`
	LastCommandAction string `json:"last_command_action,omitempty"`
	LastCommandError  string `json:"last_command_error,omitempty"`
}

func (m *Manager) recordPoll(id string, err error) {
	m.mu.Lock()
	entry := m.health[id]
	entry.LastPollAt = time.Now().UnixMilli()
	entry.LastPollError = diagnosticError(err)
	m.health[id] = entry
	m.mu.Unlock()
}

func (m *Manager) recordCommand(id, action string, err error) {
	m.mu.Lock()
	entry := m.health[id]
	entry.LastCommandAt = time.Now().UnixMilli()
	entry.LastCommandAction = diagnosticText(action, maxDiagnosticActionBytes)
	entry.LastCommandError = diagnosticError(err)
	m.health[id] = entry
	m.mu.Unlock()
}

// Diagnostics returns one bounded health record per configured device in
// config order. Reading it performs no device I/O.
func (m *Manager) Diagnostics() []DeviceDiagnostics {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]DeviceDiagnostics, 0, len(m.order))
	for _, id := range m.order {
		out = append(out, m.health[id])
	}
	return out
}

// View returns one cached device projection without touching the hardware.
func (m *Manager) View(id string) (DeviceView, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	dev, ok := m.devices[id]
	if !ok {
		return DeviceView{}, false
	}
	view := metaView(dev)
	view.State = m.latest[id]
	return view, true
}

const (
	maxDiagnosticActionBytes = 64
	maxDiagnosticErrorBytes  = 240
)

func diagnosticError(err error) string {
	if err == nil {
		return ""
	}
	return diagnosticText(err.Error(), maxDiagnosticErrorBytes)
}

func diagnosticText(message string, maxBytes int) string {
	message = strings.ToValidUTF8(message, "�")
	if len(message) <= maxBytes {
		return message
	}
	const suffix = "…"
	message = message[:maxBytes-len(suffix)]
	for !utf8.ValidString(message) {
		message = message[:len(message)-1]
	}
	return message + suffix
}

// Devices returns all devices in config order.
func (m *Manager) Devices() []device.Device {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]device.Device, 0, len(m.order))
	for _, id := range m.order {
		out = append(out, m.devices[id])
	}
	return out
}

// DeviceView is the API/JSON projection of a device: static metadata plus state.
type DeviceView struct {
	ID           string         `json:"id"`
	Name         string         `json:"name"`
	Brand        string         `json:"brand"`
	Model        string         `json:"model"`
	Series       string         `json:"series,omitempty"` // friendly product/series name, when the device provides one
	MAC          string         `json:"mac"`
	Capabilities []string       `json:"capabilities"`
	ColorTempMin int            `json:"color_temp_min,omitempty"` // hardware Kelvin range, for ColorTempControl devices
	ColorTempMax int            `json:"color_temp_max,omitempty"`
	SpeedMin     int            `json:"speed_min,omitempty"` // hardware step range, for SpeedControl devices
	SpeedMax     int            `json:"speed_max,omitempty"`
	TimerOptions []int          `json:"timer_options,omitempty"` // hour values accepted, for TimerControl devices
	Scenes       []device.Scene `json:"scenes,omitempty"`        // present only for SceneControl devices
	Apps         []device.App   `json:"apps,omitempty"`          // present only for AppControl devices
	State        device.State   `json:"state"`
}

// metaView projects a device's static metadata (everything but State).
func metaView(d device.Device) DeviceView {
	v := DeviceView{
		ID:           d.ID(),
		Name:         d.Name(),
		Brand:        d.Brand(),
		Model:        d.Model(),
		MAC:          d.MAC(),
		Capabilities: d.Capabilities(),
	}
	if ds, ok := d.(device.Described); ok {
		v.Series = ds.Series()
	}
	if ct, ok := d.(device.ColorTempControl); ok {
		v.ColorTempMin, v.ColorTempMax = ct.ColorTempRange()
	}
	if sp, ok := d.(device.SpeedControl); ok {
		v.SpeedMin, v.SpeedMax = sp.SpeedRange()
	}
	if tc, ok := d.(device.TimerControl); ok {
		v.TimerOptions = tc.TimerOptions()
	}
	if sc, ok := d.(device.SceneControl); ok {
		v.Scenes = sc.Scenes()
	}
	if ac, ok := d.(device.AppControl); ok {
		v.Apps = ac.Apps()
	}
	return v
}

// ViewOf builds a view for a single device using its own live State (used to
// return the freshest result right after a command).
func ViewOf(d device.Device) DeviceView {
	v := metaView(d)
	v.State = d.State()
	return v
}

// Snapshot returns a view of every device for the API, built from the cached
// state. The result is a fresh slice safe to serialize. With no devices it
// returns an empty (non-nil) slice so the API emits [] and not null.
func (m *Manager) Snapshot() []DeviceView {
	m.mu.RLock()
	defer m.mu.RUnlock()
	views := make([]DeviceView, 0, len(m.order))
	for _, id := range m.order {
		v := metaView(m.devices[id])
		v.State = m.latest[id]
		views = append(views, v)
	}
	return views
}
