# `internal/devices`

One package per brand; one concrete type per supported model.

| Package | Models | Protocol |
| --- | --- | --- |
| `example` | `bulb` | compiling template/stub |
| `wiz` | `color_bulb`, `tunable_white` | UDP |
| `samsung` | `tizen` | REST, WSS, UPnP, WoL |
| `atomberg` | `fan`, `fan_light` | UDP beacon/state |
| `wol` | `device` | Wake-on-LAN |

Brand packages usually contain an embedded `base` for identity, transport,
address caching, and state; models implement only their real capabilities.
Each package exports constructors and `Register(*config.Factory)`.

Addressable drivers cache the resolved IP and invalidate it after transport
failure. Scan-capable brands also implement `resolver.Scanner`. Unknown scan
models stay empty rather than being guessed.

Use [`example`](example/README.md) and the root
[`Adding a driver`](../../README.md#adding-a-driver) checklist.
