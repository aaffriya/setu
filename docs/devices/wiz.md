# Philips WiZ — native protocol reference

Reference for controlling WiZ bulbs directly, and for how Setu's
`internal/devices/wiz` package maps onto it. Point-to-point so a human or an AI
can implement/extend it without re-deriving the protocol.

---

## 1. At a glance

| Item | Value |
|---|---|
| Transport | **UDP**, one JSON object per datagram |
| Port | **38899** |
| Cloud / login / key | **None** — pure LAN |
| Read state | `{"method":"getPilot","params":{}}` |
| Write state | `{"method":"setPilot","params":{...}}` |
| Discovery | broadcast `getPilot` → every bulb replies with its `mac` |
| Identity | bulb `mac` (reported as bare hex, e.g. `d8a011ff5ef0`) |

---

## 2. Commands

### 2.1 getPilot (read)

Request:

```json
{"method":"getPilot","params":{}}
```

Reply (`result` fields vary by mode):

```json
{"method":"getPilot","env":"pro","result":{
  "mac":"d8a011ff5ef0","rssi":-61,"state":true,
  "sceneId":0,"r":255,"g":100,"b":0,"dimming":60
}}
```

- An **off** bulb may omit `dimming`/`r`/`g`/`b` and report only `sceneId`.
- A bulb in **scene mode** reports `sceneId` (no `r`/`g`/`b`).
- A **tunable-white** bulb reports `temp` instead of `r`/`g`/`b`.

### 2.2 setPilot (write)

```json
{"method":"setPilot","params":{ <one or more params> }}
```

Reply:

```json
{"method":"setPilot","env":"pro","result":{"success":true}}
```

| param | range | effect |
|---|---|---|
| `state` | `true` / `false` | on / off |
| `dimming` | **10**–100 | brightness % (hardware floor is 10) |
| `r`,`g`,`b` | 0–255 | RGB color (color bulbs only) |
| `temp` | ~2200–6500 | white color temperature (Kelvin) |
| `sceneId` | 1–32 | preset scene |
| `speed` | 10–200 | scene animation speed |

**Mutual exclusivity:** setting `r,g,b` puts the bulb in **color mode**; setting
`temp` puts it in **white mode**; setting `sceneId` puts it in **scene mode**.
Switching modes clears the previous one (e.g. an RGB command drops the scene).

---

## 3. Discovery (MAC → IP)

WiZ resolves its own address without ARP: broadcast a `getPilot`, collect the
replies, and the reply carrying your target `mac` came from the bulb's current IP
(the UDP source address).

```text
send  → 255.255.255.255:38899   {"method":"getPilot","params":{}}
recv  ← 192.168.0.140:38899      {... "result":{"mac":"d8a011ff5ef0", ...}}
```

The sending socket needs `SO_BROADCAST`. This is why a DHCP IP change "just
works": rediscover by MAC.

---

## 4. Raw examples (zero install)

```bash
# read state
echo -n '{"method":"getPilot","params":{}}' | nc -u -w1 192.168.0.140 38899

# on / off
echo -n '{"method":"setPilot","params":{"state":true}}'  | nc -u -w1 192.168.0.140 38899
echo -n '{"method":"setPilot","params":{"state":false}}' | nc -u -w1 192.168.0.140 38899

# brightness / color / white / scene
echo -n '{"method":"setPilot","params":{"state":true,"dimming":60}}'   | nc -u -w1 192.168.0.140 38899
echo -n '{"method":"setPilot","params":{"r":255,"g":100,"b":0}}'       | nc -u -w1 192.168.0.140 38899
echo -n '{"method":"setPilot","params":{"temp":2700,"dimming":80}}'    | nc -u -w1 192.168.0.140 38899
echo -n '{"method":"setPilot","params":{"sceneId":12,"speed":120}}'    | nc -u -w1 192.168.0.140 38899
```

---

## 5. Gotchas

1. **Min dimming = 10.** Values below 10 are ignored by the bulb; clamp to 10.
2. **Color vs white vs scene are exclusive** (see §2.2).
3. **White-only bulbs ignore `r,g,b`.** If `getPilot` never returns `r/g/b`
   (only `temp`), the bulb is tunable-white — use `temp`, not RGB.
4. **IP is DHCP** → keep MAC as the identity and (re)discover; an `ip` is only a
   hint/fallback.
5. **UDP is fire-and-mostly-reply.** `setPilot` replies with `success`; treat a
   missing reply as a failure and retry / re-resolve.

---

## 6. How Setu implements this

Package `internal/devices/wiz` (`go doc setu/internal/devices/wiz`).

| Capability (UI) | Method | setPilot params |
|---|---|---|
| `switch` | `On` / `Off` | `{"state":true|false}` |
| `brightness` | `SetBrightness(pct)` | `{"state":true,"dimming":max(10,pct)}` |
| `color` | `SetColor(r,g,b)` | `{"state":true,"r","g","b"}` |
| `color_temp` | `SetColorTemp(k)` | `{"state":true,"temp":clamp(2200,6500)}` |
| `scene` | `SetScene(id)` / `Scenes()` | `{"state":true,"sceneId":id}` |
| `scene` (speed) | `SetSceneSpeed(s)` | `{"speed":clamp(10,200)}` (dynamic scenes) |
| (internal) | `Poll` | `getPilot` → map to `device.State` |

- `wiz.go` — `base` (UDP transport, resolve chain, state), `ColorBulb` model.
- `discovery.go` — `Discoverer` implements `resolver.Resolver` via the broadcast in §3.
- `scenes.go` — the named scene catalogue (§7) exposed via `Scenes()`.
- Resolve order: cached IP → ARP table → WiZ discovery → `ip` hint. On any UDP
  failure the cached IP is invalidated so the next call re-resolves.
- **Modes are exclusive:** setting color clears temp+scene, setting temp clears
  scene, in both the bulb and `device.State` (`color_temp`/`scene` = 0 when
  inactive), so the UI can tell which mode is live.

**Adding a tunable-white-only model:** copy `ColorBulb`, drop `ColorControl`
(keep `ColorTempControl`).

---

## 7. Scene catalogue (ids 1–32)

WiZ scene ids are fixed; `Scenes()` returns them as `{id, name}` for the UI.

| id | name | id | name | id | name | id | name |
|--|--|--|--|--|--|--|--|
| 1 | Ocean | 9 | Wake up | 17 | True colors | 25 | Mojito |
| 2 | Romance | 10 | Bedtime | 18 | TV time | 26 | Club |
| 3 | Sunset | 11 | Warm White | 19 | Plantgrowth | 27 | Christmas |
| 4 | Party | 12 | Daylight | 20 | Spring | 28 | Halloween |
| 5 | Fireplace | 13 | Cool white | 21 | Summer | 29 | Candlelight |
| 6 | Cozy | 14 | Night light | 22 | Fall | 30 | Golden white |
| 7 | Forest | 15 | Focus | 23 | Deepdive | 31 | Pulse |
| 8 | Pastel Colors | 16 | Relax | 24 | Jungle | 32 | Steampunk |

**Dynamic** (animated, speed-adjustable — the app's *Dynamic* group): ids **1–5, 7, 8,
20–29, 31, 32**. The rest are static (the app's *White / Functional / Progressive*) and
ignore `speed`. Setu marks these via `Scene.Dynamic` and only shows the speed slider for them.

---

## 8. Verified (2026-06-03, bulb `d8:a0:11:ff:5e:f0`)

Setu command → independent `getPilot` readback:

| command | bulb after |
|---|---|
| `on` | `state:true, dimming:25` |
| `set_brightness 60` | `dimming:60` |
| `set_color {255,100,0}` | `r:255,g:100,b:0` (sceneId→0, color mode) |
| `off` | `state:false` |
