# config — settings, device spec, factory

`import "setu/internal/config"` · config is **data**; the factory turns it into devices.

## Purpose
- Read the server settings from the **environment**, and map `(brand, model)` → a device constructor.
- There is no configuration file. Devices are stored state, not config (`internal/store` + `internal/inventory`).

## Environment (all optional; unset = the shipped default)
| Variable | Default | Meaning |
| --- | --- | --- |
| `SETU_TOKEN` | `CHANGE_ME` | bearer token for `/api` and `/ws` (the composition root warns while it is the default) |
| `SETU_INTERFACE` | all interfaces | bind address, e.g. `192.168.1.10` |
| `SETU_PORT` | `80` | TCP port |
| `SETU_SOCKET` | — | Unix socket path; overrides interface/port |
| `SETU_TLS_CERT` / `SETU_TLS_KEY` | — | PEM pair → serves HTTPS. Both or neither |
| `SETU_POLL_INTERVAL` | `45s` | active poll cadence (idle backs off) |
| `SETU_STATE_DIR` | OS temp | where the state file lives (read by `internal/store`) |

## Key types
- `Config{Listen, Token, PollInterval}`, `Load()` — environment → defaults → `validate()`.
- `ListenConfig{Interface, Port, Socket, TLS}` — `Network()` returns the `net.Listen` args; `String()` renders it for logs.
- `DeviceSpec{ID, Brand, Model, Series, Name, MAC}` — one stored device. `Validate()` and `Normalized()` are the single gate for everything that reaches the state file: a scan result, a typed-in form, a restored backup.
- `Factory` — `Register(brand, model, Constructor)`, `Build`, `BuildAll`, `Supports`, `Types()` (the catalog the UI's manual-add form lists).
- `Constructor func(DeviceSpec, Deps) (device.Device, error)`; `Deps{Resolver, Bus}`.

## Design rule
- The factory imports **no device packages** — device packages depend on `config`, never the reverse. The composition root (`cmd/setu`) registers each constructor.

## Gotchas
- Ids double as file names (the Samsung pairing token is stored per device id), so `Validate()` restricts them to `a-z 0-9 _ -`, max 32, not starting with `_`/`-`.
- `Normalized()` rewrites the MAC to one canonical notation, so two spellings of one device cannot both be stored.
- **Brand/model matching is case-insensitive** (`key()` lowercases both). The device's *display* brand is whatever it reports (`Device.Brand`), e.g. `WiZ`.
- A bad environment value fails at startup with a message naming the variable — never a silent fallback.
