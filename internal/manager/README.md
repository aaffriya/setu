# manager — registry, snapshot, poller

`import "setu/internal/manager"` · owns devices and the read model.

## Purpose
- Holds instantiated devices by id; hands them to the API for command routing.
- Maintains an **event-driven state cache** for fast API snapshots.
- Runs the generic state poller.

## Key types
- `Manager` — `Device(id)`, `Devices()`, `Command()`, `Poll()`, `Snapshot()`, `Close()`.
- `DeviceView` — JSON projection (id, name, brand, model, mac, capabilities,
  optional color-temperature range, state); `ViewOf(d)`.
- `Poller` — `NewPoller(mgr, interval, log).Run(ctx)`; `Ready()` closes after the startup baseline.

## Flow
- `New()`: seed cache from each `device.State()`, then subscribe to the bus with overflow recovery.
- `consume()`: bus `StateChanged` → update cache. On overflow it drains stale events and replaces them with live device states. `Snapshot()` reads the cache (no device I/O).
- `Command()` and `Poll()` share one small mutex per device. A delayed poll can never overwrite a newer command, while different devices still operate concurrently. If a transport acknowledgement is lost, `Command()` re-polls only that device under the same lock and returns the reconciled view alongside the error when possible.
- `Poller`: an **immediate** first poll, then the configured active cadence. After 2m without app activity or device changes it backs off through 5m, 10m, 30m, 1h, then 6h. `Activity()` resets the cadence; `Refresh()` polls even when scheduled polling is disabled and reuses a cycle completed within 5s to coalesce startup/multi-client bursts. Each cycle calls manager `Poll()` **concurrently across devices** (cycle cost = slowest device, not the sum) and publishes **only on change**.
- State-triggered automation waits for `Ready()` and snapshots the resulting device state, so first discovery is a baseline rather than a user transition.

## Gotchas
- Works with **zero** devices; `Snapshot()` returns `[]`, never nil.
- Device `Poll` methods update quietly; manager `Poll()` publishes the resulting change exactly once.
