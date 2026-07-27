# `internal/wol`

Shared Wake-on-LAN packet sender used by Samsung and the generic WoL device.

`Send(mac)` builds `FF` × 6 plus MAC × 16 and sends three short rounds to:

- the limited IPv4 broadcast;
- each interface-directed broadcast;
- ports 9 and 7.

The function validates MAC syntax and reports when no target could be written.
A nil error confirms only that packets were sent, not that the host woke.
Targets need firmware/OS network standby, and the Setu container must share the
target LAN broadcast domain.
