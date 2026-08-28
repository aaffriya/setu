# `internal/inventory`

Owns the boundary between stored `DeviceSpec` entries, factory-built devices,
and manager membership.

Operations:

- load/build at startup;
- add, relabel, remove;
- export and all-or-nothing replace;
- report registered types;
- mark scan candidates already configured.

Rules:

- maximum 64 devices;
- spec must validate and its brand/driver must be registered;
- omitted IDs derive from brand plus the last three MAC bytes;
- a brand may use a MAC once, while another brand may intentionally share it
  (for example a TV plus a generic WoL card).

A bad stored entry is logged and skipped at startup but remains exportable.
`Unusable` reports those entries with the reason each was refused, recorded
where the failure happened rather than re-derived, so the app can show a device
that reached no other list — it is absent from the manager, so it appears on no
card and in no picker. Relabeling a live device updates manager metadata without
rebuilding its protocol driver; relabeling an unusable entry builds it and clears
the entry once it is running. Removal clears it too. Restore validates and builds
the complete list before mutating runtime or disk.

Removing a device does not rewrite automations. Missing references fail at run
time and are disabled on the next automation-engine startup.

Removal and restore prune stored account grants in the same `internal/store`
update that writes the new device list. The two sections either both commit or
neither does, so a MAC-derived id cannot return with access from an incomplete
earlier delete.
