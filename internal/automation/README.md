# `internal/automation`

Bounded server-side rules that continue running when no browser is open.

## Model and limits

- Trigger: minute schedule, device state, device unreachable, LAN presence, or
  authenticated webhook.
- A device-state trigger watches either the power edge (the default) or one
  reported number — brightness, speed, volume, colour temperature, timer hours —
  against `above`, `below` or `equals`. It fires on the crossing, never while the
  value merely keeps matching, and a numeric metric additionally requires the
  device to be reachable so a lost connection cannot read as "brightness fell".
- A device-unreachable trigger fires once per episode, after 1–1440 minutes, and
  rearms only when the device is seen online again. It is evaluated on the same
  minute tick as schedules, so it costs no extra goroutine.
- A presence trigger watches a MAC in the host's neighbour table, polled every
  30 seconds. It is best-effort: an entry lingers after a device leaves and can
  vanish while a phone sleeps, which is why its stable time goes to 900 seconds.
  Where the host has no neighbour table the engine refuses such rules instead of
  storing ones that would never fire; `Snapshot` reports this as `presence`.
- Up to 64 rules, 4 AND conditions, and 16 ordered actions per rule.
- Optional stable time up to 300 seconds (900 for presence), cooldown up to 3600
  seconds, and per-action delay up to 60 seconds.
- Two workers, queue size 32, last 20 run results in RAM.
- Cooldown starts only after the queue accepts a run; a full queue does not
  consume it.
- A rule may call another enabled rule inline. Calls must be acyclic, at most 8
  levels, 128 actions, and 960 delay-seconds per run.
- Rules whose triggers and actions form a feedback loop are rejected. The graph
  is over rules: an edge exists when one rule's actions (including those of
  rules it calls) could produce a state another rule's trigger matches. Power is
  treated coarsely — any on/off can feed any power trigger, which is the
  guarantee this has always given — while a numeric action is compared exactly,
  so "dim to 40% above 70%" settles and is allowed, and a pair that would
  oscillate is not. Setting a brightness or a speed also counts as switching the
  device on, because on most hardware it does.

Only bounded, idempotent device actions are allowed automatically. Remote-key
taps/holds, relative volume, mute toggles, and text input are intentionally not
automation-safe.

## Persistence and runtime

Rules occupy the `automations` section of `$SETU_STATE_DIR/setu.json` through
`internal/store`. Queue state, rate limits, cooldown clocks, and run history are
not persisted.

A configuration change resets per-rule clocks and webhook bookkeeping, but not
the record of which offline rules have already fired: clearing that would rearm
them all, so editing an unrelated rule during an outage would announce that
outage again. An offline rule rearms when its device is seen back, and nowhere
else.

Webhook plaintext is returned only on creation/rotation. The stored/exported
form contains its SHA-256 hash. Webhook payloads never select actions.

The engine waits for the poller's initial baseline before arming state edges;
devices already unreachable at startup start their offline clock then, and the
first presence read is a baseline rather than an arrival.
It uses a recoverable event subscription and replaces incomplete event history
with a fresh state snapshot after overflow. Rules that no longer match current
devices/capabilities are disabled at startup rather than blocking device
control.

Do not add scripts, expression trees, per-rule goroutines, retries, a database,
outbound HTTP, or persistent history.
