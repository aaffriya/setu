# control — shared command execution

`import "setu/internal/control"` · one small, stateless command switch shared by
the JSON API and automation engine.

- `Validate` checks values and capability support without device I/O.
- `Execute` performs the same validated command through the device capability.
- `InputError` is safe caller input; other errors are device/transport failures.

This package adds no interface or device-specific behavior. New capabilities
still start in `internal/device`; this is only the common command vocabulary.

## Actions
`on` · `off` · `set_brightness` · `set_color` · `set_color_temp` · `set_scene` ·
`set_scene_speed` · `set_speed` · `set_sleep` · `set_timer` · `set_light` · `volume_up` ·
`volume_down` · `mute` · `set_volume` · `key` · `key_down` · `key_up` ·
`send_text` · `launch_app` · `wake`.

Each maps to exactly one capability interface, checked by type assertion, and
validates its value before anything reaches the transport. Actions usable from
an automation are additionally gated by `safeActions` in `internal/automation`.
