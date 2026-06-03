// Package manager is Setu's device registry and read model. It holds the
// instantiated devices keyed by id, hands them out for command routing, and
// maintains a fast, event-driven snapshot of device state for the API.
package manager

import (
	"sync"

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
		done:    make(chan struct{}),
	}
	for _, d := range devices {
		m.order = append(m.order, d.ID())
		m.devices[d.ID()] = d
		m.latest[d.ID()] = d.State() // seed cache from initial device state
	}

	sub, unsub := bus.Subscribe()
	m.unsubscribe = unsub
	go m.consume(sub)
	return m
}

// consume keeps the latest-state cache current from the event bus.
func (m *Manager) consume(sub <-chan events.Event) {
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
		case <-m.done:
			return
		}
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
	ID           string       `json:"id"`
	Name         string       `json:"name"`
	Brand        string       `json:"brand"`
	Model        string       `json:"model"`
	MAC          string       `json:"mac"`
	Capabilities []string     `json:"capabilities"`
	State        device.State `json:"state"`
}

// metaView projects a device's static metadata (everything but State).
func metaView(d device.Device) DeviceView {
	return DeviceView{
		ID:           d.ID(),
		Name:         d.Name(),
		Brand:        d.Brand(),
		Model:        d.Model(),
		MAC:          d.MAC(),
		Capabilities: d.Capabilities(),
	}
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
