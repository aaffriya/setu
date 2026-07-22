# wol — Wake-on-LAN magic packet

`import "setu/internal/wol"` · broadcast a magic packet to wake a host by MAC.

## Purpose
- One home for the Wake-on-LAN wire format, shared by the `wol` brand
  (`internal/devices/wol`) and any brand whose devices also support WoL
  (e.g. `internal/devices/samsung`).

## Key function
- `Send(mac string) error` — builds the magic packet (6×`0xff` + MAC×16) and
  sends three short rounds to the limited broadcast plus every
  interface's directed broadcast, on the two common WoL ports (9 and 7).
  Reuses `resolver.NormalizeMAC` for the MAC. Returns an error if the MAC is
  malformed or no broadcast target was reachable; callers add their own
  identity with `%w`.

## Gotchas
- WoL is fire-and-forget: a `nil` return means the packet was *sent*, not that
  the host woke. The target needs WoL enabled (BIOS/OS); a Wi-Fi host in standby
  may not honour it.
- Directed broadcast needs the `SO_BROADCAST` socket option (set internally).
- A container must have a veth bridged onto the target LAN. Routers do not
  forward a broadcast out of an isolated/NAT-only container network.
