// Package events provides a tiny, dependency-free publish/subscribe bus built
// on Go channels. It is the backbone of Setu's event-driven core: devices and
// the state poller publish StateChanged events; the WebSocket hub subscribes to
// push them to browsers, and the manager subscribes to keep a fast state
// snapshot. The automation engine uses the recoverable subscription at this
// same seam, without changing publishers.
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
	subs   map[chan Event]chan struct{}
	buffer int // per-subscriber event buffer, sized for brief bursts
}

// NewBus returns a ready-to-use Bus.
func NewBus() *Bus {
	return &Bus{
		subs:   make(map[chan Event]chan struct{}),
		buffer: 16,
	}
}

// Subscribe registers a new subscriber and returns its receive channel together
// with an unsubscribe function. Always call unsubscribe (e.g. via defer) when
// finished, or the subscription leaks. unsubscribe is safe to call more than
// once.
func (b *Bus) Subscribe() (<-chan Event, func()) {
	events, _, unsubscribe := b.subscribe(false)
	return events, unsubscribe
}

// SubscribeRecoverable registers a subscriber that also receives a coalesced
// resync signal if its event buffer overflows. State consumers can then read one
// fresh manager snapshot instead of running a periodic reconciliation ticker.
func (b *Bus) SubscribeRecoverable() (<-chan Event, <-chan struct{}, func()) {
	return b.subscribe(true)
}

// Resync pauses publication while a recoverable subscriber discards its stale
// buffer and reads authoritative device state. Device methods update state
// before publishing, so a concurrent change is either in that snapshot or is
// delivered as a normal event immediately afterwards.
func (b *Bus) Resync(snapshot func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	snapshot()
}

func (b *Bus) subscribe(recoverable bool) (<-chan Event, <-chan struct{}, func()) {
	ch := make(chan Event, b.buffer)
	var resync chan struct{}
	if recoverable {
		resync = make(chan struct{}, 1)
	}
	b.mu.Lock()
	b.subs[ch] = resync
	b.mu.Unlock()

	var once sync.Once
	unsubscribe := func() {
		once.Do(func() {
			b.mu.Lock()
			delete(b.subs, ch)
			b.mu.Unlock()
			close(ch)
			if resync != nil {
				close(resync)
			}
		})
	}
	return ch, resync, unsubscribe
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
	for ch, resync := range b.subs {
		select {
		case ch <- e:
		default:
			// The event is dropped rather than blocking publishers. Recoverable
			// subscribers get at most one pending snapshot hint for the burst.
			if resync != nil {
				select {
				case resync <- struct{}{}:
				default:
				}
			}
		}
	}
}
