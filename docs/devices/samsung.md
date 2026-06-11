# Samsung Tizen TV — native protocol reference

Reference for controlling a Samsung Tizen TV (e.g. UA50AU7700, API v2,
`TokenAuthSupport: true`) directly, and how Setu's `internal/devices/samsung`
package maps onto it.

---

## 1. The one concept that decides everything

A TV is split across **four transports** — only the key channel is hard:

| Job | Transport | Port | Notes |
|---|---|---|---|
| Device info, app status, launch/close | **HTTP REST** (DIAL) | 8001 | plain `curl` |
| **Remote keys** (power, nav…), text, TV events | **WebSocket over TLS** | 8002 | token auth; frames are masked |
| **Absolute volume %** + mute (set *and* read) | **UPnP SOAP** (RenderingControl) | 9197 | see §3.6 |
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

### 3.3 Send a key — Click vs. Press/Release (⚠️ the rule that matters)

```json
{"method":"ms.remote.control","params":{
  "Cmd":"Click","DataOfCmd":"KEY_VOLUP","Option":"false","TypeOfRemote":"SendRemoteKey"}}
```

`Cmd` has three values and they are **not interchangeable**:

| `Cmd` | Meaning |
|---|---|
| `Click` | press **and** release instantly — normal key taps |
| `Press` | hold the key **down** and leave it down (TV auto-repeats it) |
| `Release` | let the key **up** |

> **A `Press` without its matching `Release` leaves the key stuck down: the TV
> stops responding to *every* other key, and the stuck state survives even a
> reconnect.** The only recovery is sending the matching `Release` (from any
> socket) — or rebooting the TV. Verified live on the AU7700.

List apps over the same socket:
`{"method":"ms.channel.emit","params":{"event":"ed.installedApp.get","to":"host"}}`.

### 3.4 Text input + the TV's IME events

Type into a focused text field (search bars, login forms) with two frames — the
**base64** of the text, then a commit:

```json
{"method":"ms.remote.control","params":{"Cmd":"aGVsbG8=","DataOfCmd":"base64","TypeOfRemote":"SendInputString"}}
{"method":"ms.remote.control","params":{"TypeOfRemote":"SendInputEnd"}}
```

The TV reports its text-field lifecycle as events on the same socket (verified
2026-06, typing both via this API and the physical remote):

| Event | Meaning |
|---|---|
| `ms.remote.imeStart` | a field gained focus (TV keyboard open); `entrylimit` = max length (255 here) |
| `ms.remote.imeUpdate` | `data` = **base64 of the field's full current contents** (not a delta) |
| `ms.remote.imeDone` | `data` `"enable"`/`"disable"` = the IME's done/submit button state — *not* "typing finished" |
| `ms.remote.imeEnd` | input committed / IME closed |

> ⚠️ `imeEnd` is **not guaranteed**: backing out of the field or clicking
> elsewhere emits nothing. A consumer must also clear its mirrored state when it
> sends a focus-leaving action (back/home/exit/power, app launch).

### 3.5 Frame masking

Client→server frames **must be XOR-masked** (RFC 6455); server→client are not.
A real WebSocket library handles this — Setu uses `github.com/coder/websocket`,
so the masking in the raw reference clients is not re-implemented. The TV PINGs
every ~minute and drops the socket without a PONG; the library answers while a
read is pending.

### 3.6 UPnP RenderingControl — absolute volume % and mute

`KEY_VOLUP`/`KEY_VOLDOWN` only nudge ±1 and nothing on the key channel reads the
level back. The TV's MediaRenderer (HTTP SOAP on **:9197**, control URL
`/upnp/control/RenderingControl1`, confirm via `GET :9197/dmr`) gives absolute
0–100 volume and mute, both **settable and readable**:

```bash
curl -s http://192.168.0.100:9197/upnp/control/RenderingControl1 \
  -H 'Content-Type: text/xml; charset="utf-8"' \
  -H 'SOAPACTION: "urn:schemas-upnp-org:service:RenderingControl:1#SetVolume"' \
  --data '<?xml version="1.0" encoding="utf-8"?>
<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">
 <s:Body><u:SetVolume xmlns:u="urn:schemas-upnp-org:service:RenderingControl:1">
   <InstanceID>0</InstanceID><Channel>Master</Channel><DesiredVolume>35</DesiredVolume>
 </u:SetVolume></s:Body></s:Envelope>'
```

Same shape for the rest — swap the action and inner tail:

| Action | Inner tail | Returns |
|---|---|---|
| `GetVolume` | — | `<CurrentVolume>0–100</CurrentVolume>` |
| `SetMute` | `<DesiredMute>1</DesiredMute>` (1 mute, 0 unmute) | — |
| `GetMute` | — | `<CurrentMute>0|1</CurrentMute>` |

> ⚠️ Best-effort across the fleet: some Tizen firmwares disable RenderingControl
> or answer 500 unless media is casting. **Verified working on this AU7700
> (2026-06) while just watching TV.** Channel is `Master`.

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
8. **Every `Press` needs a `Release`** (§3.3) — a stuck press freezes all keys and
   survives reconnects; only the matching `Release` (or a TV reboot) clears it.
9. **Answer PING with PONG** or the socket drops in ~1 min (§3.5; the WS library
   handles it while a read is pending).
10. **A wrong `KEY_*` is silently ignored** (no error event); a wrong `method`
    returns `ms.error`.

---

## 8. How Setu implements this

Package `internal/devices/samsung` (`go doc setu/internal/devices/samsung`).

| Capability (UI) | Method | Transport |
|---|---|---|
| `switch` on | `On` | Wake-on-LAN (§4) |
| `switch` off | `Off` | WS `KEY_POWER` (§3.3) |
| `volume` | `SetVolume(pct)` · `ToggleMute` · `VolumeUp/Down` | UPnP SOAP (§3.6); up/down stay WS keys + UPnP read-back |
| `key` | `SendKey("KEY_…")` | WS `Click`, validated `^KEY_[A-Z0-9_]+$` |
| `key_hold` | `PressKey` / `ReleaseKey` | WS `Press`/`Release` (§3.3) with guaranteed release |
| `text` | `SendText` (+ `State.TextActive/TextValue` from IME events) | WS `SendInputString`/`SendInputEnd` (§3.4) |
| `app` | `Apps` / `LaunchApp(id)` | REST `POST /api/v2/applications/<id>` (§2, §6) |
| (internal) | `Poll` | REST reachability (§2) + UPnP `GetVolume`/`GetMute` (§3.6) |

- **Persistent WebSocket = command channel + event stream:** the remote-control
  socket is opened once (capturing the token) and **kept open while the TV is
  on** — `Poll` redials it when it drops, but never without a cached token (an
  unpaired dial pops the on-screen Allow prompt, which a background poller must
  not do). A stale socket is detected on the next write and redialed once.
  Writes are serialized; the drain reader answers control frames, refreshes the
  token, and feeds the IME events into device state.
- **Press-and-hold safety:** `PressKey` arms a **watchdog** (`holdMax`, 10 s)
  that auto-releases; any newer press/click first releases the held key; and
  `ReleaseKey` always sends the `Release` frame even if nothing is tracked as
  held (an extra release is harmless — a missed one freezes the remote channel,
  §3.3). The client lifting its finger is never required for correctness.
- **Absolute volume (real):** `SetVolume(pct)` is **one UPnP `SetVolume` call**;
  `Poll` reads `GetVolume`/`GetMute` back every tick, so the slider and mute icon
  show the TV's true state, including changes made with the physical remote.
  `ToggleMute` is `GetMute` → `SetMute(!cur)` — deterministic, unlike the blind
  `KEY_MUTE` toggle. `VolumeUp/Down` remain remote keys so the TV shows its own
  volume OSD, with a UPnP read-back after each step. (The old key-stepped ramp
  with a tracked estimate is gone.)
- **Text input:** `SendText` types into the focused field (§3.4); the IME events
  mirror the field into `State.TextActive`/`State.TextValue` so the UI shows
  live what's typed on the TV. Because `imeEnd` isn't guaranteed, the mirrored
  state is also cleared on focus-leaving keys (`KEY_RETURN`/`KEY_HOME`/
  `KEY_EXIT`/`KEY_POWER`), app launches, power off, and socket loss.
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
  state. While reachable, `Poll` also reads the real volume and mute over UPnP (§3.6)
  and keeps the event socket connected. (Caveat: a TV with network-standby that keeps
  answering REST while "off" will read as on once the grace window passes — REST alone
  can't distinguish standby from on.)

**Known tradeoffs (not bugs):** `On` returns success once the WoL packet is sent
(it can't confirm the TV woke — the next poll reconciles); `SetVolume`/`ToggleMute`
depend on the TV's RenderingControl being enabled (§3.6 caveat) — they fail loudly
rather than falling back to key-stepping, so the shown state never lies.

---

## 9. This unit (reference hardware)

The unit Setu's Samsung support is developed and verified against. Read off the
TV's **Menu → Settings → Support → About This TV** (and the engineering info
overlay). Config entry: `living_tv` (`docs` ↔ `config.yaml`).

| Field | Value | Meaning |
|---|---|---|
| `MN` | **UA50AU7700KLXL** | Model number (the "actual device model") |
| `SN` | `0AK23PAW101353K` | Serial number |
| `PD` | `--/--/----` | Production date (not reported by this set) |
| `FW` | `T-KSU2EUABC-2301.1` | Firmware version |
| `FC` | `SWU-OU_T-KSU2EUABC_2301_251112` | Firmware build / OTA code (`251112` ≈ 2025-11-12) |
| `MI` | `T-KSU2EUABC` | Platform / micom id (the firmware family) |
| `LS` | `ED_INDIA` | Local set / region (India) |
| `DI` | `BDCB2NS2EP7HY` | Device id |
| `MA` | `1C:86:9A:05:E1:4C` | MAC shown on-screen — **see the MAC note below** |
| `SC` | `30601_AC2AD28AE60_HC220IM20JK5357912_AA81AC229AD12.9AE13.0AF0TB0BA6DA117IB6IC233` | Service/config descriptor (capability flags; not used by Setu) |

**Model number `UA50AU7700KLXL` decoded** (approximate):
`U`=UHD · `A`=Asia/India market · `50`=50″ panel · `AU7700`=2021 Crystal UHD
7-series · `K`=variant · `LXL`=India SKU. The driver key in config stays
`model: tizen` (it selects the Go driver, not the marketing model); the marketing
name rides in `series: "AU7700"`.

> ⚠️ **Two MACs.** The on-screen `MA` (`1C:86:9A:05:E1:4C`) is **not** the MAC in
> `config.yaml` (`a0:d7:f3:9e:74:b2`). A Samsung TV has separate Wi-Fi and Ethernet
> interfaces, each with its own MAC; the on-screen value reflects one interface
> (typically the active/Ethernet NIC) while `a0:d7:f3:9e:74:b2` is the one **WoL
> was verified against** (§4, 2026-06-04). Keep the config MAC as-is. If WoL ever
> stops waking the TV, try the other MAC — wake the interface the TV actually
> listens on in network-standby.
