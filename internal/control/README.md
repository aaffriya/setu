# control — shared command execution

`import "setu/internal/control"` · one small, stateless command switch shared by
the JSON API and automation engine.

- `Validate` checks values and capability support without device I/O.
- `Execute` performs the same validated command through the device capability.
- `InputError` is safe caller input; other errors are device/transport failures.

This package adds no interface or device-specific behavior. New capabilities
still start in `internal/device`; this is only the common command vocabulary.
