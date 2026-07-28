# `cmd/setu`

The only executable and composition root.

Startup order:

1. Load optional environment settings.
2. Run `config.Preflight` and log every finding; stop on any fatal one, before
   anything has been opened or started.
3. Create the event bus, ARP resolver, device factory, and brand scanners.
4. Open `$SETU_STATE_DIR/setu.json`.
5. Build stored devices through `inventory` and register them with `manager`;
   load the user accounts.
6. Start the adaptive poller and automation engine.
7. Serve the embedded UI, API, and WebSocket until `SIGINT`/`SIGTERM`.

Optional layers must not take the bridge down with them: a users or automation
section that will not load is logged and left out, because the administrator
still has to be able to sign in and repair it. LAN presence is resolved here
too — `lanPresence` returns nil on a host with no neighbour table, which is how
presence rules come to be refused there rather than stored and never fired.

Add a brand by registering its constructors and, when supported, its scanner in
`main.go`. Keep protocol and business behavior out of this package.

Setu accepts no flags and reads no config file. See the root
[`README.md`](../../README.md) for runtime settings and deployment.
