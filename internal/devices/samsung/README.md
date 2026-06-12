# samsung — Samsung Tizen TVs

`import "setu/internal/devices/samsung"` · REST + WebSocket/TLS + UPnP SOAP + Wake-on-LAN.

## Protocol
- Full native reference: **`docs/devices/samsung.md`**.

## Files
- `samsung.go` — `base` (REST reachability, WS keys/text/events + token persistence, UPnP volume, Wake-on-LAN) + `TV` model.

## Capabilities → transport
- `switch` on → **Wake-on-LAN** (UDP 9); off → WS `KEY_POWER`.
- `volume` → **UPnP RenderingControl** (`:9197`, SOAP): `SetVolume(pct)` is one absolute call; `GetVolume`/`GetMute` are read back on every Poll, so `State.Volume`/`State.Muted` are the TV's **real** state (physical-remote changes land within a tick). `ToggleMute` = `GetMute` → `SetMute(!cur)` (deterministic — `KEY_MUTE` is a blind toggle). `VolumeUp/Down` stay remote keys (they show the TV's own OSD) with a UPnP read-back.
- `key` → WS arbitrary key (Click), validated `^KEY_[A-Z0-9_]+$` (`KEY_FACTORY` refused). Source/input via `KEY_SOURCE` / `KEY_HDMI` / `KEY_TV`; media, channel, nav keys too.
- `key_hold` → WS `Press` … `Release`. **Safety rule: a `Press` without its `Release` freezes the TV's whole remote channel**, so the release is guaranteed without trusting the client: explicit `ReleaseKey`, the next key superseding it, or a **watchdog** (`holdMax`, 1 min) — whichever first. A `Release` is sent even when nothing is tracked as held (extra releases are harmless; missed ones are not).
- `text` → WS `SendInputString` (base64) + `SendInputEnd` into the TV's focused text field. The TV's IME events (`ms.remote.imeStart/imeUpdate/imeEnd`) are mirrored into `State.TextActive`/`State.TextValue`, so the UI shows when a field is focused and what's typed (even via the physical remote). `imeEnd` isn't guaranteed (backing out emits nothing) — focus-leaving keys (`KEY_RETURN`/`KEY_HOME`/`KEY_EXIT`/`KEY_POWER`), app launches, power off, and socket loss clear it locally.
- `app` → REST `POST /api/v2/applications/<id>` for a fixed catalog (YouTube / Netflix / Prime Video / Skypro / Apple Music). Each app tries its known ids in order; if all 404, it **self-heals** via `ed.installedApp.get` (matches by name, launches the real id, caches it). `LaunchApp` only accepts a catalog id.
- `Poll` → REST `/api/v2/` **`device.PowerState`** as the live power signal (`"on"` ⇒ on; `"standby"` ⇒ off — a TV answering REST from network standby no longer reads as on; older firmware without the field falls back to reachable ⇒ on). Out-of-band on/off (physical remote) shows up next tick. Online whenever the address resolves (off ≠ offline, so the power toggle stays usable to wake it). A short grace window after an explicit On/Off avoids flicker while the TV powers down. While on it also reads volume + mute over UPnP and keeps the event socket connected.
- **`Off` is toggle-safe:** `KEY_POWER` is a toggle, so `Off` first checks `PowerState` — if the TV is already in standby/unreachable (the app's state had drifted), it only corrects the state instead of sending the key, which would have **woken** the TV.

## Token
- Captured from the `ms.channel.connect` event after the on-screen **Allow**.
- Cached at `$SETU_STATE_DIR/setu-samsung-<id>.token` (defaults to OS temp; set `SETU_STATE_DIR` to persist across reboots), mode `0600`.

## Status / caveats
- **Verified live (2026-06-04, UA50AU7700KLXL):** WoL woke the TV from off; token pairing + volume/mute keys work. First pairing ~8 s (waits for on-screen Allow); cached-token keys ~1 s. **2026-06-02:** UPnP GetMute/SetVolume and the IME event stream confirmed on the same unit.
- WoL sprays the magic packet at each interface's **directed broadcast** (e.g. 192.168.0.255) + the limited broadcast, ports 9 & 7 — needed to wake this unit. WoL over Wi-Fi can still fail on a TV with network-standby disabled.
- The remote-control WS is opened once and **kept open while the TV is on** — it doubles as the event stream (IME, token refresh), with `Poll` redialing it if it drops. A stale socket is redialed once on the next write. `Poll` never dials without a cached token (an unpaired dial would pop the on-screen Allow prompt from the background).
- **UPnP is best-effort on other units:** some Tizen firmwares disable RenderingControl or answer 500 without active media. On this unit it works while just watching TV; if it ever fails, `SetVolume`/`ToggleMute` return the error (no silent key-step fallback — mixed paths made the state lie).
- The TV's 8002 cert is self-signed → `InsecureSkipVerify` (target is a MAC-resolved LAN device).
