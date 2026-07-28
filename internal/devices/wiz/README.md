# `internal/devices/wiz`

Local UDP `38899` WiZ driver. Full wire reference:
[`docs/devices/wiz.md`](../../../docs/devices/wiz.md).

- `wiz.go`: shared UDP transport/state plus `ColorBulb` and
  `TunableWhiteBulb`.
- `discovery.go`: MAC lookup with `getPilot`; scan and driver selection with
  `getSystemConfig`.
- `scenes.go`: names and dynamic flags for the built-in scene IDs.

`color_bulb` exposes power, brightness, RGB, 2200–6500 K temperature, and all
scenes. `tunable_white` exposes power, brightness, 2700–6500 K temperature, and
white scenes 9–16; it deliberately omits RGB.

Resolution is cached IP → ARP → WiZ broadcast. Commands require a valid reply;
any UDP failure clears the cached address. Brightness below the hardware's 10%
floor is clamped.

Both driver paths have physical-hardware verification.
