# Setu — सेतु

> A tiny, self-hosted bridge from your local IoT devices to a fast, app-like web UI.

**Setu** (Sanskrit *सेतु*, "bridge") is a lightweight home-automation server designed to
run on low-resource hardware — a MikroTik RouterOS container, an OpenWrt router, a Raspberry
Pi (~256–512 MB RAM). It controls local devices and serves a small PWA control panel for
phones and desktops.

It is a **single static Go binary**. That one binary serves the embedded web app, the JSON
API, and a WebSocket for live updates. No NGINX, no separate web server, no process
supervisor.

> **Status — Phase 1 (scaffold).** Every architectural seam is in place and the app runs,
> but no real device protocols are implemented yet. Devices are added one at a time, by
> brand and model, following the documented `example` template. See
> [Adding a device](#adding-a-device).

---

## How it fits together

```
                         ┌───────────────────────── setu (one Go binary) ─────────────────────────┐
   browser / PWA  ◀──────┤  net/http (one mux, one listener: TCP or unix socket)                   │
        │  HTTPS/tunnel  │   ├── /            → embedded Svelte build  (web/dist via //go:embed)    │
        │               │   ├── /api/*       → JSON API        ┐  bearer-token auth                │
        │  WebSocket     │   └── /ws          → live events     ┘                                   │
        └───────────────┤                                                                          │
                         │   api → manager (registry, command routing, state snapshot)             │
                         │            │              ▲                                              │
                         │   commands │              │ state-change events                         │
                         │            ▼              │                                              │
                         │        devices ──────▶ event bus (Go channels) ◀── state poller         │
                         │            │                       ▲                                     │
                         │   MAC→IP   ▼                       └── (future: automation engine)      │
                         │        resolver (ARP table → IP)                                        │
                         └──────────────────────────────────────────────────────────────────────────┘
```

**Event-driven core.** Commands flow *in* (HTTP → manager → device); state-change events
flow *out* (device/poller → event bus → WebSocket → browser). The event bus is a tiny
channel-based pub/sub. This powers the live UI today and is the seam where an automation
engine plugs in later — without touching device code.

**Interfaces only at the real seams** (idiomatic Go: composition, not inheritance):

| Seam | Interface | Why |
| --- | --- | --- |
| Device capabilities | `Switchable`, `Dimmable`, `ColorControl` (in `internal/device`) | New device features without changing existing devices; the API discovers support via type assertions |
| Address resolution | `Resolver` (in `internal/resolver`) | Swap MAC→IP strategies (ARP now; DHCP leases / per-brand discovery later) |
| Front-end protocol | the `api` package vs. the manager + event bus | A second protocol (e.g. an Apple HomeKit bridge) can be added beside `api`, talking to the same manager/bus, with no device-code changes |

### Repository layout

```
setu/
├── cmd/setu/main.go        # composition root: load config, wire deps, register devices, serve
├── internal/
│   ├── api/                # http handlers, ws hub, bearer auth, routing, static embed serving
│   ├── manager/            # device registry, command routing, event-driven state snapshot, poller
│   ├── events/             # channel-based pub/sub bus + event types
│   ├── device/             # capability interfaces + Device + Color/State
│   ├── resolver/           # Resolver interface + ARP implementation (DHCP-lease impl: future)
│   ├── devices/
│   │   └── example/        # TEMPLATE device package — the blueprint for real devices
│   └── config/             # config schema + loader + (brand,model) factory
├── web/                    # Svelte 5 + Vite + Tailwind PWA (built → web/dist, embedded)
│   ├── embed.go            #   //go:embed of web/dist
│   ├── src/                #   App.svelte, lib/{api,store}.ts, lib/components/*
│   └── public/             #   manifest.webmanifest, service-worker.js, icons
├── config.yaml             # your configuration
├── Dockerfile              # multi-stage: build web → build Go (embed) → distroless
├── Makefile
└── go.mod
```

---

## Build & run

### With Docker (recommended)

Setu needs the **host network** to reach LAN devices and read the ARP table.

```sh
make docker                       # or: docker build -t setu .
docker run --rm --network host \
  -v "$PWD/config.yaml:/etc/setu/config.yaml:ro" \
  setu
```

Open `http://<host>:8080`, enter your `auth.token`, and you'll see the empty dashboard.

### From source

Requires Go 1.23+ and Node 20+.

```sh
make build        # builds the frontend, then the binary into ./bin/setu
make run          # build + run with ./config.yaml
```

`make build-arm64` cross-compiles a static `linux/arm64` binary for a MikroTik/OpenWrt/Pi.

### Hot-reload development

```sh
# terminal 1 — backend
go run ./cmd/setu -config config.yaml
# terminal 2 — frontend (Vite proxies /api and /ws to :8080)
cd web && npm install && npm run dev
```

> `go build ./...` works on a fresh checkout even before the frontend is built: the embed
> contains only a `.gitkeep`, and the server serves a small built-in placeholder page until
> you run `make web`.

---

## Configuration

`config.yaml` is **data only** — device *behaviour* lives in code, never in config.

```yaml
listen: ":8080"          # TCP; or "unix:/run/setu.sock" for tunnel-only access
auth:
  token: "CHANGE_ME"     # bearer token required on /api and /ws — CHANGE THIS
poll_interval: 5s        # how often to re-read device state (Go duration string)
devices: []              # empty for now; see "Adding a device"
```

| Key | Meaning |
| --- | --- |
| `listen` | `":8080"` (TCP, all interfaces), `"127.0.0.1:8080"` (loopback only), or `"unix:/run/setu.sock"` |
| `auth.token` | Bearer token for `/api` and `/ws`. The server refuses to start with an empty token and warns if it's still `CHANGE_ME`. |
| `poll_interval` | Duration like `5s`, `500ms`, `1m`. `0` disables polling. |
| `devices[]` | One entry per device: `id`, `brand`, `model`, `name`, `mac` (**required**, primary identity), `ip` (optional hint). |

### HTTP / WebSocket API

All endpoints require `Authorization: Bearer <token>` (the WebSocket also accepts `?token=`).

| Method & path | Body | Result |
| --- | --- | --- |
| `GET /api/devices` | — | `[]DeviceView` (id, name, brand, model, mac, capabilities, state) — `[]` when none |
| `POST /api/devices/{id}/command` | `{"action":"on"}` / `{"action":"off"}` | updated `DeviceView` |
| | `{"action":"set_brightness","value":70}` | (0–100) |
| | `{"action":"set_color","value":{"r":255,"g":120,"b":0}}` | |
| `GET /ws` | — | WebSocket; pushes `{type,device_id,state}` (`snapshot` on connect, then `state_changed`) |

The command body is **uniform and device-agnostic**. The API checks capability support with
type assertions and returns `400` if a device doesn't support an action (e.g. brightness on a
plain switch), `404` for an unknown device, `502` for a device/IO failure.

---

## Device addressing — MAC is primary, IP is resolved at runtime

IoT devices keep a fixed **MAC** but their **IP can change** (DHCP). So in Setu:

- `mac` is the **required, stable** identity in config; `ip` is only an optional hint/fallback.
- You can't address a device by MAC at the application layer (MAC is Layer 2). At runtime Setu
  resolves the current IP from the MAC, **caches** it, and **re-resolves on send failure** (the
  device may have a new lease). The `example` template shows this pattern (`resolveIP` /
  `invalidateIP`).

Resolution sits behind one interface:

```go
type Resolver interface {
    Lookup(mac string) (net.IP, error)
}
```

- **ARP table** — the default, built now. Reads `/proc/net/arp` and matches the MAC. Requires
  host networking and only knows devices the host has talked to recently.
- **Per-device discovery** — *later, per brand.* E.g. WiZ answers a UDP broadcast with its MAC
  + current IP; that brand's package will implement discovery behind the same interface.
- **DHCP lease table** — *future.* OpenWrt `/tmp/dhcp.leases`, RouterOS via API. Same interface.

---

## Listener options

One configurable field, `listen`:

- **TCP (default), `":8080"`** — normal browser / PWA access. **Bind to a trusted interface**
  and secure it with a VPN (WireGuard / Tailscale) or a firewall. **Never expose Setu raw to
  the internet.**
- **Unix socket, `"unix:/run/setu.sock"`** — zero open ports; reach it over an SSH tunnel
  (`ssh -L 8080:/run/setu.sock user@router`). Laptop-friendly; phones need a tunnel app.

Graceful shutdown is handled on `SIGINT`/`SIGTERM`.

### PWA & the secure-context requirement

Service workers and "Add to Home Screen" (install, fullscreen, offline app shell) only work in
a **secure context** — HTTPS **or** `localhost`. Plain `http://<lan-ip>:8080` loads fine but
the browser blocks PWA features. No proxy is needed — Go does TLS natively
(`ListenAndServeTLS`). Easiest options:

- **`localhost`** via an SSH tunnel — counts as secure, nothing else needed.
- **Tailscale** — gives automatic HTTPS on your `*.ts.net` name.
- **Self-signed cert** — trust it once on each device.

(Phase 1 serves plain HTTP; the listener is the place to add TLS when you want installable PWA.)

---

## Adding a device

This is the core next step, and the whole architecture is built around making it small and
local. Each device lives in its own package, organised **by brand → model**. Use
[`internal/devices/example`](internal/devices/example/example.go) as the blueprint — it is a
fully-commented, compiling template (a brand `base` with the transport, an embedded model type,
capability methods, resolver usage, and factory registration).

1. **Copy the template:** `internal/devices/example/` → `internal/devices/<brand>/`. Set the
   `Brand` / `Model` constants.
2. **Implement the transport** in the brand `base` (`send`) — the UDP/TCP/HTTP protocol. On a
   network error, call `invalidateIP()` so the next call re-resolves the MAC.
3. **Per model**, define a type embedding `base` and implement `Model()`, `Capabilities()`, and
   only the capability interfaces that model supports (`Switchable`, `Dimmable`,
   `ColorControl`). Different models of the same brand can differ. Update the compile-time
   `var _ device.X = (*T)(nil)` assertions.
4. **Implement `Poll()`** to read real hardware state (or omit `Pollable` entirely).
5. **Export** `New` (a `config.Constructor`) and `Register(*config.Factory)`.
6. **Register it** with one line in `cmd/setu/main.go`:
   ```go
   wiz.Register(factory)   // next to example.Register(factory)
   ```
7. **Add a config entry:**
   ```yaml
   devices:
     - id: living_light
       brand: wiz
       model: color_bulb
       name: "Living Room"
       mac: "a8:bb:50:11:22:33"   # primary identity
       ip: 192.168.1.50           # optional hint
   ```

The frontend needs **no changes** — `DeviceCard` renders the right controls from the device's
reported `capabilities`.

### Not in this phase (by design)

- No real device protocols yet (the `example` package is a stub template).
- No automation/rules engine — only the event-bus seam it will subscribe to.
- No HomeKit — but the front-end-protocol seam (the `api` package over the manager/bus) keeps it
  addable later without touching device code.

---

## Deployment notes

**General.** Setu must share the **host network** (LAN reachability, ARP, and future UDP
broadcast / mDNS). Mount your `config.yaml` (and a writable dir if you use a unix socket).

**MikroTik RouterOS (container).** Build the `linux/arm64` (or your arch) image, import it into
the `container` package, attach it to a veth on your LAN bridge, and mount a config volume.
Cross-compile locally with `make build-arm64` if building off-device.

**OpenWrt.** Drop the static `linux/<arch>` binary on the router (e.g. `/usr/bin/setu`), add a
small procd/init script pointing at `/etc/setu/config.yaml`, and (future) read leases from
`/tmp/dhcp.leases` via a DHCP resolver. The binary is fully static (`CGO_ENABLED=0`), so it has
no libc dependency.

---

## Why these dependencies?

Kept deliberately minimal (standard library does the rest):

- **`github.com/coder/websocket`** — small, context-aware, zero-dependency WebSocket library.
- **`gopkg.in/yaml.v3`** — human-friendly config with comments and `5s`-style durations.
- **Frontend:** Svelte 5 (tiny runtime → small JS heap, important on mobile), Vite, Tailwind.
  No heavy UI kit. The production bundle is ~24 KB gzipped.

## License

GPL-3.0 — see [LICENSE](LICENSE).
