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
| Prime Video | `3201512006785` | `org.tizen.ignition` |
| Spotify | `3201606009684` | — |
| Browser | — | `org.tizen.browser` |

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
| `volume` | `VolumeUp/Down/ToggleMute` | WS `KEY_VOLUP/VOLDOWN/MUTE` |
| `key` | `SendKey("KEY_…")` | WS, validated `^KEY_[A-Z0-9_]+$` |
| `app` | `Apps` / `LaunchApp(id)` | REST `POST /api/v2/applications/<id>` (§2, §6) |
| (internal) | `Poll` | REST `/api/v2/` reachability (§2) |

- One WebSocket per command (connect → capture token → send → flush → close).
- **Token cache:** `$SETU_STATE_DIR/setu-samsung-<id>.token` (defaults to OS temp;
  set `SETU_STATE_DIR` to a persistent dir to survive reboots), mode `0600`.
- **App shortcuts** (`LaunchApp`): a fixed catalog (YouTube / Netflix / Prime Video)
  launched over DIAL REST; `LaunchApp` only accepts an id from that catalog. The UI
  shows the buttons from `Apps()`. Source/input selection (e.g. HDMI) is *not* an app
  — it's the `KEY_SOURCE` / `KEY_HDMI` remote key (`key` capability).
- **`Poll` / online vs. on:** for a TV, REST reachability is a *power* proxy, not a
  presence proxy. An off TV stops answering REST but can still be woken by WoL, so it
  is reported **online whenever its address resolves** (off ≠ offline — otherwise the
  UI would hide the power control needed to wake it). Reachability only forces `On`
  off; the on state otherwise follows the last command. `Poll` can't read volume. (A
  TV in network-standby that keeps answering REST is left in its last commanded on
  state rather than flipped back on.)

**Known tradeoffs (not bugs):** a fresh WS per key press adds latency for rapid
volume taps (a persistent connection could be added later); `On` returns success
once the WoL packet is sent (it can't confirm the TV woke — the next poll
reconciles).
