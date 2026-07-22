# automation — bounded local rules

`import "setu/internal/automation"` · persistent schedules, device power
relations, and authenticated incoming webhook triggers.

## Limits

- one trigger per rule; up to 4 simple AND conditions and 16 ordered actions
- 64 rules, two fixed workers, a 32-entry queue, and 20 RAM-only run results
- an action may run another enabled automation inline; call graphs must be
  acyclic, are capped at 8 rules deep, and share 128-action / 960-second delay
  budgets per run
- no scripts, nested expression tree, per-rule goroutine, database, retries, or
  outbound HTTP

Rules persist atomically at `$SETU_STATE_DIR/setu-automations.json` (OS temp
fallback). Webhook plaintext secrets are returned once; only SHA-256 hashes are
stored. The engine subscribes through the event bus's recoverable subscription
and atomically replaces a stale event buffer with a fresh device snapshot after
overflow. Rules whose configured devices/capabilities changed are disabled at
startup instead of preventing the bridge from serving its normal controls;
callers of those newly disabled nested rules are disabled in the same pass.
Device commands and polls are serialized per device by the manager, so a stale
poll response cannot reverse a successful automation action.
