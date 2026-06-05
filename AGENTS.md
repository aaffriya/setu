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
3. **No new dependency** without a real reason (say why). Today: Go = `coder/websocket`,
   `yaml.v3`; web = Svelte + Vite + Tailwind. No heavy frameworks or UI kits.
4. **Idiomatic Go: composition, not inheritance.** Struct-embed a brand `base`; don't build
   inheritance trees. Interfaces only at the 3 seams below — not for single-impl plumbing.
5. **Config is data, not behavior.** Behavior lives in code; `config.yaml` is only instance data.
6. **MAC is the identity; IP is resolved at runtime.** Never hardcode an IP.
7. **Event-driven core.** Commands in → state events out via the bus. The automation engine is
   **not built yet** — keep its seam, don't add it unprompted.
8. **Single binary.** No nginx / reverse proxy / supervisor. It serves `/`, `/api`, `/ws` itself.

## The 3 seams (the only place new interfaces belong)
- **Capabilities** — `internal/device` (`Switchable`, `Dimmable`, `ColorControl`,
  `ColorTempControl`, `SceneControl`, `Volume`, `KeyControl`). New feature = one small interface
  + a `dispatch` case in `internal/api`. The API checks support via type assertions; never
  fatten devices that lack a capability.
- **Address resolution** — `internal/resolver` (`Resolver`: ARP now; per-brand discovery; DHCP later).
- **Front-end protocol** — `internal/api` over manager + bus. A second protocol (e.g. HomeKit)
  goes here, **never** in device code.

## Where things live (each package has its own README.md — read it first)
- `cmd/setu` — composition root: wire deps, **register brands**, serve.
- `internal/{device,events,resolver,manager,config,api}` — the core.
- `internal/devices/<brand>/` — one package per brand; `example/` is the blueprint.
- `web/` — Svelte 5 PWA (embedded). `docs/devices/*.md` — native device protocols. root `README.md` — architecture & usage.

## Adding a device (the main extension task)
1. Copy `internal/devices/example/` → `internal/devices/<brand>/`; set brand/model consts.
2. Put the wire protocol in the brand `base`; implement the capability methods + `Poll`.
3. Implement **only** the capabilities the model has; update the `var _ device.X = (*T)(nil)` asserts.
4. Export `New` + `Register`; add **one** `<brand>.Register(factory)` line in `cmd/setu/main.go`.
5. Add a `config.yaml` entry (brand, model, id, name, **mac**, ip-hint).
The frontend needs **no** change — cards render from `capabilities`.

## Frontend rules
- Svelte 5 runes; small JS heap (the reason we use Svelte). Render from device data/capabilities,
  no per-device markup. Resilient to mobile backgrounding (persist + re-fetch/reconnect on resume).
- UI-only preferences (e.g. favourites) live in `localStorage`, **not** the backend — keep the
  server free of user-pref state.

## Out of scope — don't add unless explicitly asked
- Automation/rules engine · HomeKit · config-driven device behavior · heavy deps · internet exposure.

## Before you finish
- Go: `gofmt -l .` clean · `go vet ./...` · `go test ./...` · `go build ./...`.
- Web: `npm run build` and `npm run check` (0 errors / 0 warnings).
- Keep changes small and focused. **Commit only when asked.**
