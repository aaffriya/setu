// Package events provides a tiny, dependency-free publish/subscribe bus built
// on Go channels. It is the backbone of Setu's event-driven core: devices and
// the state poller publish StateChanged events; the WebSocket hub subscribes to
// push them to browsers, and the manager subscribes to keep a fast state
// snapshot. A future automation/rules engine will subscribe at this same seam —
// with no change to the publishers. (This is the documented seam referenced in
// principle 6; the engine itself is intentionally not built yet.)
package events

import (
	"sync"
	"time"

	"setu/internal/device"
)

// Type enumerates the kinds of events on the bus. It is a string so it can be
// serialized directly to JSON for WebSocket clients.
type Type string

const (
	// StateChanged is emitted whenever a device's State changes.
	StateChanged Type = "state_changed"
)

// Event is a single message on the bus. A zero Time is filled in by Publish, so
// publishers don't have to remember to stamp it.
type Event struct {
	Type     Type         `json:"type"`
	DeviceID string       `json:"device_id"`
	State    device.State `json:"state"`
	Time     time.Time    `json:"time"`
}

// Bus is a fan-out pub/sub hub, safe for concurrent use. Subscribers receive
// every event published after they subscribe; a slow subscriber never blocks
// publishers (see Publish).
type Bus struct {
	mu     sync.RWMutex
	subs   map[chan Event]struct{}
	buffer int // per-subscriber channel buffer, sized for brief bursts
}

// NewBus returns a ready-to-use Bus.
func NewBus() *Bus {
	return &Bus{
		subs:   make(map[chan Event]struct{}),
		buffer: 16,
	}
}

// Subscribe registers a new subscriber and returns its receive channel together
// with an unsubscribe function. Always call unsubscribe (e.g. via defer) when
// finished, or the subscription leaks. unsubscribe is safe to call more than
// once.
func (b *Bus) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, b.buffer)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()

	var once sync.Once
	unsubscribe := func() {
		once.Do(func() {
			b.mu.Lock()
			delete(b.subs, ch)
			b.mu.Unlock()
			close(ch)
		})
	}
	return ch, unsubscribe
}

// Publish delivers an event to all current subscribers. Delivery is
// non-blocking: if a subscriber's buffer is full, the event is dropped for that
// subscriber rather than stalling the system. Authoritative state is always
// re-fetchable via the manager snapshot, so an occasional drop on a backed-up
// client is acceptable and keeps the core resilient.
func (b *Bus) Publish(e Event) {
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	// Holding the read lock here is mutually exclusive with the write lock in
	// unsubscribe, so a channel is never closed while we might send to it.
	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch := range b.subs {
		select {
		case ch <- e:
		default:
			// subscriber is full; drop rather than block.
		}
	}
}
