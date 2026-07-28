# `internal/config`

Runtime settings, persisted device specs, and the brand/driver factory.

## Environment

| Variable | Default | Meaning |
| --- | --- | --- |
| `SETU_TOKEN` | `CHANGE_ME` | API/WebSocket bearer token |
| `SETU_INTERFACE` | all interfaces | TCP bind address |
| `SETU_PORT` | `80`, or `443` with TLS | TCP port |
| `SETU_TLS_CERT`, `SETU_TLS_KEY` | unset | native HTTPS PEM pair |
| `SETU_POLL_INTERVAL` | `45s` | active cadence |
| `SETU_STATE_DIR` | OS temp | read by store and Samsung token code |

Bad ports, durations, or incomplete TLS pairs fail startup.

## Startup self-test

`Preflight` checks the environment this configuration is about to run in and
returns every finding at once, fatal ones first, so a refused start says all its
reasons rather than one per restart. It touches nothing: the state directory is
probed with a temporary file that is removed again, and the certificate is
parsed but never served.

Fatal: a state directory this process cannot write, a bind address no interface
on this host has, an unusable TLS pair, a negative poll interval. Warnings: the
default or a short token, a privileged port without the privilege, a poll
interval under a second.

Each check exists because its failure otherwise surfaces later and somewhere
else — an unwritable directory looks like devices that will not save, a stale
bind address like a server that started and cannot be reached.

## Types and rules

- `Config` and `Load`: environment → defaults → validation.
- `DeviceSpec`: `id`, `brand`, `driver`, optional `model`, `name`, and `mac`.
- `Factory`: case-insensitive `(brand, driver)` registration and construction,
  each pair carrying the human `category` and `label` the UI shows for it.
- `Deps`: injected resolver and event bus.
- `LegacyDeviceSpec.Upgrade`: version-1 data, where `model` meant the driver key
  and `series` meant the hardware.

Three words, one job each: `brand` is the vendor, `driver` is which code runs
the device (identity, never shown), `model` is the hardware itself (a label,
nothing branches on it).

`DeviceSpec.Validate` is the gate for scan, manual add, and restore. IDs are
lowercase `a-z0-9_-`, 1–32 characters, and cannot start with punctuation.
Names are capped at 48 characters, models at 32, and MACs are canonicalized.

The factory never imports device packages. `cmd/setu` owns registration so
dependency direction remains device package → config, not the reverse.
