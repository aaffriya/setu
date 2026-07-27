# `internal/automation`

Bounded server-side rules that continue running when no browser is open.

## Model and limits

- Trigger: minute schedule, device power edge, or authenticated webhook.
- Up to 64 rules, 4 AND conditions, and 16 ordered actions per rule.
- Optional stable time up to 300 seconds, cooldown up to 3600 seconds, and
  per-action delay up to 60 seconds.
- Two workers, queue size 32, last 20 run results in RAM.
- Cooldown starts only after the queue accepts a run; a full queue does not
  consume it.
- A rule may call another enabled rule inline. Calls must be acyclic, at most 8
  levels, 128 actions, and 960 delay-seconds per run.
- Power relations that form a device cycle are rejected.

Only bounded, idempotent device actions are allowed automatically. Remote-key
taps/holds, relative volume, mute toggles, and text input are intentionally not
automation-safe.

## Persistence and runtime

Rules occupy the `automations` section of `$SETU_STATE_DIR/setu.json` through
`internal/store`. Queue state, rate limits, cooldown clocks, and run history are
not persisted.

Webhook plaintext is returned only on creation/rotation. The stored/exported
form contains its SHA-256 hash. Webhook payloads never select actions.

The engine waits for the poller's initial baseline before arming power edges.
It uses a recoverable event subscription and replaces incomplete event history
with a fresh power snapshot after overflow. Rules that no longer match current
devices/capabilities are disabled at startup rather than blocking device
control.

Do not add scripts, expression trees, per-rule goroutines, retries, a database,
outbound HTTP, or persistent history.
