# `internal/devices/samsung`

Samsung Tizen TV driver. Full wire reference:
[`docs/devices/samsung.md`](../../../docs/devices/samsung.md).

- `samsung.go`: REST power/apps, persistent TLS WebSocket remote/events, UPnP
  volume/mute, token persistence, and Wake-on-LAN.
- `discovery.go`: SSDP DIAL candidates verified by `/api/v2/` `wifiMac`; also
  supplies scan labels.

Capabilities are power, real absolute volume/mute, remote keys and safe hold,
app launch, and text input. The TV socket stays open while on. Background polls
redial only with a cached token, and every held key is released explicitly,
before a newer key, on driver close, or by watchdog. Close then prevents
reconnects.

Resolution is cached IP → ARP → MAC-verified SSDP. `On` bypasses resolution and
sends WoL. `Off` checks live power before using toggle key `KEY_POWER`.
`Poll` keeps `Online=true` for MAC-based wake control but returns
`ErrPollNoResponse` when live REST contact fails. Consequently this driver does
not opt into reachability automations: its `Online` means control availability,
not a successful live response.

Pairing tokens are mode-`0600`
`$SETU_STATE_DIR/setu-samsung-<id>.token` files. Set a persistent state
directory. The driver has live verification on an AU7700-series TV; UPnP and
WoL availability can still vary by firmware/settings.
