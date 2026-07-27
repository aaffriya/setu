# `internal/control`

One stateless command vocabulary shared by the API, manual scenes, and
automations.

- `Validate(device, request)` checks capability support and values without I/O.
- `Execute(device, request)` performs the same validated operation.
- `InputError` is safe caller input; other errors are transport/device failures.

Actions:

```text
on off
set_brightness set_color set_color_temp set_scene set_scene_speed
set_speed set_sleep set_timer set_light
volume_up volume_down mute set_volume
key key_down key_up send_text launch_app wake
```

Each action maps to exactly one capability interface in `internal/device`.
Brand-specific rules do not belong here. A new capability requires a focused
case here after its interface exists.
