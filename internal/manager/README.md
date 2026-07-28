# `internal/manager`

Live device registry, cached read model, serialized operations, diagnostics,
and adaptive poll coordination.

## Manager

- `Add`, `Remove`, `Replace`, `Close`: runtime membership and resource cleanup.
- `Device`, `Devices`: live instances.
- `Command`, `Poll`: one operation lock per device; different devices remain
  concurrent.
- `View`, `Snapshot`: cached API projections with no hardware I/O.
- `Diagnostics`: latest poll/command outcome per device, bounded in RAM.

An operation handle resolves the current instance after locking: replacement
redirects captured handles, while removal deactivates them before close.

Command success updates the cache immediately. Ambiguous transport failure
attempts one same-device poll before returning. `ErrPollNoResponse` retains
meaningful fallback state while diagnostics records failed contact.

`DeviceView` adds brand/driver/model, whether live state can be polled, whether
`Online` reports live reachability, supported ranges/options, scenes, and apps
to state.
With no devices, `Snapshot` returns `[]`.

## Poller

- Runs one initial baseline, then the configured active interval.
- Backs off through 5m, 10m, 30m, 1h, and 6h as the installation stays idle.
- Polls devices concurrently and never overlaps cycles.
- Publishes only changed states.
- Coalesces activity and reuses refresh results for 5 seconds.
- Scheduled polling can be disabled while manual refresh remains available.

Automation power triggers arm only after `Ready()` closes, so startup discovery
is a baseline rather than a user transition. Exact timing is in
[`docs/runtime.md`](../../docs/runtime.md).
