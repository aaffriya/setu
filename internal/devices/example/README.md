# example — device template (blueprint)

`import "setu/internal/devices/example"` · copy this package to add a real device.

## Purpose
- A compiling, fully-commented template. **No real protocol** — every network call is a documented stub.

## What it shows
- A brand `base`: transport seam (`send`), MAC→IP cache + re-resolve (`resolveIP`/`invalidateIP`), state mutate + publish (`applyState`).
- A model `Bulb` embedding `base`; implements `Switchable`, `Dimmable`, `ColorControl`, `Pollable`.
- Compile-time proof: `var _ device.X = (*Bulb)(nil)`.
- `New` (a `config.Constructor`) + `Register(*config.Factory)`.

## Use it
- Follow the 7-step CHECKLIST at the bottom of `example.go`.
- Real instances built from this pattern: `../wiz/`, `../samsung/`.
