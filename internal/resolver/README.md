# resolver — MAC → IP

`import "setu/internal/resolver"` · turns a stable MAC into a current IP.

## Purpose
- IoT IPs change (DHCP); the MAC is the identity. This is the resolution seam.

## Key types
- `Resolver` interface — `Lookup(mac) (net.IP, error)`.
- `ARPResolver` — default impl; reads `/proc/net/arp` (Linux; needs host networking). `NewARPResolver()`.
- `NormalizeMAC(s)` — canonical separator-free hex; accepts `:` / `-` / `.`-separated **or** bare hex (e.g. WiZ reports `d8a011ff5ef0`).

## Strategies (all behind `Resolver`)
- **ARP** — now (default).
- **Per-brand discovery** — WiZ UDP broadcast and Samsung SSDP + REST `wifiMac`
  verification, both implementing the same `Resolver` seam.
- **DHCP leases** — future (OpenWrt `/tmp/dhcp.leases`, RouterOS API).

## Gotchas
- On non-Linux dev (macOS) ARP returns an error → WiZ and Samsung fall back to their
  cross-platform brand discovery.
- Reading `/proc/net/arp` only sees devices the host has talked to recently.
