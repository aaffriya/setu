# `internal/api`

The HTTP/WebSocket protocol layer. One `net/http` mux serves the embedded app,
authenticated JSON routes, and live state events.

## Routes

| Route | Purpose |
| --- | --- |
| `GET /api/devices` | cached views; `?refresh=true` runs a coalesced hardware refresh |
| `GET /api/diagnostics` | latest bounded RAM-only operation status; no I/O |
| `POST /api/activity` | reset poll idle backoff |
| `POST /api/devices/{id}/refresh` | serialized one-device poll |
| `POST /api/devices/{id}/command` | execute a uniform `control.Request` |
| `GET /api/device-types` | registered brand/model catalog |
| `POST /api/devices` | validate, store, start, and poll a device |
| `PATCH /api/devices/{id}` | edit `name` or `series` only |
| `DELETE /api/devices/{id}` | remove and close a device |
| `GET /api/devices/export`, `PUT /api/devices` | export/replace inventory |
| `POST /api/discovery/scan` | parallel read-only LAN scan; one scan at a time |
| `GET`, `PUT /api/automations` | snapshot/replace revisioned rules |
| `GET /api/automations/export` | persistent form including webhook hashes |
| `POST /api/automations/{id}/run` | manual trigger |
| `POST /api/automations/{id}/token` | rotate and return a webhook token once |
| `POST /api/automation-hooks/{id}` | separately authenticated predefined trigger |
| `GET /ws` | snapshot, then `state_changed` events |
| `GET /api/recover` | public app-cache recovery page |

The static shell is public; device data and actions require the admin bearer
token. Browsers may pass the WebSocket token as `?token=`. Automation webhooks
use only their rule-specific bearer token, ignore the body, and cap it at 4 KB.

## Ownership

- `server.go`: route registration and JSON helpers.
- `handlers.go`: list, diagnostics, refresh, activity, commands.
- `devices.go`: inventory management and export/restore.
- `discovery.go`: concurrent scanners plus configured annotations.
- `automations.go`: rule and webhook endpoints.
- `ws.go`: recoverable bus subscription.
- `static.go`: embedded assets, SPA fallback, cache headers, recovery.

Commands go through `manager.Command`; the API contains no device-specific
behavior. Inventory validation stays in `internal/inventory`.

## Important behavior

- Input/capability errors are `400`; unknown items `404`; revision/scan conflicts
  `409`; webhook rate limit `429`; no scanners `501`; device I/O `502`; full
  automation queue `503`.
- A `502` may include a reconciled device view after ambiguous transport
  failure.
- WebSocket writes have a 10-second deadline; lagging clients reconnect for a
  fresh snapshot.
- `/assets/*` is immutable. Entry HTML and the service worker revalidate.
  Missing assets return `404`; only non-asset paths use SPA fallback.
