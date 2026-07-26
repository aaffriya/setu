# AGENTS.md — how to work in Setu

Instructions for AI assistants (and humans) editing this repo. Auto-loaded by
Codex. Keep this file short; detail lives in each package's `README.md` and
in `docs/`.

**What Setu is:** a tiny self-hosted IoT bridge — **one static Go binary** serves the
embedded Svelte UI + JSON API + WebSocket and controls local devices (WiZ, Samsung…).
It runs on **router / IoT hardware (~256–512 MB RAM)**. Treat every dependency and every
megabyte as precious.

## Golden rules — read before changing code
1. **Lightweight & simple wins.** Standard library first. It's an IoT device, not a server.
2. **No over-engineering.** Don't add layers, abstractions, or generality the current scope
   doesn't need. When in doubt pick the simpler design. Prefer deleting over adding.
3. **No new dependency** without a real reason (say why). Today: Go = `coder/websocket` only;
   web = Svelte + Vite + Tailwind. No heavy frameworks or UI kits.
4. **Idiomatic Go: composition, not inheritance.** Struct-embed a brand `base`; don't build
   inheritance trees. Interfaces only at the 3 seams below — not for single-impl plumbing.
5. **Config is data, not behavior.** Behavior lives in code. Server settings are env vars
   (`SETU_*`, all optional, defaults built in); the devices the user added are *state*, stored
   in `$SETU_STATE_DIR/setu.json` beside the automations. There is no config file.
6. **MAC is the identity; IP is resolved at runtime.** Never hardcode an IP.
7. **Event-driven core.** Commands in → state events out via the bus. The automation engine
   (`internal/automation`) consumes that bus and is deliberately small and bounded — fixed
   workers, capped rules/actions/runs, history in RAM. Keep it that way; it is not a
   general rules engine.
8. **Single binary.** No nginx / reverse proxy / supervisor. It serves `/`, `/api`, `/ws` itself.

## The 3 seams (the only place new interfaces belong)
- **Capabilities** — `internal/device` (`Switchable`, `Dimmable`, `ColorControl`,
  `ColorTempControl`, `SceneControl`, `SpeedControl`, `SleepMode`, `TimerControl`, `LightSwitch`, `Volume`,
  `KeyControl`). New feature = one small interface + a case in `internal/control`'s command
  switch (shared by the API and automations). Support is checked by type assertion; never
  fatten devices that lack a capability. A **new** capability also needs UI work — see
  "Adding a device" in the root `README.md` for the full list.
- **Address resolution** — `internal/resolver` (`Resolver`: ARP now; per-brand discovery; DHCP later).
  Same seam, other direction: `Scanner` lists devices not added yet (UI's device scan →
  `POST /api/discovery/scan`); adding one is a separate `POST /api/devices` (`internal/inventory`).
- **Front-end protocol** — `internal/api` over manager + bus. A second protocol (e.g. HomeKit)
  goes here, **never** in device code.

## Where things live (each package has its own README.md — read it first)
- `cmd/setu` — composition root: wire deps, **register brands**, serve.
- `internal/{device,events,resolver,manager,config,control,api}` — the core; `internal/automation` — the bounded rules engine; `internal/store` — the one JSON state file; `internal/inventory` — stored device specs ↔ live devices.
- `internal/devices/<brand>/` — one package per brand; `example/` is the blueprint.
- `web/` — Svelte 5 PWA (embedded). `docs/devices/*.md` — native device protocols. root `README.md` — architecture & usage.

## Adding a device (the main extension task)
1. Copy `internal/devices/example/` → `internal/devices/<brand>/`; set brand/model consts.
2. Put the wire protocol in the brand `base`; implement the capability methods + `Poll`.
3. Implement **only** the capabilities the model has; update the `var _ device.X = (*T)(nil)` asserts.
4. Export `New` + `Register`; add **one** `<brand>.Register(factory)` line in `cmd/setu/main.go`.
   If the brand can enumerate its devices, implement `Scan` on its discoverer and add it to the
   `scanners` slice there too.
5. Add the device from Settings → Devices (scan or by hand). The driver resolves IP at runtime.
The frontend needs **no** change *when the device reuses existing capabilities* — cards render from
`capabilities`. A brand-new capability does reach the UI: see "Adding a device" in the root
`README.md`, and note it must join one of `DeviceCard`'s groups or its controls are unreachable.

## Frontend rules
- Svelte 5 runes; small JS heap (the reason we use Svelte). Render from device data/capabilities,
  no per-device markup. Resilient to mobile backgrounding (persist + re-fetch/reconnect on resume).
- UI-only preferences (favourites, rooms, order, theme) live in `localStorage`, **not** the
  backend — keep the server free of user-pref state. *Which devices exist* is not a preference:
  that is server state (`internal/inventory`), and it is in the backup file too.

## Out of scope — don't add unless explicitly asked
- HomeKit · config-driven device behavior · heavy deps · internet exposure.
- Growing the automation engine beyond its bounded scope (no scripting, no expression language,
  no persistent run history).

## Before you finish
- Go: `gofmt -l .` clean · `go vet ./...` · `go test ./...` · `go build ./...`.
- Web: `npm run build` and `npm run check` (0 errors / 0 warnings).
- Keep changes small and focused. **Commit only when asked.**
