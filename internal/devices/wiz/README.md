# wiz — Philips WiZ bulbs

`import "setu/internal/devices/wiz"` · local UDP control, port 38899, no cloud.

## Protocol
- Full native reference: **`docs/devices/wiz.md`**.

## Files
- `wiz.go` — `base` (UDP `getPilot`/`setPilot`, resolve chain, state) + `ColorBulb` model.
- `discovery.go` — `Discoverer` implements `resolver.Resolver` via UDP **broadcast** (matches the bulb by MAC).
- `scenes.go` — the 32 named WiZ scenes, exposed via `Scenes()`.

## Capabilities → protocol
- `switch` → `setPilot {state}`
- `brightness` → `setPilot {dimming}` (clamped to ≥10, the WiZ floor)
- `color` → `setPilot {r,g,b}`
- `color_temp` → `setPilot {temp}` (Kelvin, clamped 2200–6500)
- `scene` → `setPilot {sceneId}` (ids 1–32; `Scenes()` lists names)
- scene speed → `setPilot {speed}` (10–200, dynamic scenes; `SetSceneSpeed`)
- `Poll` → `getPilot` (maps present fields into `device.State`)

## Modes are exclusive
- color / white-temp / scene are mutually exclusive on the bulb; each setter clears the others in `device.State` (`color_temp`/`scene` = 0 when inactive) so the UI shows the live mode.

## Resolution
- cached IP → ARP → **WiZ broadcast discovery** → `ip` hint. Any UDP failure invalidates the cache → next call re-resolves.

## Status
- **Verified live** (read + write), 2026-06-03 — confirmed a color bulb (accepts RGB).
- Tunable-white variant? Add a `tunable_white` model: drop `ColorControl`, send `temp`.
