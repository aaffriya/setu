# Setu — सेतु

Setu is a lightweight, self-hosted LAN controller for smart devices. One Go
binary serves the Svelte web app, API, WebSocket, automations, and native device
drivers.

Supported integrations:

- WiZ colour and tunable-white bulbs;
- Samsung Tizen TVs;
- Atomberg fans;
- generic Wake-on-LAN targets.

## Run

### Docker

```sh
docker build -t setu .
docker run --rm --network host \
  -e SETU_TOKEN=replace-this \
  -v setu-state:/var/lib/setu \
  setu
```

Open `http://<host>` and enter the same token.

### Source

Requires Go 1.23 and Node 22.

```sh
make build
SETU_TOKEN=dev \
SETU_INTERFACE=127.0.0.1 \
SETU_PORT=8080 \
SETU_STATE_DIR="$PWD/tmp/state" \
./bin/setu
```

Open `http://localhost:8080`.

`GET /healthz` answers without a token and discloses nothing about the
installation, for systemd, Docker, or an uptime check.

## Important

- Change `SETU_TOKEN` and keep Setu on a trusted LAN or VPN. Do not expose it
  directly to the internet.
- Set `SETU_STATE_DIR` to durable storage. Its default OS-temporary location may
  not survive a reboot.
- LAN discovery, ARP, UDP broadcasts, and Wake-on-LAN require host/L2 network
  access; Docker therefore uses `--network host`.
- Normal controls work over LAN HTTP. PWA installation and the service worker
  require HTTPS or `localhost`.

All optional runtime settings are documented in [`example.env`](example.env).

## People

`SETU_TOKEN` is the administrator: it comes from the environment, so nothing
written to disk can lock you out. Everyone else is added in the app
(Settings → People) with just a name — Setu generates their token and shows it
once.

Each person gets two things:

- **Devices** they can see and control. Nothing is shared by default, and a
  device added later has to be shared explicitly.
- **A role**: *Control* uses those devices; *Manage* also adds devices and
  writes automations — still only for the devices they were given.

A person's device list is enforced everywhere, not just in the UI: the device
list, the WebSocket, diagnostics and automations all only ever carry what they
were granted, and an automation counts as theirs only when every rule it calls
is theirs too. Removing or restoring devices takes their grants with them.

## Core rules

- MAC is device identity; IP addresses are resolved and cached at runtime.
- Device inventory, automations, and people are server state. Rooms, favourites,
  scenes, layout, and theme are browser preferences.
- Device controls come from reported capabilities, not brand-specific UI.
- Automations stay bounded for router-class hardware: fixed workers and capped
  rules, actions, delays, queue, and RAM-only history.

## Adding a driver

1. Copy `internal/devices/example` to `internal/devices/<brand>`.
2. Implement only capabilities the hardware supports; add `Poll` only when
   state can actually be read.
3. Export constructors and `Register`, then register the brand in
   `cmd/setu/main.go`. Register a `resolver.Scanner` there when discovery is
   supported.
4. Existing capabilities need no device-specific UI. A new capability must also
   be wired through control, manager metadata when needed, automation safety,
   web types/state, `DeviceCard`, backup validation, and tests.
5. Add focused protocol/driver tests and update `docs/devices/<brand>.md`.

Never guess an unsupported model from discovery data.

## Documentation

[`docs/README.md`](docs/README.md) indexes runtime, protocol, and package
references.

## License

GPL-3.0. See [`LICENSE`](LICENSE).
