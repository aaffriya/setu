# Atomberg — native protocol reference

Reference for controlling Atomberg BLDC fans directly, and for how Setu's
`internal/devices/atomberg` package maps onto it. Point-to-point so a human or
an AI can implement/extend it without re-deriving the protocol.

Atomberg documents this LAN protocol themselves, in the same OpenAPI spec that
describes their cloud API — local control is supported, not reverse-engineered.

---

## 1. At a glance

| Item | Value |
|---|---|
| Transport | **UDP**, plain JSON, no auth, no encryption, **no ack** |
| Command port | **5600** (unicast to the fan) |
| Beacon + state port | **5625** (the fan broadcasts) |
| Cloud / login / key | **None** for local control |
| Read state | send `{"speedDelta":0}` → fan broadcasts its state |
| Write state | one command JSON per datagram (§2) |
| Discovery | the fan broadcasts `<mac>_<series>` every second |
| Identity | the fan's MAC, bare lowercase hex (e.g. `a0764e5aee98`) |

The cloud API (`https://api.developer.atomberg-iot.com`) exists and exposes the
same commands, but is capped at **100 calls/day and 5/second**, which cannot
support polling. Setu does not use it.

---

## 2. Commands

One JSON object per datagram, sent to `<fan-ip>:5600`. There is **no reply**:
the fan's state broadcast (§3.2) is the only confirmation.

| Command | JSON | Accepted values |
|---|---|---|
| Power | `{"power":val}` | `true`, `false` |
| Speed (absolute) | `{"speed":val}` | `1`–`6` (6 = boost) |
| Speed (relative) | `{"speedDelta":val}` | `-5`…`-1`, `1`…`5`, and **`0` = no-op** |
| Sleep mode | `{"sleep":val}` | `true`, `false` |
| Timer | `{"timer":val}` | `0`,`1`,`2`,`3`,`4` → **off, 1 h, 2 h, 3 h, 6 h** |
| Light on/off | `{"led":val}` | `true`, `false` |
| Brightness | `{"brightness":val}` | `10`–`100` (percent) |
| Brightness delta | `{"brightnessDelta":val}` | `-90`…`+90` |
| Light colour | `{"light_mode":val}` | `"warm"`, `"cool"`, `"daylight"` |

`{"speedDelta":0}` is the key to reading state without the cloud: it changes
nothing, but the fan still answers it with a full state broadcast.

---

## 3. What the fan broadcasts (port 5625)

Two different packet shapes share this port. Hex-decode first — a beacon
contains `_` and is not valid hex, so the discrimination is unambiguous.

### 3.1 Beacon — every second

Short ASCII, `<mac12>_<SERIES>`:

```text
a0764e5aee98_R1
```

The source address of that datagram is the fan's IP. This makes address
resolution self-correcting: a DHCP change is picked up within a second, with no
ARP table and no scan. A fan that beacons is powered and on the network, so it
also gives liveness for free.

Some networks prepend a PROXY-protocol line
(`PROXY TCP4 <src> <dst> <dport> <sport> <payload>`); strip it before parsing.

### 3.2 State — after any change

The datagram is the **hex encoding** of JSON bytes. Decode the hex, then parse:

```json
{"device_id":"a0764e5aee98","message_id":"AcmBijmomfnBJbHcA",
 "state_string":"20,1,B,5,50.00,0,0,R1,2802,1,45142,0,0,0,0,0.00,0.00,0,0,0,END"}
```

`message_id` identifies the broadcast; a repeat of one already seen is a
retransmit, not a new change. In `state_string`, **field 0 is a decimal integer
bitfield** and **field 7 is the series**. The rest is undocumented vendor
diagnostics, ending in `END`.

Field 0 decodes with Atomberg's own masks:

```
power       = value & 0x10       != 0
led         = value & 0x20       != 0
sleep       = value & 0x80       != 0
speed       = value & 0x07                 // 1..6
brightness  = (value & 0x7F00)   >> 8      // percent, light models only
cool        = value & 0x08       != 0
warm        = value & 0x8000     != 0      // cool && warm  =>  "daylight"
timer_hours = (value & 0x0F0000) >> 16     // reads back as 0,1,2,3,6
elapsed_min = (value >> 24) * 4            // 4-minute ticks
```

So the vendor's own example, `20` (`0x14`), means: powered on, speed 4, light
off, no timer.

---

## 4. Raw examples (zero install)

Watch the fans announce themselves and report state:

```bash
python3 -c "import socket;s=socket.socket(socket.AF_INET,socket.SOCK_DGRAM);s.setsockopt(socket.SOL_SOCKET,socket.SO_REUSEADDR,1);s.bind(('0.0.0.0',5625));print('listening');[print(s.recvfrom(4096)) for _ in iter(int,1)]"
```

Ask a fan for its state (substitute its IP):

```bash
printf '{"speedDelta":0}' | nc -u -w1 192.168.1.50 5600
```

Set it to speed 4:

```bash
printf '{"speed":4}' | nc -u -w1 192.168.1.50 5600
```

---

## 5. Gotchas

1. **Timer index ≠ timer hours.** You *send* `{"timer":4}` for six hours, but
   read back `timer_hours: 6`. This is the single easiest thing to get wrong.
2. **Two packet shapes on one port.** Port 5625 carries both short ASCII beacons
   and long hex-encoded state. Classify by attempting a hex decode, not by length.
3. **Field 0 is decimal, not hex.** `20` in `state_string` is `0x14`, not `0x20`.
4. **No acknowledgement.** Commands are fire-and-forget; the following state
   broadcast is the only confirmation. A lost datagram looks like silence.
5. **State is only broadcast after a change.** A fan that nobody has touched
   never reports anything, so a passive listener stays empty forever — send the
   `{"speedDelta":0}` no-op to prime it.
6. **Dedupe on `message_id`.** A single change may be broadcast more than once.
7. **No authentication whatsoever.** Anyone on the LAN can command any fan.
   That is the protocol's design, not Setu's choice — keep IoT devices on a
   network segment you trust.
8. **`cool` and `warm` are a 2-bit enum, not two flags.** Both set means
   *daylight*; neither set means the model has no colour control at all.
9. **IP is DHCP** → store only the MAC and take the address from the beacon.

---

## 6. How Setu implements this

Package `internal/devices/atomberg` (`go doc setu/internal/devices/atomberg`).

| Capability (UI) | Method | wire |
|---|---|---|
| `switch` | `On` / `Off` | `{"power":true\|false}` |
| `speed` | `SetSpeed(n)` / `SpeedRange()` | `{"speed":n}`, n ∈ 1–6 |
| `sleep` | `SetSleep(on)` | `{"sleep":true\|false}` |
| `timer` | `SetTimer(h)` / `TimerOptions()` | `{"timer":index}` — hours→index mapped by the driver |
| `light` | `SetLight(on)` | `{"led":true\|false}` (`fan`) |
| `brightness` | `SetBrightness(pct)` | `0` → `{"led":false}`; `n` → `{"led":true}` then `{"brightness":max(10,n)}` (`fan_light`) |
| `scene` | `SetScene(id)` / `Scenes()` | `{"light_mode":"warm"\|"cool"\|"daylight"}` |
| (internal) | `Poll` | `{"speedDelta":0}` → read the state broadcast |

- `listener.go` — the shared UDP :5625 listener. **One socket for every fan**,
  because the beacon port is fixed and cannot be bound per device. It implements
  `resolver.Resolver` (`Lookup`: the beacon's source address) and
  `resolver.Scanner` (`Scan`: every MAC heard recently), and pushes decoded
  state to each fan's callback.
- `state.go` — packet classification and the §3.2 bitfield decoder.
- `atomberg.go` — `base` (transport, resolve chain, state) plus the `Fan` and
  `FanLight` models.
- Resolve order: cached IP → **beacon** → ARP table. The beacon comes first here,
  unlike the other brands: it is the fan itself reporting its address, where ARP
  is the host's possibly stale memory. Any send failure invalidates the cache.
- **Out-of-band changes are pushed, not polled.** Because the fan broadcasts
  after *any* change, a speed change made with the physical remote or the
  Atomberg app reaches the UI in well under a second, without waiting for the
  poll cadence.

### Two design decisions worth knowing

**A light that only switches gets its own capability.** `Switchable` is
already the fan's power, and a lamp with no levels is not `Dimmable`, so
`fan` models expose `light` — a plain on/off toggle, the same shape as sleep
mode. `fan_light` models, whose hardware genuinely dims, use `brightness`
instead, where 0 means off. A model never offers both: a dimmer for a lamp with
no levels would be a control that does nothing.

**`light_mode` is exposed as `scene`.** Three fixed colour modes are a preset
picker, not a continuous range — a `color_temp` slider would only snap to three
positions. `SetSceneSpeed` is a no-op, as on the tunable-white WiZ bulb.

### Model selection

`modelFor(series)` maps the series in the beacon to the driver:

| Series | Model | Light |
|---|---|---|
| `I1`, `I5`, `M1`, `S1`, `S2` | `fan_light` | dimmable + warm/cool/daylight |
| `R1`, `R2`, `R3`, `K1`, `I2`, `I3`, `I4`, `M2` | `fan` | on/off only |
| anything else | `""` | found, but **no driver** — never guessed at |

---

## 7. Series → hardware

| Series | Products |
|---|---|
| `R1`/`R2` | Renesa, Renesa+, Renesa Halo, Studio+ |
| `R3` | Renesa variants |
| `K1` | Erica |
| `I1`/`I5` | Aris Starlight (light + colour modes) |
| `I2`–`I4` | Aris (no light control) |
| `M1` | Aris Contour (dimmable, no colour) |
| `S1`/`S2` | Renesa Elite, Studio Nexus (dimmable, no colour) |

---

## 8. Verified (2026-07-26, protocol-level)

Verified against a protocol-faithful simulator built from this document, driving
the real driver end to end through Setu's API and UI — discovery, add, every
command, state decode, and the push path.

| Setu command | on the wire | decoded back |
|---|---|---|
| `on` | `{"power":true}` | `on: true` |
| `set_speed 5` | `{"speed":5}` | `speed: 5` |
| `set_sleep true` | `{"sleep":true}` | `sleep: true` |
| `set_timer 6` | `{"timer":4}` | `timer_hours: 6` |
| `set_brightness 45` (`fan_light`) | `{"led":true}` + `{"brightness":45}` | `brightness: 45` |
| `set_light true` (`fan`) | `{"led":true}` | `light: true` |
| `set_scene 3` | `{"light_mode":"daylight"}` | `scene: 3` |
| `Poll` | `{"speedDelta":0}` | full state |
| out-of-band `{"speed":6}` | — | UI updated in **< 100 ms**, no poll |

Rejected before reaching the transport: `set_speed 9` (outside 1–6),
`set_timer 5` (not an accepted duration), `set_color` (no such capability).

> ⚠️ **Not yet verified against physical hardware.** See §9.

---

## 9. This unit (reference hardware)

The fan Setu's Atomberg support is intended for:

| Field | Value | Notes |
|---|---|---|
| Product | **Atomberg Renesa Halo**, 1400 mm, Misty Teal | BLDC, IoT model |
| Series | expected `R1`/`R2` | read it from the beacon: `<mac>_<series>` |
| Driver model | expected `fan` | see the open question below |
| MAC | *(unrecorded)* | the beacon reports it |

**Open question — does the Halo's light dim?** Atomberg's own capability table
and the Home Assistant integration both list brightness for `I1, I5, M1, S1, S2`
only, which would make the Halo (`R1`/`R2`) an on/off light. But Atomberg's
product page describes the Halo's ring as a *dimmable* night light ("Moonlight
mode") controlled from the app. These cannot both be right.

To settle it, listen on 5625 for the beacon to get the real series, then:

```bash
printf '{"brightness":40}' | nc -u -w1 <fan-ip> 5600
```

If the `brightness` bits in the next state broadcast change, the fan should be
added as `fan_light` (dimmer slider); if only `{"led":true}` has any effect,
`fan` is correct (light on/off toggle). Both drivers already exist, so this is a
choice of model at add time, not a code change.
