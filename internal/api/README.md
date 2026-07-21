# api — HTTP, WebSocket, static

`import "setu/internal/api"` · the front-end protocol layer. Device code knows
nothing about it.

## Purpose
- One `net/http` mux serves the embedded UI (`/`), the JSON API (`/api`), and live events (`/ws`).
- Translates uniform commands through the shared control executor into capability calls.

## Routes
- `GET /api/devices` → cached `manager.Snapshot()`; `?refresh=true` performs a one-shot hardware poll and overlays its successful states first.
- `POST /api/activity` → reset the adaptive poller's idle backoff without touching hardware.
- `GET /api/recover` → self-contained service-worker/cache recovery page; preserves token and UI preferences.
- `POST /api/devices/{id}/command` → shared control executor: `on`/`off`, `set_brightness`, `set_color`, `set_color_temp`, `set_scene`, `set_scene_speed`, `volume_up`/`volume_down`/`set_volume`/`mute`, `key`, `key_down`/`key_up` (press-and-hold), `send_text`, `launch_app`.
- `GET|PUT /api/automations` → list/update the complete bounded rule set (revision checked).
- `GET /api/automations/export` → backup form, including webhook hashes but never plaintext tokens.
- `POST /api/automations/{id}/run` → manual run; `POST .../{id}/token` → rotate a webhook token and return it once.
- `POST /api/automation-hooks/{id}` → incoming trigger authenticated before reading its ignored payload; 4 KB body cap and a 10 s read deadline.
- `GET /ws` → per-connection bus subscription; pushes `snapshot` (on connect) then `state_changed`.
- `/` → embedded `web/dist` with SPA fallback (a built-in placeholder if the UI isn't built).

## Files
- server.go (routing + JSON helpers), auth.go (bearer; also `?token=` for the WS), handlers.go (device commands), automations.go (rule/webhook endpoints), ws.go (hub), static.go (embed + SPA + MIME).

## Gotchas
- ws.go: every write has a 10 s deadline (`wsWriteTimeout`) — half-open mobile sockets must die at the next event, not at kernel TCP timeout (~15 min of leaked goroutine + bus subscription).
- static.go: `/assets/*` is served `immutable, max-age=1y` (Vite content-hashes the names); `service-worker.js` is `no-cache`. The embedded FS has zero modtimes → no Last-Modified/ETag, so these explicit headers are the only caching signal browsers get.
- static.go: unknown non-asset paths return 200 + index.html (SPA fallback); missing `/assets/*` paths return 404 so HTML cannot masquerade as stale JS/CSS.

## Errors
- `400` unsupported capability / bad input · `401` missing/wrong token · `404` unknown item · `409` stale revision/paused rule · `429` webhook limit · `502` device or I/O failure · `503` full automation queue.

## Seam
- A second front-end (e.g. an Apple HomeKit bridge) is added **beside** this package, talking to the same manager + event bus — no device changes.
