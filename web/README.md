# `web`

Svelte 5 PWA built to `web/dist` and embedded by `embed.go`. Static output only;
no SSR or UI framework.

## Main ownership

- `index.html`, `main.ts`, `app.css`: first-paint splash, mount, theme tokens.
- `App.svelte`: shell, header actions, card layout, settings, resume lifecycle.
- `lib/api.ts`: API types, authenticated fetch, WebSocket URL.
- `lib/store.ts`: cached devices, optimistic commands, per-device queues,
  reconnect, and local UI state.
- `lib/backup.ts`: versioned selective backup and rollback-aware restore.
- `lib/components`: capability-driven controls, device management,
  diagnostics, rooms, scenes, automations, people, and backup/restore.
- `public/service-worker.js`: production app-shell cache only.

## State boundaries

Server-owned:

- device inventory and live state;
- automations;
- people: accounts, roles, and per-device access.

`localStorage`:

- access token and last device snapshot;
- favourites, rooms, manual scenes, card order/expanded state;
- system/light/dark theme choice.

Resetting UI preferences preserves the access token. Backup selection can
include both server and client sections, but never exports the access token.

## Runtime rules

- Cards render existing controls from reported capabilities; never add
  brand-specific card markup.
- A brand-new capability still needs API types, normalization, optimistic
  updates, a `DeviceCard` group/control, automation/backup filtering, and tests.
- Commands queue per device and continuous inputs debounce before sending.
- Only one WebSocket may be active/connecting. Old handlers identity-check
  their socket; token changes disconnect before reconnecting.
- Foreground/online events coalesce refresh and reconnect; `pagehide` closes
  the socket. Newer refreshes supersede older ones, and a refresh never
  overwrites state that arrived while it was in flight.
- Optional browser features such as vibration and wake lock are feature-tested
  and fail soft.
- `GET /api/session` says what the signed-in account may do, and the UI hides
  the rest. This is presentation only: the server re-checks every request, so an
  unanswered session (an older or unreachable Setu) shows everything rather than
  locking someone out of what they are entitled to.
- A generated token is shown once, in the response that created or rotated it.
  Any screen that receives one must display it before moving on.
- Use `ink`, `panel`, and `--card-shadow` theme tokens for neutral surfaces.
  The user can follow the OS or force light/dark; do not hardcode neutral
  white/black/slate colors.

Cross-package lifecycle details are in
[`docs/runtime.md`](../docs/runtime.md).

## Service worker

The build stamps a content-derived cache ID and precaches emitted boot assets.
It never caches `/api` or `/ws`. Controlled navigations use the cached canonical
`/` shell; failed/HTML asset responses are never stored as JS/CSS. Old Setu
caches are removed on activation without force-reloading a suspended client.

The worker is inert during Vite development and is registered only in secure
contexts.

## Commands

From `web/`:

```sh
npm run dev
npm test
npm run check
npm run build
```

Vite dev proxies `/api` and `/ws` to `localhost:8080`. `dist/.gitkeep` lets Go
compile before the first frontend build.
