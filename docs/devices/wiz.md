# Philips WiZ local protocol

Source for `internal/devices/wiz`.

## 1. Transport

| Item | Value |
| --- | --- |
| Transport | UDP, one JSON object per datagram |
| Port | `38899` |
| Authentication | none |
| Identity | `result.mac` as 12 hex digits |
| State read | `getPilot` |
| State write | `setPilot` |
| Scan metadata | `getSystemConfig` |

## 2. Wire operations

Read:

```json
{"method":"getPilot","params":{}}
```

Write:

```json
{"method":"setPilot","params":{"state":true,"dimming":60}}
```

Relevant `setPilot` parameters:

| Parameter | Values | Meaning |
| --- | --- | --- |
| `state` | boolean | power |
| `dimming` | `10`–`100` | brightness; hardware ignores lower values |
| `r`, `g`, `b` | `0`–`255` | RGB mode |
| `temp` | model range | white temperature in Kelvin |
| `sceneId` | `1`–`32` | built-in scene |
| `speed` | `10`–`200` | dynamic scene speed |

RGB, direct white temperature, and scene are mutually exclusive modes. A scene
reply can still include the temperature used to render that scene; a non-zero
`sceneId` is the active-mode marker and takes precedence over that constituent
temperature. Replies otherwise vary by active mode and an off bulb may omit
colour or brightness fields.

A standalone `dimming` write exits a built-in scene on the verified bulbs. To
keep a selected scene active, do not follow its `sceneId` write with a separate
brightness command.

## 3. Discovery and model selection

`Lookup` broadcasts `getPilot`, matches the requested normalized MAC, and uses
the matching datagram's source address.

`Scan` broadcasts `getSystemConfig`. `moduleName` selects the driver:

- contains `RGB` → `color_bulb`;
- contains `TW` → `tunable_white`;
- anything else → discovered with an empty model.

Probes are repeated inside the reply window because UDP replies are lossy.
Never select a model from a product name or an unverified source IP.

## 4. Setu mapping

| Capability | Wire operation |
| --- | --- |
| power | `setPilot state` |
| brightness | `setPilot state=true,dimming` |
| RGB | `setPilot state=true,r,g,b` |
| colour temperature | `setPilot state=true,temp` |
| scene | `setPilot state=true,sceneId` |
| scene speed | `setPilot speed` |
| poll | `getPilot` |

`color_bulb` supports 2200–6500 K, RGB, and all scenes.
`tunable_white` supports 2700–6500 K and white scenes 9–16; it intentionally
does not implement RGB. Scene names and dynamic flags live in `scenes.go`.

Resolution is cached IP → ARP → WiZ discovery. Any UDP failure clears the
cached IP.

## 5. Diagnostic probe

```sh
printf '%s' '{"method":"getPilot","params":{}}' |
  nc -u -w1 <bulb-ip> 38899
```

Raw probes require a current IP; stored Setu device specs do not.

## 6. Important failures

- Missing `setPilot` reply is a command failure, even if the bulb may have
  applied the datagram.
- White-only hardware can silently ignore RGB; expose capabilities from the
  model, not from a generic WiZ assumption.
- WiZ is unauthenticated LAN control. Keep the IoT segment trusted.

Both current Setu WiZ models have been verified against physical hardware.
