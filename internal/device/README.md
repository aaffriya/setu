# device — capabilities & value types

`import "setu/internal/device"` · the vocabulary every device speaks. Leaf
package: it imports nothing from Setu; everything else points here.

## Purpose
- The `Device` contract + the small **capability interfaces** a device opts into.
- Value types that cross the API and event bus: `State`, `Color`.

## Key types
- `Device` — identity/metadata + `State()` snapshot (must not do I/O).
- `Switchable` — `On()` / `Off()`.
- `Dimmable` — `SetBrightness(0–100)`.
- `ColorControl` — `SetColor(Color)`.
- `ColorTempControl` — `SetColorTemp(kelvin)` + the hardware's min/max Kelvin range.
- `SceneControl` — `Scenes() []Scene` + `SetScene(id)` + `SetSceneSpeed(speed)` (named presets; dynamic-scene speed).
- `SpeedControl` — `SetSpeed(step)` + the hardware's min/max step (discrete steps, e.g. a fan's 1–6).
- `SleepMode` — `SetSleep(on)` (a sleep/night mode independent of power).
- `LightSwitch` — `SetLight(on)` (a secondary light that only switches, e.g. a fan's lamp; a light that dims uses `Dimmable`).
- `TimerControl` — `SetTimer(hours)` + `TimerOptions()` (auto-off timer; the driver owns any translation to a vendor index).
- `Volume` — `VolumeUp` / `VolumeDown` / `ToggleMute` (TVs; `State.Muted` reflects real mute where readable).
- `VolumeSetter` — `SetVolume(0–100)` (absolute level; TVs over UPnP).
- `KeyControl` — `SendKey("KEY_…")` (remote keys, tap).
- `KeyHold` — `PressKey` / `ReleaseKey` (hold a key down; **implementations must guarantee the release** — watchdog, supersede — a stuck press can freeze the device).
- `TextInput` — `SendText(text)` (type into the device's focused field; `State.TextActive/TextValue` mirror it).
- `AppControl` — `Apps() []App` + `LaunchApp(id)` (launch named apps, e.g. a TV's streaming apps).
- `Pollable` — `Poll()` re-reads hardware (internal; used by the poller, **not** a UI capability). A driver may wrap `ErrPollNoResponse` when its fallback state remains meaningful without a live reply.
- `State{Online,On,Brightness,Color,ColorTemp,Scene,SceneSpeed,Speed,Sleep,Light,TimerHours,TimerElapsedMins,Volume,Muted,TextActive,TextValue}`, `Color{R,G,B}`, `Scene{ID,Name,Dynamic}` (Dynamic = speed-adjustable), `App{ID,Name}`, capability constants `Cap*`.
  **`State` must stay comparable** — the manager detects change with `!=`, so every field is a scalar.

## Design rule
- One interface per concern. A device implements `Device` + **only** the capabilities its hardware has.
- The API discovers support via type assertions (`dev.(Dimmable)`), so new capabilities never touch devices that lack them.

## Extend (new capability)
1. Add an interface here + a `Cap…` constant (and any `State` field it needs — keep it scalar).
2. Add a case to the command switch in [`internal/control`](../control/README.md) (`control.go`).
3. If it exposes a range or list (like `ColorTempRange`/`Scenes`), surface it in `manager.metaView`.
4. Implement it in the device(s) that support it.
5. The UI is **not** free for a new capability — see "Adding a device" in the root `README.md` for
   the full list of web-side touch points, including the `DeviceCard` group a capability must join
   to be reachable at all.
