# `internal/users`

The people who may use this installation besides its administrator.

## Who the administrator is

Nobody in here. The administrator is whoever presents `SETU_TOKEN`, which comes
from the environment — so no API call, no restore, and no damaged state file can
lock the owner out of their own house. This package holds only the accounts
added from the app.

## Model and limits

- A person has a name, a role, and an explicit list of device ids.
- Roles are `read` (control the devices you were given) and `modify` (also add
  devices and write automations — still only for those devices). A role is
  global; device access is the separate list.
- Up to 32 users, a 1–32 character name, and at most 64 device grants each.
- Nothing is shared by default. A device added later has to be granted, with one
  exception: whoever adds a device is granted it, since they clearly meant to
  use it.
- Grants follow their device out of the installation. `ForgetDevice` covers a
  single delete and `RetainDevices` covers a restore, which removes devices
  without deleting them one at a time. Both matter because device ids are
  derived from the brand and MAC, so the same id returns when the same hardware
  is added again — and it must not arrive pre-shared.

## Tokens

There is no password. Signing in is a Setu-generated token, and only its
SHA-256 is stored — the plaintext exists once, in the response that created or
rotated it, exactly like an automation webhook token. A lost token is replaced,
never recovered.

`Authenticate` hashes the candidate once and compares it against every stored
hash in constant time without stopping at the first match, so neither timing nor
the number of comparisons reveals which account — or whether any — a token
belongs to. That, plus the 32-user cap, is what keeps the loop honest and cheap.

## Persistence

Users occupy the `users` section of `$SETU_STATE_DIR/setu.json` through
`internal/store`, beside devices and automations. Every entry is re-validated on
load: the file is editable, and a hand-written entry would otherwise be handed
an authentication path.

Enforcement is not here. This package answers "who is this and what were they
given"; `internal/api` decides what that means for each route.

Do not add passwords, sessions, cookies, password reset, or an identity
provider. One token per person is the whole model.
