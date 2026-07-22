# web — Svelte 5 PWA (embedded)

`web/` · the frontend, built to `web/dist` and embedded into the Go binary by `embed.go`.

## Stack
- Svelte 5 (runes) + Vite + Tailwind v3. Static output, no SSR. ~24 KB gzipped JS.

## Key files
- `index.html` — also carries the pre-app **splash**: an inline (framework-free) animated loader shown from the first paint until the processed app CSS and Svelte shell are ready, plus a static "Can't reach Setu" card a watchdog reveals if the app never mounts. `main.ts` loads `app.css` asynchronously so production CSS cannot block that first paint; mounted-but-offline shows the richer in-app screen instead.
- `src/App.svelte` — shell: header, device grid, empty state, token modal, resume handling.
- `src/lib/api.ts` — fetch wrapper + bearer token; `wsURL()`.
- `src/lib/store.ts` — stores + `localStorage` cache + optimistic `command()` + auto-reconnecting WebSocket.
- `src/lib/haptics.ts` / `src/lib/wakelock.ts` — feature-detected, fail-soft progressive enhancements (vibration; screen wake lock while a remote is open). Mirror this pattern for any new optional capability.
- `src/lib/components/` — device controls plus `Scenes`, `Automations` (Settings actions + bounded editors, including acyclic nested automation calls), and `BackupRestore` (select export sections; one-click restore of every included section). Refresh and search stay in the top header; room filtering and the Settings-launched arrange mode live in `App.svelte`, with a visible header **Done** action while arranging.
- `src/lib/backup.ts` — the versioned single-file backup contract and rollback-aware restore. It never exports the access token or device cache.
- `public/` — `manifest.webmanifest` (incl. `shortcuts` → `/?do=all_on|all_off`), `service-worker.js`, icons (`icon.svg` + maskable `icon-{180,192,512}.png`), iOS `splash-*.png`. `embed.go` — `//go:embed dist`.

## Rules
- Cards render **from `capabilities`** — no per-device markup. A new backend capability lights up its control automatically.
- **Theme follows the OS** (light/dark via `prefers-color-scheme` — no toggle, no JS, no flash). Style with the theme-aware tokens from `app.css` + `tailwind.config.js`: `ink` (neutral text/fills/borders, always with an opacity, e.g. `text-ink/70`, `bg-ink/5`, `border-ink/10`), `panel` (solid surface), and the `--card-shadow` var. Vivid accents (indigo/fuchsia/emerald/rose) stay literal. **Don't hardcode `white`/`black`/`slate` for neutrals;** reach for `dark:` only for the rare accent the tokens can't express.
- **UI-only prefs stay client-side:** favourites, scenes, room assignments and manual card order live in `localStorage`. Automations are operational server state and persist through the automation API instead.
- Same-origin relative calls; token from `localStorage`; `?token=` on the WebSocket.
- Resilient to mobile backgrounding: persist state, coalesce hardware refresh + reconnect on `visibilitychange` / `pageshow` / `online`, and close the socket on `pagehide`; clean up listeners. The refresh button in the top header performs the same one-shot hardware poll. Pointer/keyboard activity sends a throttled lightweight cadence hint, never a device poll.
- **Socket rules** (store.ts — see `docs/runtime.md`): one socket at a time, handlers identity-check `ws === sock`, token change = `disconnect()` + `connect()`.
- Device commands use a tiny per-device promise queue so hardware execution preserves tap order; a client generation still prevents an intermediate response from replacing newer optimistic state. Reconciled command errors and newer WS/REST state are authoritative over the pre-command snapshot. Settings focuses its dialog container first (not the token field), avoiding an unsolicited mobile keyboard, and explicitly wraps the initial forward/backward Tab.
- **Continuous controls debounce** (~120 ms): sliders *and* the native color input — anything firing `input` per pixel of drag must not become a command per pixel.
- Service worker: `vite.config.ts` injects every hashed JS/CSS boot asset and an id derived from all emitted content (including stable icons/splashes). Controlled navigations use the canonical cached `/` shell immediately (never a redirected `/index.html` response), deploys self-evict old Setu caches, and updates take effect on the next natural launch without force-reloading a backgrounded client. The raw dev worker is inert; the production runtime asset branch refuses non-OK / HTML responses (the server's SPA fallback would poison asset URLs).

## Build / dev
- `make web` (build), `npm test` (service-worker cache regression), or `npm run dev` (Vite dev server, proxies `/api` + `/ws` → `:8080`).
- `dist/.gitkeep` keeps `dist/` tracked; Vite empties `dist/` on build, the Makefile restores the marker.
