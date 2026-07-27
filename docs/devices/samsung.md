# Samsung Tizen local protocols

Source for `internal/devices/samsung`. Samsung control is split across four
transports.

## 1. Transport map

| Job | Transport | Port |
| --- | --- | --- |
| info, power state, app launch | HTTP REST | `8001` |
| remote keys, text, events, pairing | WebSocket over TLS | `8002` |
| absolute volume and mute | UPnP RenderingControl | `9197` |
| power on | Wake-on-LAN | UDP `9` and `7` broadcasts |

Token-auth TVs require the TLS WebSocket on `8002`; the `8001` key socket may
connect while silently ignoring keys.

## 2. REST and discovery

Device info:

```sh
curl http://<tv-ip>:8001/api/v2/
```

Useful fields:

- `device.wifiMac`: identity verification;
- `device.PowerState`: `"on"` or `"standby"`;
- `device.name` and `device.modelName`: scan labels.

Discovery sends SSDP M-SEARCH for the DIAL receiver target. An SSDP source
address is only a candidate: Setu accepts it after `/api/v2/` reports the
configured Wi-Fi MAC.

Apps launch with `POST /api/v2/applications/<id>`. Catalog IDs vary by firmware,
so Setu falls back to `ed.installedApp.get`, matches the display name, and
caches the working ID in memory.

## 3. WebSocket remote

Endpoint:

```text
wss://<tv-ip>:8002/api/v2/channels/samsung.remote.control?name=<base64>&token=<token>
```

Omit `token` on first pairing. After the user accepts the TV's **Allow** prompt,
the `ms.channel.connect` event returns a token. Setu stores it at
`$SETU_STATE_DIR/setu-samsung-<id>.token`.

Normal key click:

```json
{"method":"ms.remote.control","params":{"Cmd":"Click","DataOfCmd":"KEY_HOME","Option":"false","TypeOfRemote":"SendRemoteKey"}}
```

`Cmd` can be `Click`, `Press`, or `Release`.

**Every `Press` must receive a matching `Release`.** A stuck key can freeze the
TV remote channel across reconnects. The driver guarantees release on an
explicit call, before a newer key, or after the one-minute watchdog.

Keys must match `KEY_[A-Z0-9_]+`; `KEY_FACTORY` is rejected.

## 4. Text and event socket

Text input sends base64 text using `SendInputString`, followed by
`SendInputEnd`. The same long-lived WebSocket carries:

- `ms.remote.imeStart`;
- `ms.remote.imeUpdate` with the field's full base64 value;
- `ms.remote.imeEnd`;
- token refresh and control frames.

`imeEnd` is not reliable when focus moves away. Back, home, exit, power, app
launch, socket loss, and power-off clear Setu's mirrored text state.

The driver keeps one socket open while the TV is on. Background polling redials
only when a token is already cached, so it never creates an unexpected pairing
prompt.

## 5. Volume and mute

UPnP control URL:

```text
http://<tv-ip>:9197/upnp/control/RenderingControl1
```

Setu uses `GetVolume`, `SetVolume`, `GetMute`, and `SetMute` on channel
`Master`. Polling reads real values, including changes from the physical remote.
Volume up/down remain WebSocket keys for the TV OSD, followed by UPnP readback.

RenderingControl is firmware-dependent. Failures are returned instead of
falling back to an estimated key-stepped value.

## 6. Power and online semantics

- `On`: send a Wake-on-LAN magic packet to limited and interface-directed
  broadcasts on ports 9 and 7.
- `Off`: read `PowerState` first, then send `KEY_POWER` only when the TV is
  really on. This prevents a stale UI from toggling a standby TV back on.
- `Poll`: read `PowerState`; while on, also read volume/mute and maintain the
  event socket.

The TV remains `Online=true` even without a live REST response because power-on
is still available by MAC. Poll returns `ErrPollNoResponse` so diagnostics
separately show failed live contact.

## 7. Resolution and caveats

Resolution is cached IP → ARP → MAC-verified SSDP. Transport errors invalidate
the cache. Wake-on-LAN never requires an IP.

- The TV and Setu generally need the same L2 segment.
- Port `8002` uses a self-signed certificate; the driver skips certificate
  verification only for the MAC-resolved LAN target.
- WoL over Wi-Fi requires Samsung network standby/“Power On with Mobile”.
- A sent WoL packet cannot confirm that the TV woke; the next poll reconciles.

The driver has been verified live on a Samsung AU7700-series Tizen TV.
