# `internal/events`

Small dependency-free pub/sub bus for `StateChanged` events.

- Publishers: device command methods, manager polling, and pushed device state.
- Consumers: manager cache, API WebSockets, and automation.
- `Publish` is non-blocking; one slow subscriber cannot stall devices.
- `Subscribe` returns an idempotent cancel function.
- `SubscribeRecoverable` adds a coalesced resync signal after overflow.
- `Resync` pauses publication while stale buffered events are drained and an
  authoritative snapshot is installed.
- `Publish` stamps a missing event time.

Dropped state events are acceptable only because every consumer has a snapshot
recovery path. Do not use this bus for commands or irreplaceable audit data.
