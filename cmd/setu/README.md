# `cmd/setu`

The only executable and composition root.

Startup order:

1. Load optional environment settings.
2. Create the event bus, ARP resolver, device factory, and brand scanners.
3. Open `$SETU_STATE_DIR/setu.json`.
4. Build stored devices through `inventory` and register them with `manager`.
5. Start the adaptive poller and automation engine.
6. Serve the embedded UI, API, and WebSocket until `SIGINT`/`SIGTERM`.

Add a brand by registering its constructors and, when supported, its scanner in
`main.go`. Keep protocol and business behavior out of this package.

Setu accepts no flags and reads no config file. See the root
[`README.md`](../../README.md) for runtime settings and deployment.
