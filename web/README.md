# web — Svelte 5 PWA (embedded)

`web/` · the frontend, built to `web/dist` and embedded into the Go binary by `embed.go`.

## Stack
- Svelte 5 (runes) + Vite + Tailwind v3. Static output, no SSR. ~24 KB gzipped JS.

## Key files
- `src/App.svelte` — shell: header, device grid, empty state, token modal, resume handling.
- `src/lib/api.ts` — fetch wrapper + bearer token; `wsURL()`.
- `src/lib/store.ts` — stores + `localStorage` cache + optimistic `command()` + auto-reconnecting WebSocket.
- `src/lib/components/` — `DeviceCard`, `Toggle`, `BrightnessSlider`, `ColorPicker`, `ColorTempSlider`, `ScenePicker`, `SceneSpeedSlider`, `VolumeControl` (real level + true mute state), `RemotePad` (tap + press-and-hold on every button), `TextEntry` (send text; mirrors the TV's focused field live), `Favorites`.
- `public/` — `manifest.webmanifest`, `service-worker.js`, icons. `embed.go` — `//go:embed dist`.

## Rules
- Cards render **from `capabilities`** — no per-device markup. A new backend capability lights up its control automatically.
- **Theme follows the OS** (light/dark via `prefers-color-scheme` — no toggle, no JS, no flash). Style with the theme-aware tokens from `app.css` + `tailwind.config.js`: `ink` (neutral text/fills/borders, always with an opacity, e.g. `text-ink/70`, `bg-ink/5`, `border-ink/10`), `panel` (solid surface), and the `--card-shadow` var. Vivid accents (indigo/fuchsia/emerald/rose) stay literal. **Don't hardcode `white`/`black`/`slate` for neutrals;** reach for `dark:` only for the rare accent the tokens can't express.
- **UI-only prefs stay client-side:** favourites (saved color / white-temp / scene presets) live in `localStorage` per device — no backend state, keeping the server lightweight. They're per-browser.
- Same-origin relative calls; token from `localStorage`; `?token=` on the WebSocket.
- Resilient to mobile backgrounding: persist state, re-fetch + reconnect on `visibilitychange` / `online`; clean up listeners.

## Build / dev
- `make web` (build) or `npm run dev` (Vite dev server, proxies `/api` + `/ws` → `:8080`).
- `dist/.gitkeep` keeps `dist/` tracked; Vite empties `dist/` on build, the Makefile restores the marker.
