# cmd/setu — the binary / composition root

`go build ./cmd/setu` · the only `main`; wires everything together.

## What it does (in order)
1. Load `config.yaml`.
2. Build the event `Bus`, the `ARPResolver`, and a device `Factory`.
3. **Register brands** — one `<brand>.Register(factory)` line each.
4. `factory.BuildAll(cfg.Devices, …)` → the `Manager`.
5. Start the adaptive `Poller` (immediate poll, active `poll_interval`, then idle backoff).
6. Start the HTTP server; serve until `SIGINT`/`SIGTERM`, then graceful shutdown.

## Where to edit
- **Add a brand:** add `wiz.Register(factory)` / `samsung.Register(factory)` next to the others.
- Listener selection (`:8080` TCP vs `unix:/run/setu.sock`), graceful shutdown, and the slog logger live in `main.go`.

## Run
- `./setu -config config.yaml`. Flags & deployment: root README.
