# api — HTTP, WebSocket, static

`import "setu/internal/api"` · the front-end protocol layer. Device code knows
nothing about it.

## Purpose
- One `net/http` mux serves the embedded UI (`/`), the JSON API (`/api`), and live events (`/ws`).
- Translates uniform commands → capability calls via type assertions.

## Routes
- `GET /api/devices` → `manager.Snapshot()`.
- `POST /api/devices/{id}/command` → `dispatch`: `on`/`off`, `set_brightness`, `set_color`, `set_color_temp`, `set_scene`, `set_scene_speed`, `volume_up`/`volume_down`/`mute`, `key`, `launch_app`.
- `GET /ws` → per-connection bus subscription; pushes `snapshot` (on connect) then `state_changed`.
- `/` → embedded `web/dist` with SPA fallback (a built-in placeholder if the UI isn't built).

## Files
- server.go (routing + JSON helpers), auth.go (bearer; also `?token=` for the WS), handlers.go (`dispatch`), ws.go (hub), static.go (embed + SPA + MIME).

## Errors
- `400` unsupported capability / bad input · `404` unknown device · `502` device or I/O failure · `401` missing/wrong token.

## Seam
- A second front-end (e.g. an Apple HomeKit bridge) is added **beside** this package, talking to the same manager + event bus — no device changes.
