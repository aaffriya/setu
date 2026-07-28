# `internal/store`

Owns `$SETU_STATE_DIR/setu.json`, one versioned JSON document containing device
specs plus the raw automation and users sections.

- `Load`: missing file means an empty installation.
- `Update`: locked read-modify-write of the whole document.
- `DefaultPath`: OS-temp fallback plus a flag used for the startup warning.
- Maximum file size: 384 KB; automation input is separately capped at 256 KB.

A version-1 file — where a device's `model` meant the driver key and `series`
meant the hardware — is upgraded on read and rewritten in the current shape by
the next write.

The store knows section shape, not what the sections mean: `automations` belongs
to `internal/automation` and `users` to `internal/users`, and both ride as opaque
JSON so a write to one can never disturb the other. Every write uses a
mode-`0600` temporary file, sync, close, rename, and parent-directory sync.
Decoding rejects unsupported versions, unknown fields, and trailing data.

When `setu.json` does not yet exist, a valid legacy
`setu-automations.json` beside it is adopted once. Do not create new legacy
files.
