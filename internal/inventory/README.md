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
Relabeling rebuilds and swaps the device while preserving position and cached
state. Restore validates and builds the complete list before mutating runtime or
disk.

Removing a device does not rewrite automations. Missing references fail at run
time and are disabled on the next automation-engine startup.
