# example — device template (blueprint)

`import "setu/internal/devices/example"` · copy this package to add a real device.

## Purpose
- A compiling, fully-commented template. **No real protocol** — every network call is a documented stub.
- Also a usable stand-in device: `discovery.go` resolves to loopback and `send` is a stub, so an `example`
  entry in a config answers commands on any machine. Handy for working on the UI/API with no hardware.

## What it shows
- A brand `base`: transport seam (`send`), MAC→IP cache + re-resolve (`resolveIP`/`invalidateIP`), state mutate + publish (`applyState`).
- Resolution order — cached → injected ARP → brand discovery (`discovery.go`), the same chain `../wiz/` and `../samsung/` use.
- Both directions of that seam: `Lookup` (where is this configured MAC?) and `Scan` (what is on
  the network that is not configured yet — the UI's device scan). The template's `Scan` finds
  nothing and documents what a real one does.
- A model `Bulb` embedding `base`; implements `Switchable`, `Dimmable`, `ColorControl`, `Pollable`.
- The `Poll` contract: wrap `device.ErrPollNoResponse` when a failed read still returns meaningful state,
  or the manager discards it and the device keeps rendering as online (see the `Poll` doc comment).
- Compile-time proof: `var _ device.X = (*Bulb)(nil)`.
- `New` (a `config.Constructor`) + `Register(*config.Factory)`.

## Use it
- Follow the 7-step CHECKLIST at the bottom of `example.go`.
- Real instances built from this pattern: `../wiz/`, `../samsung/`.
