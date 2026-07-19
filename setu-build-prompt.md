# Build Prompt — "Setu" (Phase 1: Design & Scaffold, Go-only)

> Paste this entire document as the task for an AI coding agent (e.g. Claude Code).
>
> **Scope of this phase: design and scaffold only.** Produce a complete, compiling, runnable skeleton with every architectural seam in place plus a documented device template. **Do NOT implement real device protocols yet** — actual devices (WiZ, etc.) will be added one-by-one in later steps, organised by **brand** and **model**.

## Name

The project is **Setu** (Sanskrit सेतु, "bridge") — it bridges local IoT devices to a simple web UI. Use `setu` as the repo name, Go module name, and binary name.

## Role & Objective

You are a senior Go and frontend engineer. Scaffold a **lightweight self-hosted home automation server** ("Setu") that runs on a low-resource device (a MikroTik RouterOS container or an OpenWrt router, ~256–512 MB RAM). It will control local IoT devices and serve a small, fast, app-like control UI for mobile and desktop browsers.

This is **not** a large server. It is a tiny, focused server on an IoT-class device. Keep it **lightweight, static, simple, and free of over-engineering.**

The whole thing is **Go-only**: a single static Go binary serves the embedded frontend, the JSON API, and the WebSocket. **No NGINX, no separate web server, no process supervisor.**

## Core Principles (non-negotiable)

1. **Lightweight & simple above all.** Standard library wherever possible; no heavy frameworks. Small memory, low CPU, clean structure. **No over-complexity** — never add layers, abstractions, or generality the current scope doesn't need.
2. **Idiomatic Go — composition, not inheritance.** Struct embedding for shared code; each device is implemented explicitly in its own brand/model package.
3. **Interfaces only where they earn their place.** Use them at the real seams: device **capabilities** (new device types), **address resolution** (MAC→IP strategies), and a **bridge/transport seam** so a second front-end protocol (e.g. Apple HomeKit) can be added later without touching device code. Do **not** add interfaces for single-implementation plumbing.
4. **Configuration is data, not behavior.** Behavior lives in code; config supplies only instance data. Keep it minimal.
5. **MAC is the primary device identity; IP is resolved at runtime.** (See "Device Addressing".)
6. **Event-driven core from day one.** Commands in, state-change events out, via an internal event bus (Go channels). Powers live UI now and a future automation engine. **Do not build the automation engine yet** — leave a clean, documented seam.
7. **Single Go binary.** It serves static (via `embed`), `/api`, and `/ws`. It listens directly — no reverse proxy.

## Serving & Listener (Go-only)

- One `net/http` server with one mux serves:
  - `/` + assets → the **embedded** Svelte build (`//go:embed` of `web/dist`, served via `http.FileServer` with SPA fallback to `index.html`).
  - `/api/*` → JSON API.
  - `/ws` → WebSocket (use a minimal WS library, e.g. `coder/websocket`; justify it).
- **Listener** (configurable, single field):
  - Default: **TCP** `listen: ":8080"` — for normal browser/PWA access. Bind to a trusted interface; secure via VPN (WireGuard / Tailscale) or firewall. Never expose raw to the internet.
  - Optional: **Unix socket** `listen: "unix:/run/setu.sock"` — for zero-open-port setups accessed via SSH tunnel (laptop-friendly; phones need a tunnel app).
- Bearer-token auth (from config) on `/api` and `/ws`. Graceful shutdown via `context` + `os/signal`.

> **PWA secure-context note (document in README):** service workers / PWA install require a **secure context — HTTPS or `localhost`**. Plain `http://<lan-ip>:8080` will block PWA features. No proxy needed — Go does TLS natively (`ListenAndServeTLS`). Easiest options: access via `localhost` (SSH tunnel), **Tailscale** (auto HTTPS on `*.ts.net`), or a self-signed cert (trusted once).

## Device Addressing (MAC-primary, IP resolved at runtime)

IoT devices keep a **fixed MAC** but their **IP can change** (DHCP). So:

- In config, **`mac` is the required, stable identifier**; device IPs are resolved at runtime.
- You cannot address a device by MAC at the application layer (MAC is Layer 2). At runtime, **resolve the current IP from the MAC**, cache it, and **re-resolve on any send/connection failure**.
- Behind one interface:

```go
type Resolver interface {
    Lookup(mac string) (net.IP, error)
}
```

  - **ARP table (default impl — build now):** read `/proc/net/arp` (or `ip neigh`) and match the MAC. Requires host networking.
  - **Per-device discovery (per brand):** e.g. WiZ replies to a UDP broadcast with its MAC + current IP.
  - **DHCP lease table (future impl — do not build):** OpenWrt `/tmp/dhcp.leases`; RouterOS via API — slots behind the same interface.

## Backend Architecture (Go)

Layers, top to bottom:

**HTTP server** — `net/http`, one mux, listener as above. Routes:
- `GET /api/devices` → list devices with capabilities + current state. (Returns an empty list until devices are added.)
- `POST /api/devices/{id}/command` → uniform, device-agnostic body:
  - `{"action":"on"}` / `{"action":"off"}`
  - `{"action":"set_brightness","value":70}`
  - `{"action":"set_color","value":{"r":255,"g":120,"b":0}}`
- `GET /ws` → WebSocket; pushes state-change events.

**API layer** — HTTP → manager calls; capability checks via type assertions; clean JSON errors:
```go
if d, ok := dev.(Dimmable); ok {
    err = d.SetBrightness(req.Value)
} else {
    http.Error(w, "device does not support brightness", http.StatusBadRequest)
}
```

**Device manager / registry** — instantiated devices keyed by `id`; routes commands; subscribes to the event bus; exposes a state snapshot. Must work correctly with **zero devices**.

**Event bus** — tiny channel-based pub/sub. Devices + poller publish `StateChanged`; manager + WS hub subscribe. The automation engine subscribes here later (not now).

**Capability interfaces** (small, one concern each):
```go
type Switchable   interface { On() error; Off() error }
type Dimmable     interface { SetBrightness(pct int) error } // 0–100
type ColorControl interface { SetColor(c Color) error }

type Device interface {
    ID() string
    Name() string
    Brand() string
    Model() string
    MAC() string
    Capabilities() []string   // e.g. ["switch","brightness","color"]
    State() State
}
```

**Device packages — organised by brand → model.** Layout: `internal/devices/<brand>/`. Each brand package holds a **shared brand base** (the brand's transport/protocol, embedded) and **one type per model** (each implementing whichever capability interfaces that model supports — different models of the same brand can behave differently). A `(brand, model)` → constructor **factory** wires config entries to the right type.

**Device template (this phase):** provide `internal/devices/example/` — a compiling, documented **template** package showing the exact pattern: a brand base struct, an embedded model type, capability methods as stubs, resolver usage, and how it registers in the factory. This is the blueprint real devices will follow. **Do not implement a real device's protocol yet.**

**State poller** — interval-based; emits `StateChanged` only on change. Wire it generically; it operates over whatever devices the registry holds.

## Configuration (minimal — data only)

`config.yaml` (or `config.json` with stdlib to avoid a YAML dependency — pick one):
```yaml
listen: ":8080"               # TCP; or "unix:/run/setu.sock" for tunnel-only
auth:
  token: "CHANGE_ME"          # bearer token for /api and /ws
poll_interval: 45s
devices: []                   # empty for now; real devices added later, e.g.:
  # - id: living_light
  #   brand: wiz
  #   model: color_bulb
  #   name: "Living Room"
  #   mac: "a8:bb:50:xx:xx:xx" # PRIMARY identity (stable)
```
The `(brand, model)` factory maps each entry to its package type. Adding a device later = implement the brand/model package + add a config entry + register one factory line.

## Frontend (Svelte 5 + Vite + Tailwind + PWA) — shell this phase

- **Svelte 5 + Vite + Tailwind CSS**; output is **static** (`web/dist`), **embedded into the Go binary**.
- **PWA**: web-app manifest + service worker (cache the app shell) → installable, fullscreen, app-like.
- **Low memory & resilient to backgrounding** (critical — build now):
  - Keep the JS heap small (this is why Svelte, not React).
  - Persist UI state (current view + last-known device states) to `localStorage`/IndexedDB.
  - On `visibilitychange` resume → re-fetch `/api/devices` and reconnect the WebSocket, so it "just works" after the mobile OS kills a backgrounded tab.
  - Clean up listeners/intervals; no leaks.
- **Reusable components** (composition), built now and rendered from device data: `DeviceCard.svelte`, `Toggle.svelte`, `BrightnessSlider.svelte`, `ColorPicker.svelte`. The dashboard renders from `GET /api/devices` and shows a clean **empty-state** when there are none.
- **Live updates** via `/ws`; optimistic UI on command, reconciled by events.
- **Look & feel:** fresh, colorful, smooth — clean palette, rounded cards, soft shadows, Svelte built-in transitions. Mobile-first, responsive. **No heavy UI kit.**
- Call the API at **relative paths** (same origin); send the bearer token.

## Repository Layout (produce exactly this shape)

```
setu/
├── cmd/setu/main.go               # load config, wire deps, embed+serve, listen (tcp/unix)
├── internal/
│   ├── api/                       # http handlers, ws hub, auth, routing, static-embed serve
│   ├── manager/                   # registry, command routing, state snapshot
│   ├── events/                    # event bus (channels) + event types
│   ├── device/                    # capability interfaces + Device + Color/State
│   ├── resolver/                  # Resolver interface + ARP impl (DHCP-lease: future)
│   ├── devices/
│   │   └── example/               # TEMPLATE package (brand base + model stub + factory reg)
│   └── config/                    # config struct, loader, (brand,model) factory
├── web/                           # Svelte + Vite + Tailwind source (built → web/dist, embedded)
│   ├── src/
│   │   ├── App.svelte
│   │   ├── lib/components/        # DeviceCard, Toggle, BrightnessSlider, ColorPicker
│   │   ├── lib/api.ts             # fetch wrapper + token
│   │   ├── lib/store.ts           # state + localStorage persistence
│   │   └── service-worker.ts
│   ├── manifest.webmanifest
│   ├── package.json
│   ├── vite.config.ts
│   └── tailwind.config.js
├── Dockerfile                     # multi-stage: build web → go build (embed) → tiny final image
├── config.yaml
├── Makefile                       # build web → build/cross-compile go (linux/arm64 + host)
├── go.mod
└── README.md
```

## Deliverables (this phase)

1. A complete, **compiling, runnable** skeleton per the layout. `docker run` it, open the browser, see the dashboard's clean empty-state.
2. All seams wired: capability interfaces, `Device`, manager, event bus, `Resolver` (interface + ARP impl), API routes, config loader + `(brand,model)` factory, generic state poller.
3. `internal/devices/example/` **template** package documenting exactly how to add a real device (brand base + model + capabilities + resolver use + factory registration). No real device protocol implemented.
4. Frontend **shell**: Svelte + Tailwind PWA with the reusable components, resume-handling, empty-state — embedded into the binary.
5. `Dockerfile` (multi-stage) → small image (scratch/distroless) containing just the binary + config. Document that it needs **host networking** (LAN access, broadcast, ARP, future mDNS) and a volume for config (and the unix socket, if used).
6. `Makefile`: build frontend, embed, cross-compile Go for `linux/arm64` (MikroTik) and host.
7. `README.md`: a short "how it fits together"; build/run; config reference; **MAC-primary addressing** + resolver; the **listener options** (TCP vs unix socket) and the **PWA secure-context** note (HTTPS/localhost; Go TLS; Tailscale/tunnel/self-signed); **how to add a device by brand/model** (the core next step); RouterOS/OpenWrt deployment notes.

## Constraints / Non-Goals

- **Go-only** — no NGINX, no separate web server, no process supervisor.
- **Design/scaffold only** — do not implement real device protocols this phase.
- Device behavior is **hand-coded per brand/model package**, never config-driven.
- **No automation/rules engine yet** — only the event-bus seam + a note on where it plugs in.
- **No HomeKit yet** — but keep the bridge/transport seam so it can be added at the interface layer later without touching device code.
- No heavy Go frameworks, no heavy frontend UI kits; minimal dependencies (justify each).
- **No over-engineering.** When in doubt, choose the simpler design.

## Quality Bar

Idiomatic, well-commented Go; typed errors; graceful shutdown. A frontend that already looks like a polished native app (even with no devices) within a small memory budget. The `example` template + `Resolver` + capability interfaces should read cleanly enough to be the obvious blueprint for every device added next.
