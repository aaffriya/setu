# config — schema, loader, factory

`import "setu/internal/config"` · config is **data**; the factory turns it into devices.

## Purpose
- Parse `config.yaml`, validate, and map `(brand, model)` → a device constructor.

## Key types
- `Config{Listen, Auth, PollInterval, Devices}`, `DeviceSpec`, `Duration` (parses `"5s"`).
- `ListenConfig{Interface, Port, Socket}` — TCP on `Interface:Port` (blank interface = all interfaces, port defaults to **80**), or a Unix socket when `Socket` is set. `Network()` returns the `net.Listen` args; `String()` renders it for logs.
- `Load(path)` — apply defaults → unmarshal → `validate()`.
- `Factory` — `Register(brand, model, Constructor)`, `Build`, `BuildAll`.
- `Constructor func(DeviceSpec, Deps) (device.Device, error)`; `Deps{Resolver, Bus}`.

## Design rule
- The factory imports **no device packages** — device packages depend on `config`, never the reverse. The composition root (`cmd/setu`) registers each constructor.

## Gotchas
- `validate()` rejects an empty token, duplicate ids, and a device missing brand/model/mac.
- `Duration` exists because YAML can't decode `"5s"` into `time.Duration` directly.
- **Brand/model matching is case-insensitive** (`key()` lowercases both), so config may write `WiZ`, `wiz`, etc. The device's *display* brand is whatever it reports (`Device.Brand`), e.g. `WiZ`.
