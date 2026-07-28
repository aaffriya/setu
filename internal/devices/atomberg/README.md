# `internal/devices/atomberg`

Local Atomberg fan driver: commands to UDP `5600`, beacons/state on UDP `5625`.
Full wire reference:
[`docs/devices/atomberg.md`](../../../docs/devices/atomberg.md).

- `listener.go`: one process-wide beacon/state socket, recent sightings,
  `Resolver`, `Scanner`, and pushed state callbacks.
- `state.go`: beacon classification and hex state/bitfield decoding.
- `atomberg.go`: shared transport plus `Fan` and `FanLight`.

One listener is required because every fan broadcasts to the same fixed port.
It also lets physical-remote/vendor-app changes reach the UI without waiting for
the poll cadence.

`fan` exposes power, speed, sleep, timer, and a light toggle. `fan_light`
replaces the light toggle with brightness and warm/cool/daylight scenes.
Model-to-driver mapping is explicit in `driverFor`; the beacon's "series" code
("R1", "I1") is this brand's model, and an unknown one is scanned with no
driver.

Both driver paths explicitly report live reachability for offline/recovery
automations.

Resolution is fresh beacon → cached IP → cold-cache beacon wait → ARP. Commands
have no acknowledgement; `Poll` sends `speedDelta:0` and waits for a newer
broadcast. The driver is simulator-verified end to end but not yet verified on
physical hardware.
