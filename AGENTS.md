# Working in Setu

Setu is a low-resource LAN IoT bridge. One static Go binary serves the embedded
Svelte UI, JSON API, WebSocket, automation engine, and native device drivers.
Assume a 256–512 MB router-class host.

## Non-negotiable design rules

- Prefer the standard library and small, direct code. Do not add a dependency,
  layer, interface, or generality without a concrete need.
- Keep one binary. Do not add nginx, a supervisor, or a second service.
- Server behavior is code. Runtime settings are optional `SETU_*` environment
  variables; devices, automations, and users are state in
  `$SETU_STATE_DIR/setu.json`.
- The administrator is `SETU_TOKEN` and is never stored. Every other account is
  a stored token with a role and an explicit device list, and every restriction
  is enforced per request in `internal/api` — the app's hiding is advisory.
- MAC is device identity. Resolve and cache the current IP at runtime, then
  invalidate it on transport failure.
- Commands enter through `internal/control`; state changes leave through
  `internal/events`. Keep device-specific behavior inside its brand package.
- The automation engine stays bounded: fixed workers, capped rules/actions,
  RAM-only run history, no scripting or expression language.

## The only interface seams

1. Device capabilities in `internal/device`.
2. MAC resolution and LAN scanning in `internal/resolver`.
3. Front-end protocols over the manager and event bus; HTTP/WS is
   `internal/api`.

Do not introduce interfaces for single-implementation plumbing.

## Source map

- `cmd/setu`: composition root and brand/scanner registration.
- `internal/config`, `store`, `inventory`: environment (plus the startup
  self-test), persisted state, and stored specs to live devices.
- `internal/users`: the accounts besides the administrator — role, device
  grants, and hashed tokens.
- `internal/device`, `control`, `events`, `manager`: capability vocabulary,
  command dispatch, events, cached read model, diagnostics, and polling.
- `internal/automation`: bounded schedules, device-state and metric rules,
  unreachable and LAN-presence triggers, and webhooks.
- `internal/devices/<brand>`: native protocols; `example` is the template.
- `web`: Svelte 5 PWA. UI preferences stay in `localStorage`; operational
  automations and the device inventory stay server-side.
- `docs/runtime.md`: cross-package timing, cache, and lifecycle rules.

Read the nearest package README before editing that package.

## Device changes

- Implement only capabilities the hardware actually supports.
- Reusing capabilities requires no device-specific UI. A new capability must
  also be wired through control, manager metadata if needed, automation safety,
  web API types/optimistic state, `DeviceCard`, backup validation, and tests.
- If a brand can enumerate devices, implement `resolver.Scanner` and register
  it in `cmd/setu/main.go`.
- Never guess an unknown driver from a discovery response.
- Device metadata is three words with one job each: `brand` is the vendor,
  `driver` is which code runs it (identity with brand; never shown to a user —
  register a human label instead), `model` is the hardware as the device or the
  user reported it (presentation only; nothing branches on it).

## Validation

- Go: `gofmt -l .`, `go vet ./...`, `go test ./...`, `go build ./...`.
- Web: from `web/`, run `npm test`, `npm run check`, and `npm run build`.
- Preserve unrelated worktree changes. Commit only when asked.
