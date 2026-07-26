# atomberg — Atomberg BLDC fans

`import "setu/internal/devices/atomberg"` · local UDP control, commands on 5600,
beacons + state on 5625, no cloud.

## Protocol
- Full native reference: **`docs/devices/atomberg.md`**.
- Atomberg documents the LAN protocol themselves. The cloud API is deliberately
  unused: 100 calls/day cannot support polling.

## Files
- `listener.go` — `Discoverer`, the **shared** UDP :5625 listener. Implements
  `resolver.Resolver` (`Lookup`: a beacon's source address is the fan's IP) and
  `resolver.Scanner` (`Scan`: every MAC heard recently), and pushes decoded
  state to each fan.
- `state.go` — packet classification (beacon vs hex-encoded state) and the
  `state_string` bitfield decoder.
- `atomberg.go` — `base` (transport, resolve chain, state) + the `Fan` and
  `FanLight` models.

## Why one shared listener
Every other brand opens a socket per exchange, because it asks a question and
reads the answer. An Atomberg fan is never asked: it broadcasts to a **fixed**
port, once a second. That port can only be bound once, so a socket per device
would mean fans competing for the same datagrams. One process-wide listener,
created lazily, serves every fan and the scanner — which also keeps registration
in `cmd/setu/main.go` to the usual single line.

## Capabilities → protocol
- `switch` → `{"power":bool}`
- `speed` → `{"speed":n}` (1–6; 6 is the boost step)
- `sleep` → `{"sleep":bool}`
- `timer` → `{"timer":index}` — **hours ≠ index**: 6 h is index 4. `TimerOptions`
  reports hours (0, 1, 2, 3, 6) and the driver owns the translation.
- `light` → `{"led":bool}` — a plain on/off toggle, the whole light control on a
  `fan`.
- `brightness` → a dimmable light on `fan_light`, where **0 means off**:
  `{"led":false}`, or `{"led":true}` then `{"brightness":max(10,pct)}`.
- `scene` → `{"light_mode":"warm"|"cool"|"daylight"}` (`fan_light` only), three
  static presets; `SetSceneSpeed` no-ops.
- `Poll` → `{"speedDelta":0}`, the documented no-op that makes the fan
  re-broadcast its state.

## Light handling, and one seam reused
- **A lamp that only switches gets `light`.** `Switchable` is already the fan's
  power and a lamp with no levels is not `Dimmable`, so `fan` exposes `light`: a
  toggle, the same shape as sleep mode. `fan_light` uses `brightness` (0 = off)
  because that hardware genuinely dims. A model never offers both — a dimmer for
  a lamp with no levels would be a control that does nothing.
- **`light_mode` is `scene`.** Three fixed colour modes are a preset picker, not
  a range a slider could land between.

## Resolution
- cached IP → **beacon** → ARP. The beacon comes first here, unlike the other
  brands: it is the fan reporting its own address every second, where ARP is the
  host's possibly stale memory. Any send failure invalidates the cache.

## Push, not just poll
The hardware broadcasts its state after *any* change — including one made with
the physical remote or the Atomberg app — so those reach the UI in well under a
second without waiting for the poll cadence. Broadcasts are deduped on
`message_id`.

## Scan → model
- The beacon's series decides the driver: `I1, I5, M1, S1, S2` → `fan_light`;
  `R1, R2, R3, K1, I2, I3, I4, M2` → `fan`.
- Any other series is reported with an **empty model** — found, but no driver —
  never guessed at, because commanding a fan through the wrong driver would
  silently do the wrong thing.

## Status
- **Verified end to end (2026-07-26) against a protocol-faithful simulator**
  built from `docs/devices/atomberg.md`: discovery, add, every command's wire
  payload, state decoding, validation, and sub-100 ms push of an out-of-band
  change. The light is a toggle on `fan` and a dimmer on `fan_light`.
- ⚠️ **Not yet verified against physical hardware.** In particular, whether the
  Renesa Halo (`R1`/`R2`) dims its light is unresolved — vendor docs say no, the
  product page says yes. See `docs/devices/atomberg.md` §9 for the one-command
  test; both drivers exist, so it is a choice at add time.
