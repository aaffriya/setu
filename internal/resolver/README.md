# `internal/resolver`

The address/discovery seam.

- `Resolver.Lookup(mac)`: find the current IP for one configured MAC.
- `Scanner.Scan(ctx)`: enumerate devices heard on the LAN.
- `Candidate`: reported brand, supported model or empty, labels, MAC, and source
  IP.
- `ARPResolver`: Linux `/proc/net/arp` implementation.
- `NormalizeMAC` and `FormatMAC`: canonical comparison and display.

Brand drivers compose strategies:

- WiZ: ARP then UDP MAC discovery.
- Samsung: ARP then SSDP candidates verified by REST `wifiMac`.
- Atomberg: fresh beacon, cached IP fallback, brief beacon wait, then ARP.

ARP is unavailable on non-Linux development hosts and only knows recent
neighbors, so brand discovery must remain functional. Scanners repeat lossy
broadcast probes, stop on context cancellation, and return unknown hardware
with an empty model. Never invent a candidate or guess its driver.
