import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const devices = readFileSync(new URL('../src/lib/components/Devices.svelte', import.meta.url), 'utf8')
const api = readFileSync(new URL('../src/lib/api.ts', import.meta.url), 'utf8')

// A stored device the server could not start is in no device list and on no
// card. The device screen is where it is repaired or removed, so it is the one
// place that has to ask for it — otherwise the entry exists only in the logs.
test('the device screen loads the entries the server could not start', () => {
  assert.match(api, /export function listUnusableDevices/)
  assert.match(api, /\/api\/devices\/unusable/)
  assert.match(devices, /listUnusableDevices/, 'the device screen never asks for them')

  const openDialog = devices.slice(devices.indexOf('function openDialog()'))
  assert.match(openDialog.slice(0, 300), /loadUnusable\(\)/, 'opening the screen does not load them')
})

test('each unusable entry shows its reason and can be repaired or removed', () => {
  const start = devices.indexOf('{#if unusable.length > 0}')
  assert.notEqual(start, -1, 'there is no section for unusable devices')
  const section = devices.slice(start, devices.indexOf('<!-- Scan -->', start))

  assert.match(section, /\{entry\.reason\}/, 'the reason is never shown, so the fix is a guess')
  assert.match(section, /repair\(entry, 'name'/, 'the name cannot be edited back into a valid one')
  assert.match(section, /discard\(entry\)/, 'the entry cannot be removed')
  // Deleting is the administrator's, exactly as in the list above.
  assert.match(section, /\{#if canRemove\}/, 'Remove is offered to accounts the server would refuse')
})

// Only the labels are editable. An entry whose driver or MAC is the problem
// cannot be fixed by any edit this screen can make, so offering the field is a
// promise the server refuses on every attempt.
test('the label fields appear only when editing them would actually fix the entry', () => {
  const start = devices.indexOf('{#if unusable.length > 0}')
  const section = devices.slice(start, devices.indexOf('<!-- Scan -->', start))

  const guard = section.indexOf('{#if entry.repairable}')
  assert.notEqual(guard, -1, 'the inputs are offered regardless of whether a rename can help')
  assert.ok(
    guard < section.indexOf("repair(entry, 'name'"),
    'the name field is rendered outside the repairable guard',
  )
  assert.match(section, /\{:else\}[\s\S]{0,200}\{entry\.name\}/, 'an unrepairable entry shows no name at all')
  // The copy must not promise a rename for entries that cannot take one.
  assert.doesNotMatch(section, /Fix the name below/)
})

// Repairing brings the device online on the same request, so it moves from this
// list into the real one — which only a refetch can show, since a device that
// was never in the store cannot be updated into it.
test('a repaired device is refetched into the ordinary device list', () => {
  const start = devices.indexOf('async function repair(')
  assert.notEqual(start, -1, 'repair is missing')
  const body = devices.slice(start, devices.indexOf('\n  }', start))

  assert.match(body, /renameDevice\(entry\.id/)
  assert.match(body, /refresh\(\)/, 'the repaired device never reaches the device list')
  assert.match(body, /loadUnusable\(\)/, 'the repaired device stays listed as broken')
})

// Both lists read one confirmRemove, and a repaired entry crosses between them
// keeping its id. Leaving the pending confirmation armed would render the new
// row's Remove already confirmed: one click, and the device the user just
// repaired is gone with the second tap spent on the other list.
test('repairing an entry disarms a Remove confirmation it left behind', () => {
  const start = devices.indexOf('async function repair(')
  assert.notEqual(start, -1, 'repair is missing')
  const body = devices.slice(start, devices.indexOf('\n  }', start))

  const disarm = body.indexOf('confirmRemove')
  assert.notEqual(disarm, -1, 'a pending confirmation survives the move between lists')
  assert.match(body, /confirmRemove = ''/, 'the pending confirmation is never cleared')
  assert.ok(
    disarm > body.indexOf('renameDevice('),
    'the confirmation is cleared before the edit is known to have succeeded',
  )
})
