# api — HTTP, WebSocket, static

`import "setu/internal/api"` · the front-end protocol layer. Device code knows
nothing about it.

## Purpose
- One `net/http` mux serves the embedded UI (`/`), the JSON API (`/api`), and live events (`/ws`).
- Translates uniform commands through the shared control executor into capability calls.

## Routes
- `GET /api/devices` → cached `manager.Snapshot()`; `?refresh=true` performs a one-shot hardware poll and overlays its successful states first.
- `GET /api/diagnostics` → latest per-device poll/command outcomes from bounded backend RAM; no hardware I/O.
- `POST /api/devices/{id}/refresh` → one serialized hardware read for that device; returns its refreshed view, including fallback state with a 502 when no live response arrives.
- `POST /api/activity` → reset the adaptive poller's idle backoff without touching hardware.
- `POST /api/discovery/scan` → asks every registered `resolver.Scanner` what is on the LAN; answers `{candidates, errors}`. Each candidate carries the device's own labels plus `configured`/`device_id` (matched by brand + MAC against the devices already added). POST because it actively broadcasts; one scan at a time (`409` otherwise); `501` when no scanner is registered. It stores nothing — adding is a separate, deliberate `POST /api/devices`.
- `GET /api/device-types` → the `(brand, model)` pairs this build can drive, for the UI's manual add form.
- `POST /api/devices` → add one device (id optional, derived from brand + MAC); it is stored, brought online, polled once, and returned as a live view (`201`).
- `PATCH /api/devices/{id}` → edit the labels (`name`, `series`). Identity (brand, model, MAC) is not editable — that would be a different device.
- `DELETE /api/devices/{id}` → remove it (`204`).
- `GET /api/devices/export` → the stored specs (`{version, items}`) — the backup form. `PUT /api/devices` replays it (restore); the whole list is validated and built before anything is replaced.
- `GET /api/recover` → self-contained service-worker/cache recovery page; preserves token and UI preferences.
- `POST /api/devices/{id}/command` → shared control executor: `on`/`off`, `set_brightness`, `set_color`, `set_color_temp`, `set_scene`, `set_scene_speed`, `volume_up`/`volume_down`/`set_volume`/`mute`, `key`, `key_down`/`key_up` (press-and-hold), `send_text`, `launch_app`.
- `GET|PUT /api/automations` → list/update the complete bounded rule set (revision checked).
- `GET /api/automations/export` → backup form, including webhook hashes but never plaintext tokens.
- `POST /api/automations/{id}/run` → manual run; `POST .../{id}/token` → rotate a webhook token and return it once.
- `POST /api/automation-hooks/{id}` → incoming trigger authenticated before reading its ignored payload; 4 KB body cap and a 10 s read deadline.
- `GET /ws` → per-connection recoverable bus subscription; pushes `snapshot` (on connect) then `state_changed`. A client that falls behind is closed so its automatic reconnect receives a fresh snapshot.
- `/` → embedded `web/dist` with SPA fallback (a built-in placeholder if the UI isn't built).

## Files
- server.go (routing + JSON helpers), auth.go (bearer; also `?token=` for the WS), handlers.go (device commands), devices.go (add/edit/remove/export), discovery.go (LAN scan → annotated candidates), automations.go (rule/webhook endpoints), ws.go (hub), static.go (embed + SPA + MIME).

## Gotchas
- ws.go: every write has a 10 s deadline (`wsWriteTimeout`) — half-open mobile sockets must die at the next event, not at kernel TCP timeout (~15 min of leaked goroutine + bus subscription).
- Device commands go through manager `Command()`, which serializes them with polling for that device and updates the read model before returning. A `502` may include a reconciled `device` view when the command result was ambiguous but the follow-up read succeeded.
- static.go: `/assets/*` is served `immutable, max-age=1y` (Vite content-hashes the names); `service-worker.js` is `no-cache`. The embedded FS has zero modtimes → no Last-Modified/ETag, so these explicit headers are the only caching signal browsers get.
- static.go: unknown non-asset paths return 200 + index.html (SPA fallback); missing `/assets/*` paths return 404 so HTML cannot masquerade as stale JS/CSS.

## Errors
- `400` unsupported capability / bad input · `401` missing/wrong token · `404` unknown item · `409` stale revision/paused rule/scan already running · `429` webhook limit · `501` no scanners registered · `502` device or I/O failure · `503` full automation queue.

## Device management
- The device-management routes are mounted only when an `Inventory` is configured. The API validates nothing itself: it hands the spec to `internal/inventory`, which owns the rules (valid spec, registered brand, free id, one brand per MAC) and reports a plain message the UI shows as-is.

## Seam
- A second front-end (e.g. an Apple HomeKit bridge) is added **beside** this package, talking to the same manager + event bus — no device changes.
