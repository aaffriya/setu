# wol — Wake-on-LAN

`import "setu/internal/devices/wol"` · wake a machine (PC, NAS, router) by MAC.

## Purpose
- The simplest possible device: a MAC address with one action, **Wake**, which
  broadcasts a Wake-on-LAN magic packet to power the machine on.

## What it is
- One model `Device` implementing only `device.WakeOnLAN` (`Wake() error`).
- No `base`, no transport, no resolver/bus: WoL is a layer-2 broadcast, so there
  is no IP to resolve, no state to poll, and no events (waking is fire-and-forget).
- `State` reports `Online: true` always so the UI keeps the Wake button enabled.

## Config
```yaml
- id: desktop_pc
  brand: wol
  model: device
  name: "Desktop PC"
  mac: "aa:bb:cc:dd:ee:ff"   # no ip needed — WoL is broadcast
```

The frontend renders a card with the name, the MAC, and a single **Wake** button
(capability `wol`). Note: the target must have Wake-on-LAN enabled in its BIOS/OS,
and a Wi-Fi machine in standby may not honour it — same caveat as `../samsung/`.
