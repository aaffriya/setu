# `internal/device`

Leaf package defining device identity, cached state, metadata, and opt-in
capabilities.

## Contracts

- `Device`: ID, name, brand, driver, model, MAC, capability IDs, and a cheap
  cached `State()`; it must not perform I/O.
- Optional lifecycle: `Closer`, and internal `Pollable`.
- Power/light: `Switchable`, `Dimmable`, `ColorControl`,
  `ColorTempControl`, `SceneControl`, `LightSwitch`.
- Fan: `SpeedControl`, `SleepMode`, `TimerControl`.
- Media: `Volume`, `VolumeSetter`, `KeyControl`, `KeyHold`, `TextInput`,
  `AppControl`.
- Wake-only: `WakeOnLAN`.

`Pollable` is not a UI capability. A driver that returns useful fallback state
without a live response wraps `ErrPollNoResponse`.

`State` must remain comparable because manager change detection uses `!=`.
Keep its fields scalar. Ranges and lists belong on capability methods and are
projected by manager metadata.

## New capability checklist

1. Add the smallest interface, capability constant, and scalar state field if
   required.
2. Add validation/execution in [`internal/control`](../control/README.md).
3. Project ranges/lists in manager metadata when needed.
4. Implement it only on supported models.
5. Add it to automation-safe actions only if unattended execution is safe.
6. Update web API/action types, state normalization, optimistic updates,
   snapshot/backup filtering, and automation options.
7. Render it in a `DeviceCard` group; otherwise the control can be unreachable.
8. Add backend and frontend regression coverage.
