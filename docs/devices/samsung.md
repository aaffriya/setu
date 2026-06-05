# Samsung Tizen TV — native protocol reference

Reference for controlling a Samsung Tizen TV (e.g. UA50AU7700, API v2,
`TokenAuthSupport: true`) directly, and how Setu's `internal/devices/samsung`
package maps onto it.

---

## 1. The one concept that decides everything

A TV is split across **three transports** — only the key channel is hard:

| Job | Transport | Port | Notes |
|---|---|---|---|
| Device info, app status, launch/close | **HTTP REST** (DIAL) | 8001 | plain `curl` |
| **Remote keys** (power, volume, nav…) | **WebSocket over TLS** | 8002 | token auth; frames are masked |
| Power **ON** | **Wake-on-LAN** | UDP 9 | unreliable over Wi-Fi |

Use **8002 (wss) + token**, not 8001 — on token-auth models the 8001 socket
connects but keys silently do nothing.

---

## 2. REST (DIAL) — port 8001

```bash
# device info (also a liveness/power probe)
curl http://192.168.0.100:8001/api/v2/

# app status (installed / running / version)
curl http://192.168.0.100:8001/api/v2/applications/111299001912

# launch app  (first time: TV shows an "Allow" prompt)
curl -X POST   http://192.168.0.100:8001/api/v2/applications/111299001912
# close app
curl -X DELETE http://192.168.0.100:8001/api/v2/applications/111299001912
```

Secure variant: `curl -k https://192.168.0.100:8002/api/v2/...` (self-signed cert).

---

## 3. WebSocket remote keys — port 8002

### 3.1 Endpoint

```
wss://192.168.0.100:8002/api/v2/channels/samsung.remote.control?name=<BASE64>&token=<TOKEN>
```

- `name` = base64 of any label (shows in the TV's *Device Connection Manager*).
- `token` = omit on first connect; the TV returns one after you tap **Allow**.
  Save it and append it on every later connect.

### 3.2 Handshake & token

Standard RFC 6455 upgrade over TLS. On success the TV immediately emits:

```json
{"event":"ms.channel.connect","data":{"token":"12345678","clients":[...]}}
```

→ read `data.token`, persist it.

### 3.3 Send a key (single press)

```json
{"method":"ms.remote.control","params":{
  "Cmd":"Click","DataOfCmd":"KEY_VOLUP","Option":"false","TypeOfRemote":"SendRemoteKey"}}
```

Hold = `"Cmd":"Press"` … `"Cmd":"Release"`. List apps over the same socket:
`{"method":"ms.channel.emit","params":{"event":"ed.installedApp.get","to":"host"}}`.

### 3.4 Frame masking

Client→server frames **must be XOR-masked** (RFC 6455); server→client are not.
A real WebSocket library handles this — Setu uses `github.com/coder/websocket`,
so the masking in the raw reference clients is not re-implemented.

---

## 4. Wake-on-LAN — power on

Magic packet = `FF*6` + `MAC*16`, broadcast to `255.255.255.255:9`.

```python
import socket
mac = bytes.fromhex("A0D7F39E74B2")
pkt = b"\xff"*6 + mac*16
s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
s.setsockopt(socket.SOL_SOCKET, socket.SO_BROADCAST, 1)
s.sendto(pkt, ("255.255.255.255", 9))
```

Setu sprays the packet at the **limited broadcast and every interface's directed
broadcast** (e.g. `192.168.0.255`), on ports **9 and 7** — directed broadcast is what
actually woke this unit (`255.255.255.255` alone did not).

> ✅ **Verified (2026-06-04):** WoL woke the TV from off, then token pairing + volume/mute
> keys worked over the WebSocket.
> ⚠️ WoL over Wi-Fi can still fail if the TV's network-standby ("Power On with Mobile") is
> off. Power-OFF over the WebSocket (`KEY_POWER`) always works when the TV is on.

---

## 5. Key codes (common subset)

Send as `DataOfCmd`. Full set is firmware-specific; `ed.installedApp.get` + trial
is the only definitive list.

| Group | Keys |
|---|---|
| Power | `KEY_POWER` (toggle), `KEY_POWEROFF`, `KEY_POWERON` |
| Volume | `KEY_VOLUP`, `KEY_VOLDOWN`, `KEY_MUTE` |
| D-pad | `KEY_UP` `KEY_DOWN` `KEY_LEFT` `KEY_RIGHT` `KEY_ENTER` (OK) `KEY_RETURN` (back) `KEY_EXIT` |
| Home/menus | `KEY_HOME` `KEY_MENU` `KEY_TOOLS` `KEY_GUIDE` `KEY_INFO` `KEY_CONTENTS` |
| Channels | `KEY_CHUP` `KEY_CHDOWN` `KEY_CH_LIST` `KEY_PRECH` |
| Numbers | `KEY_0` … `KEY_9` |
| Media | `KEY_PLAY` `KEY_PAUSE` `KEY_STOP` `KEY_REWIND` `KEY_FF` `KEY_REC` |
| Source | `KEY_SOURCE` `KEY_HDMI` `KEY_TV` `KEY_AMBIENT` |
| Color | `KEY_RED` `KEY_GREEN` `KEY_YELLOW` `KEY_BLUE` |

> `KEY_FACTORY` opens the service menu — Setu **refuses** it.

---

## 6. App IDs (REST launch / DIAL)

IDs change per release; pull the live list with `ed.installedApp.get` if one fails.

| App | Numeric ID | Alphanumeric |
|---|---|---|
| YouTube | `111299001912` | — |
| Netflix | `11101200001` / `3201907018807` | `org.tizen.netflix-app` |
| Prime Video | `3201910019365` (confirmed) · `3201512006785` (older) | `org.tizen.ignition` |
| Skypro | `3202410037378` (confirmed) | — |
| Spotify | `3201606009684` | — |
| Apple Music | `3201908019041` (verify) | — |
| Browser | — | `org.tizen.browser` |

> App ids vary by firmware/region. If a launch 404s, Setu self-heals by matching
> the app name against `ed.installedApp.get` (§3.3); you can also confirm an id with
> `GET /api/v2/applications/<id>` (installed/running). "(confirmed)" = seen live on a
> UA50AU7700 in 2026-06.

---

## 7. Gotchas

1. **Use 8002 + token**, not 8001 (keys no-op on 8001 for token-auth TVs).
2. **"Allow" prompt:** first connect needs an on-screen Allow. Set *General →
   External Device Manager → Device Connection Manager → Access Notification* to
   "First Time Only".
3. **Same L2 segment.** Samsung blocks the remote WS across subnets/VLANs — keep
   the controller and TV on the same network.
4. **Pin the IP** (DHCP reservation for the MAC) so scripts don't break.
5. **Flush before close.** Sending a key then closing immediately drops it — wait
   ~500 ms (or keep the socket open).
6. **Self-signed cert** on 8002/https — clients skip verification.
7. **`firmwareVersion: Unknown`** in the info JSON is normal; doesn't affect control.

---

## 8. How Setu implements this

Package `internal/devices/samsung` (`go doc setu/internal/devices/samsung`).

| Capability (UI) | Method | Transport |
|---|---|---|
| `switch` on | `On` | Wake-on-LAN (§4) |
| `switch` off | `Off` | WS `KEY_POWER` (§3.3) |
| `volume` | `VolumeUp/Down/ToggleMute` · `SetVolume(pct)` | WS `KEY_VOLUP/VOLDOWN/MUTE` |
| `key` | `SendKey("KEY_…")` | WS, validated `^KEY_[A-Z0-9_]+$` |
| `app` | `Apps` / `LaunchApp(id)` | REST `POST /api/v2/applications/<id>` (§2, §6) |
| (internal) | `Poll` | REST `/api/v2/` reachability (§2) |

- **Reused WebSocket:** the remote-control socket is opened once (capture token) and
  reused for subsequent keys, then idle-closed after ~45 s. A stale socket (the TV
  drops idle ones) is detected on the next write and redialed once, so reliability
  matches a one-shot dial while avoiding a TLS dial + ~500 ms flush per press. Writes
  are serialized; a drain reader keeps control frames handled and refreshes the token.
- **Absolute volume (tracked):** the remote channel has no "set volume" — only
  step up/down/mute, and the TV **debounces rapid identical keys** (a fast burst lands
  as one step). So `SetVolume(pct)` tracks the level and steps to the target with
  presses **paced ~120 ms apart** (tunable: `volumePace`). The ramp runs in the
  background (the UI reflects the target immediately) and a newer `set_volume`
  supersedes an in-flight one. Sliding fully to **0 or 100 overshoots to the rail**,
  re-calibrating the tracked level if it drifted (e.g. the physical remote). `State.Volume`
  is the tracked estimate — it can't be read back from this channel.
- **Token cache:** `$SETU_STATE_DIR/setu-samsung-<id>.token` (defaults to OS temp;
  set `SETU_STATE_DIR` to a persistent dir to survive reboots), mode `0600`.
- **App shortcuts** (`LaunchApp`): a fixed catalog (YouTube / Netflix / Prime Video /
  Skypro / Apple Music) launched over DIAL REST; `LaunchApp` only accepts an id from that catalog.
  Each app tries its known ids in order; **if all 404, it self-heals** by querying the
  TV's real installed apps (`ed.installedApp.get`, §3.3), matching by name, launching
  that id, and caching it. The UI shows the buttons from `Apps()` (icon-only). HDMI rides
  in the same shortcut grid but is a `KEY_HDMI` remote key, not an app.
- **`Poll` / online vs. on:** `Poll` reads REST reachability as the **live power
  signal** (reachable ⇒ on, unreachable ⇒ off), like the WiZ bulb's `getPilot` poll —
  so powering the TV on/off out-of-band (e.g. the physical remote) is reflected on the
  next tick. Reachability is a *power* proxy, not a presence proxy: an off TV stops
  answering REST but can still be woken by WoL, so the TV is reported **online whenever
  its address resolves** (config hint / ARP). That keeps off ≠ offline, so the UI never
  hides the power control needed to wake it. Right after an explicit `On`/`Off` the
  command is trusted for a short **grace window** (~10 s): the TV keeps answering REST
  for a few seconds while it powers down, which would otherwise flicker the polled
  state. `Poll` can't read volume. (Caveat: a TV with network-standby that keeps
  answering REST while "off" will read as on once the grace window passes — REST alone
  can't distinguish standby from on.)

**Known tradeoffs (not bugs):** `On` returns success once the WoL packet is sent
(it can't confirm the TV woke — the next poll reconciles); the volume slider's thumb
is indicative, not the TV's true level (the protocol has no absolute volume — an
absolute slider would need UPnP/SmartThings).
