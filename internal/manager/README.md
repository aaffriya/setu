# manager — registry, snapshot, poller

`import "setu/internal/manager"` · owns devices and the read model.

## Purpose
- Holds instantiated devices by id; hands them to the API for command routing.
- Maintains an **event-driven state cache** for fast API snapshots.
- Runs the generic state poller.

## Key types
- `Manager` — `Device(id)`, `Devices()`, `Snapshot()`, `Close()`.
- `DeviceView` — JSON projection (id, name, brand, model, mac, capabilities, state); `ViewOf(d)`.
- `Poller` — `NewPoller(mgr, bus, interval, log).Run(ctx)`.

## Flow
- `New()`: seed cache from each `device.State()`, then subscribe to the bus.
- `consume()`: bus `StateChanged` → update cache. `Snapshot()` reads the cache (no device locks, consistent).
- `Poller`: an **immediate** first poll on `Run`, then every `poll_interval`; calls `Pollable.Poll()` and publishes **only on change**.

## Gotchas
- Works with **zero** devices; `Snapshot()` returns `[]`, never nil.
- `Poll` updates device state quietly; the poller (not the device) publishes polled changes → avoids double events.
