# `internal/events`

Small dependency-free pub/sub bus for replaceable device state and inventory
invalidation events.

- Publishers: device command methods, manager polling, pushed device state, and
  successful inventory/account-access mutations.
- Consumers: manager cache, API WebSockets, and automation.
- `Publish` is non-blocking; one slow subscriber cannot stall devices.
- `Subscribe` returns an idempotent cancel function.
- `SubscribeRecoverable` adds a coalesced resync signal after overflow.
- `Resync` pauses publication while stale buffered events are drained and an
  authoritative snapshot is installed.
- `Publish` stamps a missing event time.

Dropped events are acceptable only because every consumer has a snapshot
recovery path. A device-membership `InventoryChanged` may carry the current ids
inside the process so automation can prune transient clocks; WebSockets expose
only the event type and cause a permission-filtered re-read. Account-only
changes carry no ids. Do not use this bus for commands or irreplaceable audit
data.
