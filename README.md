# Setu — सेतु

> A tiny, self-hosted bridge from your local IoT devices to a fast, app-like web UI.

**Setu** (Sanskrit *सेतु*, "bridge") is a lightweight home-automation server designed to
run on low-resource hardware — a MikroTik RouterOS container, an OpenWrt router, a Raspberry
Pi (~256–512 MB RAM). It controls local devices and serves a small PWA control panel for
phones and desktops.

It is a **single static Go binary**. That one binary serves the embedded web app, the JSON
API, and a WebSocket for live updates. No NGINX, no separate web server, no process
supervisor.

> **Status.** The full architecture is in place, plus two real device integrations —
> **Philips WiZ** bulbs and **Samsung Tizen** TVs (see [Supported devices](#supported-devices)).
> Add more one at a time, by brand and model, following the `example` template and the two
> real packages. See [Adding a device](#adding-a-device).

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

Open `http://<host>` (port 80 by default), enter your `auth.token`, and you'll see the empty dashboard.

### From source

Requires Go 1.23+ and Node 20+.

```sh
make build        # builds the frontend, then the binary into ./bin/setu
make run          # build + run with ./config.yaml
```

`make build-arm64` cross-compiles a static `linux/arm64` binary for a MikroTik/OpenWrt/Pi.

### Hot-reload development

```sh
# terminal 1 — backend (set listen.port: 8080 in config.yaml for sudo-free dev)
go run ./cmd/setu -config config.yaml
# terminal 2 — frontend (Vite proxies /api and /ws to :8080)
cd web && npm install && npm run dev
```

> The shipped `config.yaml` defaults to port **80** (privileged). For hot-reload dev, set
> `listen.port: 8080` so the unprivileged backend matches the Vite proxy above.

> `go build ./...` works on a fresh checkout even before the frontend is built: the embed
> contains only a `.gitkeep`, and the server serves a small built-in placeholder page until
> you run `make web`.

---

## Configuration

`config.yaml` is **data only** — device *behaviour* lives in code, never in config.

```yaml
listen:
  port: 80               # TCP port (default 80; binding to 80 needs privilege)
  interface: ""          # bind address; blank = all interfaces
  # socket: /run/setu.sock  # serve on a Unix socket instead (tunnel-only)
auth:
  token: "CHANGE_ME"     # bearer token required on /api and /ws — CHANGE THIS
poll_interval: 5s        # how often to re-read device state (Go duration string)
devices: []              # empty for now; see "Adding a device"
```

| Key | Meaning |
| --- | --- |
| `listen.port` | TCP port. Defaults to `80` (binding to 80 needs privilege — run as root or grant `CAP_NET_BIND_SERVICE`). |
| `listen.interface` | Bind address (a network interface's IP, e.g. `192.168.1.10`). **Blank = all interfaces.** Use `127.0.0.1` for loopback only. |
| `listen.socket` | Optional Unix-domain socket path (e.g. `/run/setu.sock`) for tunnel-only, zero-open-port access. When set, it overrides `interface`/`port`. |
| `auth.token` | Bearer token for `/api` and `/ws`. The server refuses to start with an empty token and warns if it's still `CHANGE_ME`. |
| `poll_interval` | Duration like `5s`, `500ms`, `1m`. `0` disables polling. |
| `devices[]` | One entry per device: `id`, `brand`, `model`, `name`, `mac` (**required**, primary identity), `ip` (optional hint), `series` (optional friendly product/series name shown in the UI, e.g. `AU7700`). |

### HTTP / WebSocket API

All endpoints require `Authorization: Bearer <token>` (the WebSocket also accepts `?token=`).

| Method & path | Body | Result |
| --- | --- | --- |
| `GET /api/devices` | — | `[]DeviceView` (id, name, brand, model, `series` (optional), mac, capabilities, state) — `[]` when none |
| `POST /api/devices/{id}/command` | `{"action":"on"}` / `{"action":"off"}` | updated `DeviceView` |
| | `{"action":"set_brightness","value":70}` | (0–100) |
| | `{"action":"set_color","value":{"r":255,"g":120,"b":0}}` | |
| | `{"action":"set_color_temp","value":2700}` | white temperature (Kelvin) |
| | `{"action":"set_scene","value":11}` | preset scene id (see device `scenes`) |
| | `{"action":"set_scene_speed","value":120}` | dynamic-scene speed (10–200) |
| | `{"action":"volume_up"}` / `{"action":"volume_down"}` / `{"action":"mute"}` | relative volume / mute toggle |
| | `{"action":"set_volume","value":35}` | absolute volume (0–100) |
| | `{"action":"key","value":"KEY_HOME"}` | named remote key (tap) |
| | `{"action":"key_down","value":"KEY_RIGHT"}` / `{"action":"key_up","value":"KEY_RIGHT"}` | press-and-hold a key (the device auto-releases a hold the client never ends) |
| | `{"action":"send_text","value":"breaking bad"}` | type into the device's focused text field |
| `GET /ws` | — | WebSocket; pushes `{type,device_id,state}` (`snapshot` on connect, then `state_changed`) |

The command body is **uniform and device-agnostic**. The API checks capability support with
type assertions and returns `400` if a device doesn't support an action (e.g. brightness on a
plain switch), `404` for an unknown device, `502` for a device/IO failure. Capabilities reported
today: `switch`, `brightness`, `color`, `color_temp`, `scene`, `volume`, `key`, `key_hold`,
`app`, `text`. A device that has `scene` also lists its presets in the `scenes` field of
`GET /api/devices`.

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

The `listen` block has three fields:

- **TCP (default)** — `port` (default `80`) on `interface`. **Blank `interface` = all
  interfaces;** set it to one address (e.g. `127.0.0.1` for loopback) to **bind to a trusted
  interface**, and secure it with a VPN (WireGuard / Tailscale) or a firewall. **Never expose
  Setu raw to the internet.** Binding to port 80 needs privilege (run as root or grant
  `CAP_NET_BIND_SERVICE`).
- **Unix socket** — set `socket: /run/setu.sock` for zero open ports; reach it over an SSH
  tunnel (`ssh -L 8080:/run/setu.sock user@router`). Laptop-friendly; phones need a tunnel app.
  When set, it overrides `interface`/`port`.

Graceful shutdown is handled on `SIGINT`/`SIGTERM`.

### Reaching Setu by name (e.g. `http://setu.lan`)

Setu's server answers on **any hostname** that resolves to its IP — no Setu config is
needed for this; it's purely **DNS**. With port 80 as the default, `http://setu.lan` (no
`:port`) just works once the name resolves. `.lan` is your router's local domain, so set it up
there:

- **Router DNS (recommended).** On most home routers / OpenWrt (dnsmasq), set the Setu host's
  hostname to `setu` — it's then auto-served as `setu.lan`. Or add a static record, e.g.
  dnsmasq `address=/setu.lan/192.168.0.50`. RouterOS: a static DNS entry.
- **Per-client (no router access).** Add `192.168.0.50  setu.lan` to each device's hosts file
  (`/etc/hosts`, or `C:\Windows\System32\drivers\etc\hosts`).

> mDNS/Bonjour can give a zero-config name too, but only under **`.local`** (`setu.local`), not
> `.lan`, and would add a dependency — so for `.lan`, router DNS is the lightweight path.
> Note: `http://setu.lan` is still plain HTTP (not a secure context), so PWA install stays
> blocked — see below.

### PWA & the secure-context requirement

Service workers and "Add to Home Screen" (install, fullscreen, offline app shell) only work in
a **secure context** — HTTPS **or** `localhost`. Plain `http://<lan-ip>` loads fine but
the browser blocks PWA features. No proxy is needed — Go does TLS natively
(`ListenAndServeTLS`). Easiest options:

- **`localhost`** via an SSH tunnel — counts as secure, nothing else needed.
- **Tailscale** — gives automatic HTTPS on your `*.ts.net` name.
- **Self-signed cert** — trust it once on each device.

(Phase 1 serves plain HTTP; the listener is the place to add TLS when you want installable PWA.)

---

## Supported devices

| Brand · model (`brand`/`model`) | Capabilities | Transport |
| --- | --- | --- |
| Philips WiZ — `WiZ`/`color_bulb` | switch, brightness, color, color_temp, scene | UDP :38899 (local, no cloud) |
| Samsung Tizen TV — `Samsung`/`tizen` | switch (power), volume (absolute + mute), key, key_hold, app, text | REST :8001 + WebSocket/TLS :8002 + UPnP :9197 + Wake-on-LAN |

### Philips WiZ (`WiZ`/`color_bulb`)

- Pure local control over UDP — no cloud, login, or key. On/off, brightness (10–100; the WiZ
  hardware floor is 10%, so lower values clamp), RGB color, **white temperature** (2200–6500 K),
  and the **32 predefined scenes** (color / white-temp / scene are exclusive modes on the bulb).
- IP resolution chain: ARP table → **WiZ UDP broadcast discovery** (matches the bulb by MAC) →
  the `ip` hint. Discovery means a DHCP IP change is handled automatically — this is the
  per-brand discovery the `Resolver` seam anticipates (`internal/devices/wiz/discovery.go`).
- Tunable-white-only WiZ bulbs ignore RGB; add a `tunable_white` model (switch + brightness +
  color temperature) the same way if you have one.

### Samsung Tizen TV (`Samsung`/`tizen`)

- **Power on** = Wake-on-LAN (sprayed at each interface's directed broadcast + the limited
  broadcast, ports 9 & 7). ✅ Verified to wake a UA50AU7700KLXL from off. ⚠️ WoL over Wi-Fi can
  still fail if the TV's network-standby ("Power On with Mobile") is off — that's a Samsung/Wi-Fi
  limit, not Setu. **Power off**, volume, and navigation keys (over the WebSocket) work when the
  TV is on.
- **Volume & mute are real state:** the slider sets an absolute level over UPnP
  (RenderingControl) and Setu reads volume + mute back on every poll, so changes made with the
  physical remote show up in the UI within a tick.
- **Press-and-hold** on every remote button (`key_down`/`key_up`): a hold the client never ends
  is auto-released by a watchdog — a stuck key would otherwise freeze the TV's remote channel.
- **Text input:** type into whatever field is focused on the TV; the card mirrors the TV-side
  field live (focused or not, current contents) from the TV's IME events.
- **First-use pairing:** the first power-off/key/volume command makes the TV show an **Allow**
  prompt — accept it once. Setu captures the returned token and caches it. Set the TV's *General →
  External Device Manager → Device Connection Manager → Access Notification* to "First Time Only".
- **Token cache:** `$SETU_STATE_DIR/setu-samsung-<id>.token` (defaults to the OS temp dir). Point
  `SETU_STATE_DIR` at a persistent path so the token survives reboots.
- **Same L2 segment required:** Samsung blocks the remote WebSocket across subnets/VLANs — keep
  Setu and the TV on the same segment.
- The TV serves its WebSocket/HTTPS with a self-signed cert, which Setu trusts (a known LAN device
  resolved from its MAC). Remote keys are validated against `KEY_[A-Z0-9_]+`; `KEY_FACTORY`
  (service menu) is refused.

## Adding a device

This is the core next step, and the whole architecture is built around making it small and
local. Two real packages show the pattern applied to hardware: `internal/devices/wiz` (a compact
UDP device) and `internal/devices/samsung` (REST + WebSocket + Wake-on-LAN, and how new
capabilities like `volume`/`key` light up matching UI controls). Each device lives in its own package, organised **by brand → model**. Use
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
       brand: WiZ   # case-insensitive (wiz / WiZ both work)
       model: color_bulb
       name: "Living Room"
       mac: "a8:bb:50:11:22:33"   # primary identity
       ip: 192.168.1.50           # optional hint
   ```

The frontend needs **no changes** — `DeviceCard` renders the right controls from the device's
reported `capabilities`.

### Not yet (by design)

- No automation/rules engine — only the event-bus seam it will subscribe to.
- No HomeKit — but the front-end-protocol seam (the `api` package over the manager/bus) keeps it
  addable later without touching device code.
- Setu treats REST reachability as a TV's power proxy — so a TV in network-standby may read
  as "on". (Volume/mute *are* read back for real, over UPnP.)

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

## Documentation

Beyond this file, docs are kept **point-to-point** for humans and AI assistants:

- **Native device protocols:** [`docs/devices/wiz.md`](docs/devices/wiz.md),
  [`docs/devices/samsung.md`](docs/devices/samsung.md) — how to call each device on the wire.
- **Per-module context:** every package has its own `README.md`
  (`internal/*/README.md`, `cmd/setu/`, `web/`) — purpose, key types, flow, gotchas, how to extend.
- **Index:** [`docs/README.md`](docs/README.md).

## Why these dependencies?

Kept deliberately minimal (standard library does the rest):

- **`github.com/coder/websocket`** — small, context-aware, zero-dependency WebSocket library.
- **`gopkg.in/yaml.v3`** — human-friendly config with comments and `5s`-style durations.
- **Frontend:** Svelte 5 (tiny runtime → small JS heap, important on mobile), Vite, Tailwind.
  No heavy UI kit. The production bundle is ~24 KB gzipped.

## License

GPL-3.0 — see [LICENSE](LICENSE).
