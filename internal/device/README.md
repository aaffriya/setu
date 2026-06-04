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
- `ColorTempControl` — `SetColorTemp(kelvin)` (tunable white).
- `SceneControl` — `Scenes() []Scene` + `SetScene(id)` + `SetSceneSpeed(speed)` (named presets; dynamic-scene speed).
- `Volume` — `VolumeUp` / `VolumeDown` / `ToggleMute` (relative; TVs).
- `KeyControl` — `SendKey("KEY_…")` (remote keys).
- `AppControl` — `Apps() []App` + `LaunchApp(id)` (launch named apps, e.g. a TV's streaming apps).
- `Pollable` — `Poll()` re-reads hardware (internal; used by the poller, **not** a UI capability).
- `State{Online,On,Brightness,Color,ColorTemp,Scene,SceneSpeed}`, `Color{R,G,B}`, `Scene{ID,Name,Dynamic}` (Dynamic = speed-adjustable), `App{ID,Name}`, capability constants `Cap*`.

## Design rule
- One interface per concern. A device implements `Device` + **only** the capabilities its hardware has.
- The API discovers support via type assertions (`dev.(Dimmable)`), so new capabilities never touch devices that lack them.

## Extend (new capability)
1. Add an interface here + a `Cap…` constant.
2. Add a dispatch case in `internal/api` (handlers.go).
3. Implement it in the device(s) that support it; the UI renders it by capability.
