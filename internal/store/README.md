# `internal/store`

Owns `$SETU_STATE_DIR/setu.json`, one versioned JSON document containing device
specs and the raw automation section.

- `Load`: missing file means an empty installation.
- `Update`: locked read-modify-write of the whole document.
- `DefaultPath`: OS-temp fallback plus a flag used for the startup warning.
- Maximum file size: 384 KB; automation input is separately capped at 256 KB.

The store knows section shape, not automation meaning. Every write uses a
mode-`0600` temporary file, sync, close, and rename; failure preserves the
previous file. Decoding rejects unknown fields and trailing data.

When `setu.json` does not yet exist, a valid legacy
`setu-automations.json` beside it is adopted once. Do not create new legacy
files.
