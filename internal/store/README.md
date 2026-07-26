# store — the state file

`import "setu/internal/store"` · one small JSON document, written atomically.

## Purpose
- Hold everything Setu persists: the **devices** the user added and the **automation** rules.
- Server settings come from the environment, so this file plus a few variables fully describe an installation. That is what makes backup a copy of two things, not a filesystem tour.

## Key types
- `State{Version, Devices []config.DeviceSpec, Automations json.RawMessage}`.
- `New(path)`, `Load()`, `Update(func(*State) error)` — read-modify-write of the whole document under one lock.
- `DefaultPath()` → `$SETU_STATE_DIR/setu.json`; the bool reports the OS-temp fallback so the composition root can warn that state may not survive a reboot.

## Design rule
- The store knows the file's **shape**, not the **meaning** of its sections. Devices are `config.DeviceSpec` (validated by `config`); the automation section stays raw JSON owned by `internal/automation`. Both go through `Update`, so writing one section can never drop the other.

## Gotchas
- Writes go to a temp file + `rename`, mode `0600` — a crash or full disk leaves the previous file, never a half-written one.
- A failing `mutate` leaves the file untouched.
- `MaxBytes` (384 KB) bounds the whole file; the automation section is separately capped at 256 KB where it enters the API.
- Decoding is strict (`DisallowUnknownFields`): a hand-edited typo fails loudly at startup instead of silently starting with no devices.
- One-time upgrade path: when the file is missing, a legacy `setu-automations.json` beside it is adopted as the automation section. Removable once no installation predates the state file.
