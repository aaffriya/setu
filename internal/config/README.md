# `internal/config`

Runtime settings, persisted device specs, and the brand/model factory.

## Environment

| Variable | Default | Meaning |
| --- | --- | --- |
| `SETU_TOKEN` | `CHANGE_ME` | API/WebSocket bearer token |
| `SETU_INTERFACE` | all interfaces | TCP bind address |
| `SETU_PORT` | `80`, or `443` with TLS | TCP port |
| `SETU_SOCKET` | unset | Unix socket, overriding TCP |
| `SETU_TLS_CERT`, `SETU_TLS_KEY` | unset | native HTTPS PEM pair |
| `SETU_POLL_INTERVAL` | `45s` | active cadence |
| `SETU_STATE_DIR` | OS temp | read by store and Samsung token code |

Bad ports, durations, or incomplete TLS pairs fail startup.

## Types and rules

- `Config` and `Load`: environment → defaults → validation.
- `DeviceSpec`: `id`, `brand`, `model`, optional `series`, `name`, and `mac`.
- `Factory`: case-insensitive `(brand, model)` registration and construction.
- `Deps`: injected resolver and event bus.

`DeviceSpec.Validate` is the gate for scan, manual add, and restore. IDs are
lowercase `a-z0-9_-`, 1–32 characters, and cannot start with punctuation.
Names are capped at 48 characters, series at 32, and MACs are canonicalized.

The factory never imports device packages. `cmd/setu` owns registration so
dependency direction remains device package → config, not the reverse.
