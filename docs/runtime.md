# Runtime logic & flow

Cross-module behavior that no single package README shows. Read this before
changing anything on the command path, the event path, or the connection /
caching lifecycles. Per-package detail stays in each package's `README.md`.

## Command path (tap → device)

```
DeviceCard → store.command()                      web/src/lib/store.ts
  ├─ optimistic update of the TARGET device only
  ├─ POST /api/devices/{id}/command               internal/api/handlers.go
  │    auth → dispatch (type assertion) → capability method → device transport
  ├─ response = fresh DeviceView → reconciles that card
  └─ on error → revert ONLY that device (never the whole list: other devices
     kept receiving WS/optimistic updates while this command was in flight)
```

- Devices **never hold their state mutex during network I/O** — a slow `Poll`
  can never block a command. Keep it that way in new device code.
- Errors: `400` unsupported capability / bad input · `404` unknown id ·
  `502` device I/O. The UI shows a toast and reverts; state truth returns via
  poll/WS.

## State path (device → UI)

Two publishers, one rule (avoids double events):
- **Command methods** publish immediately via `applyState` (optimistic echo).
- **Poll() updates quietly** (`updateState`); the *poller* diffs against its
  `last` map and publishes only changes.

Manager subscribes to the bus → `latest` cache → `Snapshot()` touches no
devices. WS handler order is subscribe → snapshot → stream, so there is no
missed-event window. Bus delivery is non-blocking: a subscriber whose buffer
(16) is full **drops events**; the REST snapshot (`refresh()` on resume) is the
recovery path, by design.

## Timing model

- **Polling is concurrent per tick** (`poller.pollOnce`): cycle cost = slowest
  device, not the sum. Worst cases to keep in mind: off TV ≈ 4 s REST connect
  timeout; unreachable WiZ ≈ 3.5 s (ARP miss → 1.5 s broadcast discovery → 2 s
  rpc). The configured `poll_interval` is the active cadence (default 45 s).
  After 2m without app activity or device changes it backs off to 5m → 10m →
  30m → 1h → 6h. UI pointer/keyboard activity is signalled at most every 30s;
  foreground/manual refresh performs a one-shot hardware poll and resets the
  cadence. Refreshes arriving during or within 5s of a completed cycle reuse
  that result, so startup and multiple clients cannot cause back-to-back polls.
  The poll coordinator serializes cycles, so a device is never polled twice at
  once.
- **Server WS writes are bounded** (`wsWriteTimeout`, 10 s): a phone that
  suspended mid-connection leaves a half-open socket; the deadline drops it at
  the next event instead of waiting ~15 min for kernel TCP timeout.

## Browser socket lifecycle (store.ts)

Rules — all three exist to fix real bugs, don't relax them:
1. **One socket at a time.** `openSocket` refuses while one is live/connecting
   (a second socket duplicates events and leaks a server subscription).
2. **Handlers identity-check** (`ws === sock`) so a replaced socket's late
   onclose/onerror can't null the live socket or spawn competing reconnects.
3. **`disconnect()` nulls `ws` before closing** (neutralizes handlers); a token
   change is `disconnect()` + `connect()` (`App.svelte saveToken`) because rule
   1 means `connect()` alone won't replace a live socket.

Reconnect backoff: 1 s ×2 → cap 15 s; reset on open and on `resume()`.
Foreground signals (`visibilitychange`, bfcache `pageshow`, `online`) are
coalesced before `resume()` = bounded REST `refresh()` + eager reconnect;
`pagehide` closes the old socket. Commands never wait for this path and cached
cards stay rendered while it runs. The initial device-list request times out
after 8 s so a half-reachable LAN server cannot hold the loading state forever.
A newer refresh aborts and supersedes the previous one; only its result may
update UI state, and a non-auth REST failure never demotes an already-open live
WebSocket to offline.

## TV socket lifecycle (samsung)

- One remote-control WS, kept open while the TV is on; `Poll → ensureEvents`
  redials **only with a cached token** (an unpaired dial pops the on-screen
  Allow prompt — never from background). `drainWS` pumps reads (TV pings,
  IME events, token refresh); a stale socket gets exactly one redial on the
  next write.
- **Power:** On = Wake-on-LAN, **MAC only — no IP needed**, which is why the
  TV's `State.Online` is *always* true (off ≠ offline; tying Online to IP
  resolution would disable the power toggle once the off TV's ARP entry
  expires). Off checks `PowerState` first because `KEY_POWER` is a toggle.
  `powerGrace` (10 s) trusts the last command over polled state during the
  power transition.
- **Key-hold safety:** every `Press` is guaranteed a `Release` — explicit,
  superseded by the next key, or the `holdMax` (60 s) watchdog; the UI also
  releases eagerly on pointerup / pointercancel / hidden tab / pagehide.

## Caching model — three layers, who owns what

| Layer | What | Invalidation |
| --- | --- | --- |
| `localStorage` | device list (instant paint on cold resume), token, favourites, expanded | overwritten by next `refresh()` / WS event |
| Service worker (secure contexts only) | complete shell + hashed JS/CSS, cache `setu-shell-<content id>` | `vite.config.ts` injects boot assets and hashes all emitted content plus the worker template → any changed UI/icon/splash/cache logic = new cache, `activate` deletes old Setu caches |
| HTTP headers (plain-HTTP LAN — **no SW possible there**) | HTML + `service-worker.js` → `no-cache`; `/assets/*` → `immutable, max-age=1y` (Vite content-hashes names) | entrypoints revalidate; asset filename hash changes per build |

Service-worker fetch rules: never touches `/api` or `/ws`; controlled
navigations return the versioned cached canonical `/` response immediately so
an OS-evicted PWA never waits on a stalled LAN request before it can paint.
Hashed JS/CSS are pre-cached during install, and the asset branch caches **only
`res.ok` non-HTML** responses — the Go server answers any unknown non-asset path
with `200` plus `index.html` (SPA fallback), while missing `/assets/*` paths
return `404` plus `no-store`; this keeps HTML from ever masquerading as stale
JS/CSS. A newly activated worker is adopted on the next natural navigation; it
never force-reloads a client that may be suspended in the background.
`/index.html` is also served directly (never redirected), but is not used as a
cache key: WebKit rejects redirected cached navigation responses.

## Addressing invariants

- MAC is identity. Each device caches its resolved IP and **invalidates it on
  any send failure** → next call re-resolves: ARP table → brand discovery
  (WiZ UDP broadcast) → config `ip` hint.
- Samsung WoL bypasses resolution entirely (broadcast by MAC) — don't gate any
  power-on path on `resolveIP`.
