# `internal/resolver`

The address/discovery seam.

- `Resolver.Lookup(mac)`: find the current IP for one configured MAC.
- `Scanner.Scan(ctx)`: enumerate devices heard on the LAN.
- `Candidate`: brand, the driver key or empty when unsupported, the model the
  device reported, its name, MAC, and source IP.
- `ARPResolver`: Linux `/proc/net/arp` implementation.
- `ARPResolver.Neighbours`: the whole table in one read, keyed by normalized
  MAC, for a caller watching several addresses (automation presence rules).
  Incomplete entries — asked about but never answered — are left out, so a stale
  probe cannot look like a device that is present.
- `NormalizeMAC` and `FormatMAC`: canonical comparison and display.

Brand drivers compose strategies:

- WiZ: ARP then UDP MAC discovery.
- Samsung: ARP then SSDP candidates verified by REST `wifiMac`.
- Atomberg: fresh beacon, cached IP fallback, brief beacon wait, then ARP.

ARP is unavailable on non-Linux development hosts and only knows recent
neighbors, so brand discovery must remain functional. Scanners repeat lossy
broadcast probes, stop on context cancellation, and return unknown hardware
with an empty driver. Never invent a candidate or guess its driver.
