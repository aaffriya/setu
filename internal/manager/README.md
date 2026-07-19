# manager — registry, snapshot, poller

`import "setu/internal/manager"` · owns devices and the read model.

## Purpose
- Holds instantiated devices by id; hands them to the API for command routing.
- Maintains an **event-driven state cache** for fast API snapshots.
- Runs the generic state poller.

## Key types
- `Manager` — `Device(id)`, `Devices()`, `Snapshot()`, `Close()`.
- `DeviceView` — JSON projection (id, name, brand, model, mac, capabilities,
  optional color-temperature range, state); `ViewOf(d)`.
- `Poller` — `NewPoller(mgr, bus, interval, log).Run(ctx)`.

## Flow
- `New()`: seed cache from each `device.State()`, then subscribe to the bus.
- `consume()`: bus `StateChanged` → update cache. `Snapshot()` reads the cache (no device locks, consistent).
- `Poller`: an **immediate** first poll, then the configured active cadence. After 2m without app activity or device changes it backs off through 5m, 10m, 30m, 1h, then 6h. `Activity()` resets the cadence; `Refresh()` polls even when scheduled polling is disabled and reuses a cycle completed within 5s to coalesce startup/multi-client bursts. Each cycle calls `Pollable.Poll()` **concurrently** (cycle cost = slowest device, not the sum) and publishes **only on change**.

## Gotchas
- Works with **zero** devices; `Snapshot()` returns `[]`, never nil.
- `Poll` updates device state quietly; the poller (not the device) publishes polled changes → avoids double events.
