# devices — brand/model packages

`internal/devices/<brand>/` · one package per brand, one type per model.

## Packages
- `example/` — the documented **blueprint** (compiles; no real protocol).
- `wiz/` — Philips WiZ bulbs (UDP). Protocol: `docs/devices/wiz.md`.
- `samsung/` — Samsung Tizen TVs (REST + WebSocket + Wake-on-LAN). Protocol: `docs/devices/samsung.md`.

## Pattern (every brand)
- A brand `base` struct holds identity + transport + cached state; models **embed** it (composition, not inheritance).
- Each model implements `Model()`, `Capabilities()`, and **only** the capability interfaces it supports.
- Exports `New` (matches `config.Constructor`) + `Register(*config.Factory)`.
- Resolution + re-resolve-on-failure follows the `resolver.Resolver` seam.

## Add a device
- Copy `example/`, implement the protocol, add one `Register` line in `cmd/setu/main.go`, add a `config.yaml` entry. Full steps: root README → "Adding a device".
