# api — HTTP, WebSocket, static

`import "setu/internal/api"` · the front-end protocol layer. Device code knows
nothing about it.

## Purpose
- One `net/http` mux serves the embedded UI (`/`), the JSON API (`/api`), and live events (`/ws`).
- Translates uniform commands → capability calls via type assertions.

## Routes
- `GET /api/devices` → cached `manager.Snapshot()`; `?refresh=true` performs a one-shot hardware poll and overlays its successful states first.
- `POST /api/activity` → reset the adaptive poller's idle backoff without touching hardware.
- `GET /api/recover` → self-contained service-worker/cache recovery page; preserves token and UI preferences.
- `POST /api/devices/{id}/command` → `dispatch`: `on`/`off`, `set_brightness`, `set_color`, `set_color_temp`, `set_scene`, `set_scene_speed`, `volume_up`/`volume_down`/`set_volume`/`mute`, `key`, `key_down`/`key_up` (press-and-hold), `send_text`, `launch_app`.
- `GET /ws` → per-connection bus subscription; pushes `snapshot` (on connect) then `state_changed`.
- `/` → embedded `web/dist` with SPA fallback (a built-in placeholder if the UI isn't built).

## Files
- server.go (routing + JSON helpers), auth.go (bearer; also `?token=` for the WS), handlers.go (`dispatch`), ws.go (hub), static.go (embed + SPA + MIME).

## Gotchas
- ws.go: every write has a 10 s deadline (`wsWriteTimeout`) — half-open mobile sockets must die at the next event, not at kernel TCP timeout (~15 min of leaked goroutine + bus subscription).
- static.go: `/assets/*` is served `immutable, max-age=1y` (Vite content-hashes the names); `service-worker.js` is `no-cache`. The embedded FS has zero modtimes → no Last-Modified/ETag, so these explicit headers are the only caching signal browsers get.
- static.go: unknown non-asset paths return 200 + index.html (SPA fallback); missing `/assets/*` paths return 404 so HTML cannot masquerade as stale JS/CSS.

## Errors
- `400` unsupported capability / bad input · `404` unknown device · `502` device or I/O failure · `401` missing/wrong token.

## Seam
- A second front-end (e.g. an Apple HomeKit bridge) is added **beside** this package, talking to the same manager + event bus — no device changes.
