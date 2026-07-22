// Package manager is Setu's device registry and read model. It holds the
// instantiated devices keyed by id, hands them out for command routing, and
// maintains a fast, event-driven snapshot of device state for the API.
package manager

import (
	"errors"
	"sync"

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
	ops     map[string]*sync.Mutex   // one command/poll operation at a time per device

	unsubscribe func()
	done        chan struct{}
}

// New creates a Manager over the given devices and starts consuming state
// events. It works correctly with zero devices. Call Close to stop it.
func New(bus *events.Bus, devices []device.Device) *Manager {
	m := &Manager{
		bus:     bus,
		devices: make(map[string]device.Device, len(devices)),
		latest:  make(map[string]device.State, len(devices)),
		ops:     make(map[string]*sync.Mutex, len(devices)),
		done:    make(chan struct{}),
	}
	for _, d := range devices {
		m.order = append(m.order, d.ID())
		m.devices[d.ID()] = d
		m.latest[d.ID()] = d.State() // seed cache from initial device state
		m.ops[d.ID()] = &sync.Mutex{}
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

// Close stops the manager's event consumer. Safe to call once.
func (m *Manager) Close() {
	close(m.done)
	if m.unsubscribe != nil {
		m.unsubscribe()
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
	dev, ok := m.devices[id]
	op := m.ops[id]
	m.mu.RUnlock()
	if !ok {
		return DeviceView{}, false, nil
	}

	op.Lock()
	defer op.Unlock()
	if err := control.Execute(dev, req); err != nil {
		// Invalid input never reached the transport, so there is nothing to
		// reconcile. A transport error is ambiguous, though: the device may have
		// applied the command and lost only its reply. Re-read this device while
		// still holding its operation lock so callers can restore authoritative
		// state instead of guessing from the HTTP failure.
		var inputErr control.InputError
		if !errors.As(err, &inputErr) {
			state, pollable, _, pollErr := m.pollLocked(id, dev)
			if pollable && pollErr == nil {
				view := ViewOf(dev)
				view.State = state
				return view, true, err
			}
		}
		return DeviceView{}, true, err
	}
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
	dev, ok := m.devices[id]
	op := m.ops[id]
	m.mu.RUnlock()
	if !ok {
		return device.State{}, false, false, nil
	}
	op.Lock()
	defer op.Unlock()
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
	if err != nil {
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
	return state, true, changed, nil
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
	Scenes       []device.Scene `json:"scenes,omitempty"` // present only for SceneControl devices
	Apps         []device.App   `json:"apps,omitempty"`   // present only for AppControl devices
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
