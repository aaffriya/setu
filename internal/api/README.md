# `internal/api`

The HTTP/WebSocket protocol layer. One `net/http` mux serves the embedded app,
authenticated JSON routes, and live state events.

## Routes

| Route | Purpose |
| --- | --- |
| `GET /healthz` | public liveness probe; discloses nothing about the install |
| `GET /api/session` | the caller's name, role, and device grants |
| `GET /api/devices` | cached views; `?refresh=true` runs a coalesced hardware refresh |
| `GET /api/diagnostics` | latest bounded RAM-only operation status; no I/O |
| `POST /api/activity` | reset poll idle backoff |
| `POST /api/devices/{id}/refresh` | serialized one-device poll |
| `POST /api/devices/{id}/command` | execute a uniform `control.Request` |
| `GET /api/device-types` | registered brand/driver catalog, with labels |
| `POST /api/devices` | validate, store, start, and poll a device |
| `PATCH /api/devices/{id}` | edit `name` or `model` only |
| `DELETE /api/devices/{id}` | remove and close a device |
| `GET /api/devices/export`, `PUT /api/devices` | export/replace inventory |
| `POST /api/discovery/scan` | parallel read-only LAN scan; one scan at a time |
| `GET`, `PUT /api/automations` | snapshot/replace revisioned rules |
| `GET /api/automations/export` | persistent form including webhook hashes |
| `POST /api/automations/{id}/run` | manual trigger |
| `POST /api/automations/{id}/token` | rotate and return a webhook token once |
| `POST /api/automation-hooks/{id}` | separately authenticated predefined trigger |
| `GET`, `POST /api/users` | list and create accounts (token returned once) |
| `PATCH`, `DELETE /api/users/{id}` | edit or remove an account |
| `POST /api/users/{id}/token` | rotate and return a token once |
| `GET /ws` | snapshot, then `state_changed` events |
| `GET /api/recover` | public app-cache recovery page |

The static shell is public; device data and actions require a bearer token.
Browsers may pass the WebSocket token as `?token=`. Automation webhooks use only
their rule-specific bearer token, ignore the body, and cap it at 4 KB.

## Who may do what

Three middlewares guard these routes, and every one of them re-checks per
request — the app's own hiding is advisory:

- `auth`: any account. Routes naming a device additionally check that this
  account was granted it (`403`, not `404`: the administrator has to be able to
  tell "never shared" from "gone").
- `authModify`: accounts whose role is `modify`. Adding devices, scanning, and
  writing automations.
- `authAdmin`: the `SETU_TOKEN` holder only. Managing users, and the
  whole-installation export/restore that necessarily reaches past any one
  person's grants.

The administrator has no stored record — their token is the environment — so no
API call, restore, or damaged state file can lock them out.

Reads are filtered rather than refused: `GET /api/devices`, `/api/diagnostics`
and the WebSocket carry only granted devices, and `GET /api/automations` only
the rules this account owns. A socket resolves its grants once, at connect, so a
device granted afterwards appears on the next refresh and streams live after the
next reconnect.

A rule is owned when every device it names was granted **and** every rule it
calls is owned too — running a rule runs its callees' actions, so calling one is
exactly as powerful as owning it, and an id the account was never shown is not
ownable. `ownedRules` resolves that cascade to a fixpoint.

A restricted `PUT /api/automations` is merged against the stored set and then
re-checked on the result, so a save from a partial view can neither delete the
rules it was not shown, claim their ids, nor reach them through a call. The
installation-wide `paused` flag stops rules such an account never saw, so its
value is taken from storage and only the administrator can change it.

Hardware work is rate limited per account and device — commands and single-device
refreshes share one token bucket (burst 20, 5/s sustained) — so a client stuck in
a retry loop cannot saturate one device by either route.

Grants follow their device: deleting one clears it from every account, and a
restore prunes grants to the device list it installed, so an id that returns
later never arrives pre-shared.

## Ownership

- `server.go`: route registration and JSON helpers.
- `auth.go`: the Principal, the three middlewares, and `/api/session`.
- `ratelimit.go`: the bounded per-account, per-device command budget.
- `handlers.go`: list, diagnostics, refresh, activity, commands, health.
- `devices.go`: inventory management and export/restore.
- `discovery.go`: concurrent scanners plus configured annotations.
- `automations.go`: rule and webhook endpoints, and their per-account scoping.
- `users.go`: account management.
- `ws.go`: recoverable bus subscription.
- `static.go`: embedded assets, SPA fallback, cache headers, recovery.

Commands go through `manager.Command`; the API contains no device-specific
behavior. Inventory validation stays in `internal/inventory`.

## Important behavior

- Input/capability errors are `400`; a permission the account lacks `403`;
  unknown items `404`; revision/scan conflicts `409`; webhook and command rate
  limits `429`; no scanners `501`; device I/O `502`; full automation queue `503`.
- A `502` may include a reconciled device view after ambiguous transport
  failure.
- WebSocket writes have a 10-second deadline; lagging clients reconnect for a
  fresh snapshot.
- `/assets/*` is immutable. Entry HTML and the service worker revalidate.
  Missing assets return `404`; only non-asset paths use SPA fallback.
