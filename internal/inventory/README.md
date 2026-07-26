# inventory — the devices the user added

`import "setu/internal/inventory"` · stored specs on one side, live devices on the other.

## Purpose
- The one place that turns a `config.DeviceSpec` into a live device and keeps the two in step: the state file, the factory and the manager all meet here.
- Devices are added from the UI — found by a network scan, or typed in for hardware that answers no scan (a Wake-on-LAN target) — so this replaces what a config file used to do at startup.

## Key type
- `New(file, factory, deps, mgr, log)` — load stored specs, build them, register them with the manager.
- `Specs()` (backup form) · `Add(spec)` · `Update(id, name, series)` · `Remove(id)` · `Replace(specs)` (restore) · `Configured(brand, mac)` (what the scan marks) · `Types()`.
- `IsNotFound(err)` — the API maps it to 404.

## Rules it enforces (so a bad entry is refused at the door, not at the next start)
- The spec must validate (`config.DeviceSpec.Validate`) and its `(brand, model)` must be registered.
- An empty id is filled in from the brand and the last three MAC bytes (`wiz_a234f2`), with a numeric suffix on collision.
- One **brand talking to one MAC** is one device. Deliberately not the MAC alone: a Wake-on-LAN card for a TV shares the TV's MAC on purpose.
- `MaxDevices` (64) bounds a home installation — this runs on router hardware.

## Gotchas
- A spec that fails validation or cannot be built (unknown brand after a downgrade, a hand-edited entry) is logged and skipped at startup, never fatal. The entry stays in the state file — it shows up in a backup export and can be removed with `DELETE /api/devices/{id}` — but it is not a live device, so the UI's list does not show it. Editing it through `Update` repairs it and brings it online without a restart.
- `Update` **rebuilds** the device (its name is fixed at construction) and swaps it in with `manager.Replace`, which keeps its position and cached state — a rename must not blank the card.
- `Replace` validates and builds everything before touching the manager, so a bad restore leaves the running installation exactly as it was.
- Removing a device leaves its automations alone: rules that reference it are disabled at the engine's next start (a run before that fails on the missing device), and silently rewriting a user's automations from a delete would be a surprise. A backup restore does reconcile them, because the frontend disables rules whose devices are not in the restored list.
