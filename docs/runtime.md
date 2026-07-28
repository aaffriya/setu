# Runtime invariants

Use this reference for changes spanning the API, manager, devices, event bus, or
frontend store.

## Command and state flow

```text
UI optimistic state
  -> POST command
  -> manager per-device lock
  -> control validation
  -> device transport
  -> device cached state + event
  -> manager snapshot + WebSocket
  -> UI reconciliation
```

- A command and a poll for the same device never overlap. Different devices
  remain concurrent.
- Transport failure is ambiguous, so `manager.Command` re-polls that device when
  possible and can return an authoritative view with the error.
- Device commands publish immediately. `Poll` updates quietly; the manager
  publishes the changed result exactly once.
- `Snapshot` reads the manager cache and performs no hardware I/O.
- Event delivery is non-blocking. Recoverable subscribers discard an incomplete
  buffer and install a fresh snapshot after overflow.

## Polling

Scheduled polling starts with one baseline. The configured interval is a floor:

| Idle since app activity or device change | Next interval |
| --- | --- |
| under 2 minutes | `SETU_POLL_INTERVAL` |
| 2–15 minutes | 5 minutes |
| 15–60 minutes | 10 minutes |
| 1–6 hours | 30 minutes |
| 6–24 hours | 1 hour |
| 24+ hours | 6 hours |

`SETU_POLL_INTERVAL=0` disables scheduled polling but not manual refresh.
Devices are polled concurrently per cycle. Cycles never overlap, and refreshes
during or within 5 seconds of a cycle reuse its result.

The browser sends throttled activity hints. Foreground resume and the header
refresh request a hardware refresh and reset idle backoff.

## Online and diagnostics

`State.Online` means the control surface is available, not necessarily that the
last live poll succeeded. Samsung stays online while off because Wake-on-LAN
needs only its MAC.

A driver with meaningful fallback state wraps `device.ErrPollNoResponse`.
Manager keeps that state but records the failed live contact in the bounded,
RAM-only diagnostics entry. Diagnostics never trigger I/O.

## Address lifecycle

- Stored identity is MAC, never IP.
- WiZ and Samsung: cached IP → ARP → verified brand discovery.
- Atomberg: fresh beacon → cached IP → cold-cache beacon wait → ARP.
- Any transport failure invalidates the cached IP.
- Samsung Wake-on-LAN and generic WoL bypass IP resolution.

## Browser lifecycle

- Only one WebSocket may be live or connecting.
- Handlers identity-check their socket so late events from an old connection
  cannot close or replace the current one.
- Token changes disconnect before reconnecting.
- `visibilitychange`, `pageshow`, and `online` coalesce into refresh plus eager
  reconnect; `pagehide` closes the old socket.
- The initial device request has an 8-second timeout. New refreshes supersede
  older ones.
- Commands queue per device to preserve tap order. Reconciled command errors,
  newer WebSocket events, and newer REST results outrank old optimistic state.

## Persistence and caches

| Owner | Data |
| --- | --- |
| `$SETU_STATE_DIR/setu.json` | device specs, automations, and user accounts |
| Samsung token files | TV pairing tokens |
| `localStorage` | access token, cached cards, favourites, rooms, manual scenes, order, expanded state, theme |
| service worker | versioned app shell only; never `/api` or `/ws` |

The service worker is enabled only in secure contexts. Hashed assets are
immutable; HTML and the worker revalidate. `/api/recover` removes only Setu's
worker and shell cache.
