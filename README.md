# Setu — सेतु

Setu is a small, self-hosted bridge for controlling local IoT devices from a
fast web app. One static Go binary serves the embedded Svelte PWA, JSON API,
WebSocket, device drivers, and bounded automation engine.

It is designed for router-class hardware and uses no cloud service, database,
reverse proxy, or process supervisor.

## Supported devices

| Brand / model key | Capabilities | Local transport |
| --- | --- | --- |
| `WiZ/color_bulb` | power, brightness, RGB, colour temperature, scenes | UDP `38899` |
| `WiZ/tunable_white` | power, brightness, colour temperature, white scenes | UDP `38899` |
| `Samsung/tizen` | power, volume, remote keys/hold, apps, text | REST `8001`, WSS `8002`, UPnP `9197`, WoL |
| `Atomberg/fan` | power, speed, sleep, timer, light toggle | UDP `5600`/`5625` |
| `Atomberg/fan_light` | power, speed, sleep, timer, dimmable light, light modes | UDP `5600`/`5625` |
| `wol/device` | Wake-on-LAN | UDP broadcast |

WiZ and Samsung drivers have been verified with physical hardware. Atomberg is
covered end to end with a protocol-faithful simulator but still needs physical
hardware verification.

## Run

### Docker

LAN discovery, ARP, UDP broadcasts, and Wake-on-LAN need host/L2 network access.

```sh
docker build -t setu .
docker run --rm --network host \
  -e SETU_TOKEN=replace-this \
  -v setu-state:/var/lib/setu \
  setu
```

Open `http://<host>`, enter the token, then add devices from **Settings →
Devices**.

### Source

Requires Go 1.23+ and Node `20.19+` or `22.12+`.

```sh
make build
SETU_TOKEN=replace-this SETU_PORT=8080 make run
```

`make build` builds `web/dist` and embeds it into `bin/setu`.

For frontend hot reload:

```sh
# terminal 1
SETU_TOKEN=dev SETU_PORT=8080 SETU_STATE_DIR="$PWD/tmp/state" go run ./cmd/setu

# terminal 2
cd web
npm install
npm run dev
```

Vite proxies `/api` and `/ws` to `localhost:8080`.

## Configuration

Setu does not read a config file. All settings are optional environment
variables; [`example.env`](example.env) is a copyable reference.

| Variable | Default | Purpose |
| --- | --- | --- |
| `SETU_TOKEN` | `CHANGE_ME` | bearer token for `/api` and `/ws`; change it |
| `SETU_INTERFACE` | all interfaces | TCP bind address |
| `SETU_PORT` | `80`, or `443` with TLS | TCP port |
| `SETU_SOCKET` | unset | Unix socket; overrides interface and port |
| `SETU_TLS_CERT`, `SETU_TLS_KEY` | unset | PEM pair for native HTTPS; both are required |
| `SETU_POLL_INTERVAL` | `45s` | active poll cadence; `0` disables scheduled polling only |
| `SETU_STATE_DIR` | OS temp directory | location for persistent state and Samsung tokens |

Set `SETU_STATE_DIR` to durable storage. The temporary default is not guaranteed
to survive a reboot.

## State and backup

`$SETU_STATE_DIR/setu.json` is the only Setu state document. It contains:

- the device inventory;
- the automation rule set.

Writes use a mode-`0600` temporary file plus rename. Samsung pairing tokens are
separate mode-`0600` files named `setu-samsung-<id>.token` in the same directory.

The UI creates a versioned JSON backup with selected sections: devices,
favourites, rooms, manual scenes, layout/theme, and automations. A restore
replaces only sections present in the file. It never exports the Setu access
token, Samsung plaintext pairing tokens, live state, caches, or run history.

## Architecture

```text
browser/PWA
  ├─ HTTP /api ──> API ──> manager ──> control ──> device capability
  └─ WebSocket <── event bus <── commands, polls, device push, automations
                                  │
MAC ──> ARP or brand discovery ──> current IP
```

Important boundaries:

- `internal/device`: small opt-in capability interfaces.
- `internal/resolver`: MAC-to-IP resolution and discovery scanning.
- `internal/api`: one front-end protocol over the manager and event bus.
- `internal/manager`: live registry, serialized per-device operations, cached
  snapshots, diagnostics, and adaptive polling.
- `internal/automation`: server-side schedules, device-state edges, incoming
  webhooks, conditions, ordered actions, and acyclic nested rules.

See [`docs/runtime.md`](docs/runtime.md) before changing cross-package command,
polling, WebSocket, cache, or address-resolution behavior.

## Device identity and discovery

Stored device specs contain `id`, `brand`, `model`, optional `series`, `name`,
and `mac`—never an IP. Drivers cache the current address and clear it after a
transport failure.

- WiZ: ARP, then UDP discovery matched by reported MAC.
- Samsung: ARP, then SSDP candidates verified through `/api/v2/` `wifiMac`.
- Atomberg: the fan's UDP beacon, then ARP.
- WoL: no IP resolution.

**Settings → Devices → Scan network** runs registered brand scanners in
parallel. Unsupported hardware is shown with no model instead of being guessed.
Adding a device is a separate explicit action.

## Adding a driver

1. Copy `internal/devices/example` to `internal/devices/<brand>`.
2. Put shared transport, address caching, and state publishing in an embedded
   brand `base`.
3. Implement only the capability interfaces each model supports and add
   compile-time interface assertions.
4. Implement `Poll` only when state can be read.
5. Export constructors plus `Register`, then register the brand in
   `cmd/setu/main.go`.
6. If discovery is possible, implement `resolver.Scanner` and register the
   scanner there too.
7. Add protocol tests, focused driver tests, and update the protocol document.

Existing capabilities render automatically. A new capability must be wired
through `internal/device`, `internal/control`, manager metadata when applicable,
automation validation, web API types and optimistic state, a reachable
`DeviceCard` group/control, backup restore validation, and tests.

## Automation limits

Automation is intentionally bounded for router hardware:

- 64 rules, 4 AND conditions, and 16 ordered actions per rule;
- 2 workers, a 32-run queue, and 20 RAM-only results;
- schedule, power-state, or authenticated webhook triggers;
- acyclic nested rules, at most 8 levels and 128 total actions per run;
- no scripts, expression language, retries, outbound HTTP, or persistent
  history.

Webhook bodies are ignored; callers can only trigger predefined actions.
Plaintext webhook tokens are returned only when created or rotated, while the
state file stores SHA-256 hashes.

## Network and browser requirements

- Keep Setu on a trusted LAN/VPN. Do not expose it directly to the internet.
- Samsung remote control and LAN broadcasts generally require the same L2
  segment as the device.
- A Unix socket can provide tunnel-only access.
- PWA install and service workers require HTTPS or `localhost`. Plain LAN HTTP
  still works as a normal web app. Setu can serve TLS directly with the two TLS
  environment variables.
- If an old installed PWA is stuck on a broken app shell, open
  `/api/recover`; it clears only Setu's service worker/cache and preserves the
  token and UI preferences.

## Documentation

[`docs/README.md`](docs/README.md) indexes the runtime, protocol, and package
references. Package READMEs describe only local ownership and invariants; wire
details stay under `docs/devices`.

## License

GPL-3.0. See [`LICENSE`](LICENSE).
