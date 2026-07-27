# `internal/devices/wol`

Generic `wol/device` for a PC, NAS, router, or other Wake-on-LAN target.

It implements only `device.WakeOnLAN`:

- no IP resolver;
- no polling or readable power state;
- no event publisher;
- one `wake` action using the shared `internal/wol` sender.

`State.Online` is always true so the fire-and-forget Wake button remains
available. Add the target manually from **Settings → Devices** using its MAC.
The target must have Wake-on-LAN/network standby enabled and share the broadcast
domain.
