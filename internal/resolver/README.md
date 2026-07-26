# resolver — MAC → IP

`import "setu/internal/resolver"` · turns a stable MAC into a current IP.

## Purpose
- IoT IPs change (DHCP); the MAC is the identity. This is the resolution seam.

## Key types
- `Resolver` interface — `Lookup(mac) (net.IP, error)`.
- `Scanner` interface — `Scan(ctx) ([]Candidate, error)`: the same seam the other
  way round. `Lookup` = "where is this configured MAC now?", `Scan` = "what is on
  the network that has **not** been added yet?" (see `POST /api/discovery/scan`).
- `Candidate` — one scan result: brand, model (empty = no driver for it), the
  device-reported series/name, MAC, and the IP it answered from.
- `ARPResolver` — default impl; reads `/proc/net/arp` (Linux; needs host networking). `NewARPResolver()`.
- `NormalizeMAC(s)` — canonical separator-free hex; accepts `:` / `-` / `.`-separated **or** bare hex (e.g. WiZ reports `d8a011ff5ef0`).
- `FormatMAC(s)` — the colon form, for config files and the UI. Comparison always goes through `NormalizeMAC`.

## Strategies (all behind `Resolver`)
- **ARP** — now (default).
- **Per-brand discovery** — WiZ UDP broadcast and Samsung SSDP + REST `wifiMac`
  verification, both implementing the same `Resolver` seam (and both also `Scanner`).
- **DHCP leases** — future (OpenWrt `/tmp/dhcp.leases`, RouterOS API).

## Gotchas
- On non-Linux dev (macOS) ARP returns an error → WiZ and Samsung fall back to their
  cross-platform brand discovery.
- Reading `/proc/net/arp` only sees devices the host has talked to recently.
- A `Scanner` must never invent a device it did not hear from, and never guess a
  model: an unrecognised reply is a `Candidate` with an empty `Model`. Broadcast
  probes are repeated inside the reply window — one datagram is not enough on real
  Wi-Fi, and a missed reply is indistinguishable from an absent device.
