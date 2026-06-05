# samsung — Samsung Tizen TVs

`import "setu/internal/devices/samsung"` · REST + WebSocket/TLS + Wake-on-LAN.

## Protocol
- Full native reference: **`docs/devices/samsung.md`**.

## Files
- `samsung.go` — `base` (REST reachability, WS `sendKey` + token persistence, Wake-on-LAN) + `TV` model.

## Capabilities → transport
- `switch` on → **Wake-on-LAN** (UDP 9); off → WS `KEY_POWER`.
- `volume` → WS `KEY_VOLUP` / `KEY_VOLDOWN` / `KEY_MUTE`. No absolute level on the wire, so `SetVolume(pct)` **tracks** the level and steps to the target with presses **paced ~120 ms** (the TV debounces faster bursts). Ramps in the background; a newer `set_volume` supersedes; sliding fully to 0/100 re-calibrates. `State.Volume` is the tracked estimate.
- `key` → WS arbitrary key, validated `^KEY_[A-Z0-9_]+$` (`KEY_FACTORY` refused). Source/input via `KEY_SOURCE` / `KEY_HDMI` / `KEY_TV`; media, channel, nav keys too.
- `app` → REST `POST /api/v2/applications/<id>` for a fixed catalog (YouTube / Netflix / Prime Video / Skypro / Apple Music). Each app tries its known ids in order; if all 404, it **self-heals** via `ed.installedApp.get` (matches by name, launches the real id, caches it). `LaunchApp` only accepts a catalog id.
- `Poll` → REST `/api/v2/` reachability as the **live power signal** (reachable ⇒ on), like the bulb's `getPilot` poll, so out-of-band on/off (physical remote) shows up next tick. Online whenever the address resolves (off ≠ offline, so the power toggle stays usable to wake it). A short grace window after an explicit On/Off avoids flicker while the TV powers down. Can't read volume.

## Token
- Captured from the `ms.channel.connect` event after the on-screen **Allow**.
- Cached at `$SETU_STATE_DIR/setu-samsung-<id>.token` (defaults to OS temp; set `SETU_STATE_DIR` to persist across reboots), mode `0600`.

## Status / caveats
- **Verified live (2026-06-04, UA50AU7700KLXL):** WoL woke the TV from off; token pairing + volume/mute keys work. First pairing ~8 s (waits for on-screen Allow); cached-token keys ~1 s.
- WoL sprays the magic packet at each interface's **directed broadcast** (e.g. 192.168.0.255) + the limited broadcast, ports 9 & 7 — needed to wake this unit. WoL over Wi-Fi can still fail on a TV with network-standby disabled.
- The remote-control WS is reused across keys and idle-closed (~45 s); a stale socket is redialed once on the next write. Fast for volume/D-pad bursts; reliability matches the old one-shot dial.
- The TV's 8002 cert is self-signed → `InsecureSkipVerify` (target is a MAC-resolved LAN device).
