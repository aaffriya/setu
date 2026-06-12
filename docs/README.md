# Setu docs

Reference docs for humans **and** AI assistants — point-to-point, so a module can
be understood or extended without re-deriving context.

## Cross-module behavior
- [`runtime.md`](runtime.md) — command/state flow, timing model, socket
  lifecycles (browser + TV), the three caching layers, addressing invariants.
  **Read first when touching anything that spans packages.**

## Native device protocols
- [`devices/wiz.md`](devices/wiz.md) — Philips WiZ (UDP getPilot/setPilot, discovery).
- [`devices/samsung.md`](devices/samsung.md) — Samsung Tizen (REST + WebSocket + Wake-on-LAN, key codes, app IDs).

## Per-module context (a `README.md` lives in each package)
- Core: [`internal/device`](../internal/device/README.md) · [`internal/events`](../internal/events/README.md) · [`internal/resolver`](../internal/resolver/README.md) · [`internal/config`](../internal/config/README.md) · [`internal/manager`](../internal/manager/README.md) · [`internal/api`](../internal/api/README.md)
- Devices: [`internal/devices`](../internal/devices/README.md) · [`example`](../internal/devices/example/README.md) · [`wiz`](../internal/devices/wiz/README.md) · [`samsung`](../internal/devices/samsung/README.md)
- Entry & UI: [`cmd/setu`](../cmd/setu/README.md) · [`web`](../web/README.md)

## Architecture & usage
- Root [`README.md`](../README.md) — how it fits together, build/run, config, addressing, deployment, adding a device.

## Conventions
- Each package `README.md`: **Purpose · Key types · Flow/Files · Gotchas · Extend**.
- A device protocol doc: **at-a-glance · commands · discovery · gotchas · how Setu maps it**.
