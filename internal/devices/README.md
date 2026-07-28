# `internal/devices`

One package per brand; one concrete type per driver.

| Package | Models | Protocol |
| --- | --- | --- |
| `example` | `bulb` | compiling template/stub |
| `wiz` | `color_bulb`, `tunable_white` | UDP |
| `samsung` | `tizen` | REST, WSS, UPnP, WoL |
| `atomberg` | `fan`, `fan_light` | UDP beacon/state |
| `wol` | `device` | Wake-on-LAN |

Brand packages usually contain an embedded `base` for identity, transport,
address caching, and state; driver types implement only their real
capabilities. `base` carries the reported `model`; the driver type carries
`Driver()`.
Each package exports constructors and `Register(*config.Factory)`.

Addressable drivers cache the resolved IP and invalidate it after transport
failure. Scan-capable brands also implement `resolver.Scanner`, registering a
human label per driver. Hardware with no driver here is reported with an empty
driver rather than being guessed at.

Use [`example`](example/README.md) and the root
[`Adding a driver`](../../README.md#adding-a-driver) checklist.
