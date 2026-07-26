# cmd/setu — the binary / composition root

`go build ./cmd/setu` · the only `main`; wires everything together.

## What it does (in order)
1. `config.Load()` — settings from the environment (all optional; see `internal/config`).
2. Build the event `Bus`, the `ARPResolver`, and a device `Factory`.
3. **Register brands** — one `<brand>.Register(factory)` line each, plus the brand
   discoverers that can scan, in the `scanners` slice.
4. Open the state file (`internal/store`) → `Manager` → `inventory.New(...)`, which
   builds and registers the devices the user added.
5. Start the adaptive `Poller` (immediate poll, active `SETU_POLL_INTERVAL`, then idle backoff).
6. Load automations from the same state file (optional layer; a load failure is logged, not fatal).
7. Start the HTTP server; serve until `SIGINT`/`SIGTERM`, then graceful shutdown.

## Where to edit
- **Add a brand:** add `wiz.Register(factory)` / `samsung.Register(factory)` next to the others,
  and its discoverer to `scanners` if it implements `resolver.Scanner`.
- Listener selection (`:8080` TCP vs `unix:/run/setu.sock`), graceful shutdown, and the slog logger live in `main.go`.

## Run
- `./setu` — no flags, no config file. `SETU_TOKEN=… SETU_PORT=8080 ./setu` to override
  defaults; devices are added from the UI. Deployment: root README.
