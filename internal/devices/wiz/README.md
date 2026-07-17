# wiz — Philips WiZ bulbs

`import "setu/internal/devices/wiz"` · local UDP control, port 38899, no cloud.

## Protocol
- Full native reference: **`docs/devices/wiz.md`**.

## Files
- `wiz.go` — `base` (UDP `getPilot`/`setPilot`, resolve chain, state) +
  `ColorBulb` and `TunableWhiteBulb` models.
- `discovery.go` — `Discoverer` implements `resolver.Resolver` via UDP **broadcast** (matches the bulb by MAC).
- `scenes.go` — the 32 named WiZ scenes, exposed via `Scenes()`.

## Capabilities → protocol
- `switch` → `setPilot {state}`
- `brightness` → `setPilot {dimming}` (clamped to ≥10, the WiZ floor)
- `color` → `setPilot {r,g,b}`
- `color_temp` → `setPilot {temp}` (Kelvin, clamped to the model range:
  2200–6500 for `color_bulb`, 2700–6500 for `tunable_white`)
- `scene` → `setPilot {sceneId}` (ids 1–32; `Scenes()` lists names)
- scene speed → `setPilot {speed}` (10–200, `color_bulb` dynamic scenes;
  `tunable_white` exposes only static scenes and no-ops speed)
- `Poll` → `getPilot` (maps present fields into `device.State`)

`color_bulb` exposes all five capabilities. `tunable_white` exposes switch,
brightness, color temperature, and its supported white scenes (ids 9–16); it
does not implement `ColorControl`, so the UI does not render an RGB picker.

## Modes and readback
- Color, direct white temperature, and scene commands select mutually exclusive
  modes. A white scene can still report both its `sceneId` and underlying `temp`;
  `sceneId` is authoritative for saving/replaying the selected preset.

## Resolution
- cached IP → ARP → **WiZ broadcast discovery** → `ip` hint. Any UDP failure invalidates the cache → next call re-resolves.

## Status
- **Verified live** (read + write), 2026-06-03 — confirmed a color bulb (accepts RGB).
- **Verified live** (read + write), 2026-07-17 — confirmed an
  `ESP25_SHTW_01` tunable-white bulb: RGB and colour scene 1 are ignored; white
  scenes 9–16 and the reported 2700–6500 K range work.
