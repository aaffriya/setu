# Atomberg local fan protocol

Source for `internal/devices/atomberg`. The local protocol is UDP with no
authentication, encryption, or command acknowledgement.

## 1. Transport

| Item | Value |
| --- | --- |
| Commands | unicast JSON to UDP `5600` |
| Beacons and state | broadcast to UDP `5625` |
| Identity | MAC from beacon/state |
| State request | `{"speedDelta":0}` |

The cloud API is not used; its daily request limit cannot support polling.

## 2. Commands

| Feature | Datagram |
| --- | --- |
| power | `{"power":true|false}` |
| speed | `{"speed":1..6}` |
| sleep | `{"sleep":true|false}` |
| timer | `{"timer":0..4}` |
| light | `{"led":true|false}` |
| brightness | `{"brightness":10..100}` |
| light mode | `{"light_mode":"warm"|"cool"|"daylight"}` |
| request state | `{"speedDelta":0}` |

Timer wire values mean `0, 1h, 2h, 3h, 6h`; six hours is sent as index `4`.
Commands have no direct reply. A later state broadcast is the only confirmation.

## 3. Port 5625 packets

Two packet types share one port.

### Beacon

```text
<mac12>_<SERIES>
```

The source address is the fan's current IP. Beacons arrive about once per
second and drive discovery, address resolution, and liveness.

### State

State is hex-encoded JSON containing `device_id`, `message_id`, and
`state_string`. Deduplicate retransmissions by `message_id`.

`state_string` is comma-separated. Field 0 is a **decimal** integer bitfield;
field 7 is the series. Current decoding:

```text
power        value & 0x10 != 0
light        value & 0x20 != 0
sleep        value & 0x80 != 0
speed        value & 0x07
brightness   (value & 0x7f00) >> 8
cool         value & 0x08 != 0
warm         value & 0x8000 != 0
timer_hours  (value & 0x0f0000) >> 16
elapsed_min  (value >> 24) * 4
```

Both colour bits set means daylight. The packet classifier attempts hex decode;
a beacon contains `_` and is therefore unambiguous.

## 4. Setu implementation

One process-wide listener owns port `5625`. A socket per fan would compete for
the same broadcasts. The listener:

- implements `resolver.Resolver` from beacon source addresses;
- implements `resolver.Scanner` from recent sightings;
- decodes and pushes out-of-band state changes to registered fan instances.

Resolution is cached IP → beacon → ARP. Send failures clear the cache. `Poll`
sends the no-op state request and waits for a newer listener revision.

## 5. Models

| Beacon series | Model | Light control |
| --- | --- | --- |
| `I1`, `I5`, `M1`, `S1`, `S2` | `fan_light` | brightness plus warm/cool/daylight scenes |
| `R1`, `R2`, `R3`, `K1`, `I2`, `I3`, `I4`, `M2` | `fan` | on/off light |
| unknown | empty | discovered but unsupported |

Both models expose power, speed 1–6, sleep, and timer `0/1/2/3/6h`.
`fan_light` uses brightness `0` as light-off and implements static scene modes;
`fan` uses the separate `light` toggle.

## 6. Important failures

- No acknowledgement means send success is not device confirmation.
- A passive listener may have no state until `speedDelta:0` primes the device.
- Unknown series must not be guessed.
- Anyone on the LAN can command a fan; keep the segment trusted.
- Reuse-port behavior is platform-specific and isolated in
  `reuseport_<platform>.go`.

Discovery, commands, decode, validation, and push updates are covered with a
protocol-faithful simulator. Physical Atomberg hardware remains unverified.
