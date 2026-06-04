# events — the event bus

`import "setu/internal/events"` · channel-based pub/sub; the event-driven core.

## Purpose
- Decouples producers (devices, poller) from consumers (WS hub, manager, future automation engine).
- Tiny, dependency-free, safe for concurrent use.

## Key types
- `Bus` — `Subscribe() (<-chan Event, cancel)`, `Publish(Event)`, `NewBus()`.
- `Event{Type, DeviceID, State, Time}`; `Type` is `StateChanged`.

## Behaviour
- `Publish` is **non-blocking**: if a subscriber's buffer is full the event is dropped for that subscriber (state is always re-fetchable), so one slow client never stalls the system.
- `Subscribe` returns an unsubscribe func (idempotent via `sync.Once`) — always call it (e.g. `defer`).
- A zero `Event.Time` is stamped by `Publish`.

## Used by
- **Publishers:** device capability methods (immediate UI feedback) + `manager.Poller` (polled changes).
- **Subscribers:** `api` WS hub (push to browsers), `manager` (state cache). The automation engine will subscribe here later — no publisher changes needed.
