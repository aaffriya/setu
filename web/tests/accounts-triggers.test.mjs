import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const app = readFileSync(new URL('../src/App.svelte', import.meta.url), 'utf8')
const users = readFileSync(new URL('../src/lib/components/Users.svelte', import.meta.url), 'utf8')
const automations = readFileSync(
  new URL('../src/lib/components/Automations.svelte', import.meta.url),
  'utf8',
)
const api = readFileSync(new URL('../src/lib/api.ts', import.meta.url), 'utf8')
const store = readFileSync(new URL('../src/lib/store.ts', import.meta.url), 'utf8')
const backup = readFileSync(new URL('../src/lib/backup.ts', import.meta.url), 'utf8')

// The session decides what the app bothers to show. Every restriction is
// enforced again server-side, so the failure mode of a wrong answer here is a
// 403 — never silent, unauthorised access.
test('permissions come from the server and default to showing everything', () => {
  assert.match(api, /\/api\/session/)
  assert.match(store, /export const session/)
  assert.match(store, /export async function loadSession/)
  // An older or unreachable Setu has no answer; that must not lock the person
  // out of their own installation.
  assert.match(store, /session\.set\(null\)/)
  assert.match(app, /\$session === null \|\| \$session\.admin/)
})

test('the session is reloaded when the token or the tab changes', () => {
  assert.match(app, /void loadSession\(\)/)
  const saveToken = app.slice(app.indexOf('function saveToken'))
  assert.match(saveToken.slice(0, 400), /loadSession\(\)/)
  assert.match(store, /export function resume[\s\S]{0,400}loadSession\(\)/)
})

// A "control" account operates the devices it was given and nothing else, so
// the screens that change the installation are not offered to it.
test('Settings hides what the account cannot do', () => {
  const settings = app.slice(app.indexOf('{#if showSettings}'))
  assert.match(settings, /\{#if canModify\}[\s\S]{0,400}<Devices\b/)
  assert.match(settings, /\{#if canManageUsers\}[\s\S]{0,400}<Users\b/)
  assert.match(settings, /\{#if isAdmin\}[\s\S]{0,300}<BackupRestore \/>/)
  assert.match(settings, /<Automations[\s\S]{0,200}\{canModify\}/)
})

test('automations stay viewable and runnable without the modify permission', () => {
  // The Run button is deliberately not gated: running a rule reaches only the
  // devices this account already controls.
  assert.match(automations, /onclick=\{\(\) => run\(rule\)\}/)
  const runButton = automations.slice(automations.indexOf('onclick={() => run(rule)}'))
  assert.doesNotMatch(runButton.slice(0, 200), /!canModify/)
  // Editing, deleting and rotating a webhook token are.
  assert.match(automations, /\{#if canModify\}[\s\S]{0,400}New automation/)
  assert.match(automations, /rule\.trigger\.type === 'webhook' && canModify/)
})

// Pausing is installation-wide: it would stop rules a restricted account was
// never shown, so the server keeps the administrator's value and the switch is
// only offered to them.
test('the installation-wide pause belongs to the administrator', () => {
  assert.match(automations, /\{#if isAdmin\}[\s\S]{0,400}togglePause/)
  assert.match(automations, /Paused by the administrator/)
  assert.match(app, /<Automations[\s\S]{0,200}\{isAdmin\}/)
})

// Offering "Add a device" to an account the server would refuse is worse than
// saying plainly that nothing has been shared yet.
test('the empty screen matches what the account may actually do', () => {
  assert.match(app, /Nothing shared with you yet/)
  const empty = app.slice(app.indexOf('Nothing shared with you yet'))
  assert.doesNotMatch(empty.slice(0, 400), /Add a device/)
})

// A token exists once, in the response that created it. The screen has to put
// it in front of the person before the editor closes.
test('a new account shows its token exactly once', () => {
  assert.match(users, /it will not be shown again/)
  assert.match(users, /if \(result\.token\) issued =/)
  assert.match(users, /rotateUserToken/)
  assert.match(users, /function closePeople\(\)[\s\S]*?if \(issued\) return/)
  assert.match(users, /if \(event\.key !== 'Escape'\) return[\s\S]*?if \(issued\) return/)
  assert.match(users, /disabled=\{Boolean\(issued\)\}/)
  assert.match(users, />I’ve saved it</)
  // Only a name is collected: there is no password to choose.
  assert.doesNotMatch(users, /type="password"/)
})

test('device access is explicit and says so', () => {
  assert.match(users, /Nothing is shared by default/)
  assert.match(users, /draft\.devices\.includes\(device\.id\)/)
  assert.match(api, /export function createUser/)
  assert.match(api, /export function updateUser/)
  assert.match(api, /export async function deleteUser/)
})

// The reachability and relation triggers each expose the field that cannot be
// defaulted, while the six tabs remain usable in the narrow settings drawer.
test('the editor offers metric, offline, online and presence triggers', () => {
  for (const kind of ['device_state', 'device_offline', 'device_online', 'presence']) {
    assert.match(automations, new RegExp(`value: '${kind}'`))
  }
  assert.match(automations, /function setMetric/)
  assert.match(automations, /Runs the moment the value passes this point/)
  assert.match(automations, /arms again only once it is seen back online/)
  assert.match(automations, /comparing how long it was gone against the number below/)
  assert.match(automations, /grid-cols-3[\s\S]{0,100}sm:grid-cols-6/)
  assert.match(api, /type: 'device_online'/)
  assert.match(api, /operator: AutomationComparison/)
  assert.match(api, /reports_reachability: item\.reports_reachability === true/)
  assert.match(automations, /device\.reports_reachability/)
  assert.match(automations, /function isMAC/)
})

// Presence needs a host that can read its neighbour table. Where it cannot, the
// server refuses the rule — so the tab is disabled instead of offered.
test('presence is disabled where the host cannot answer it', () => {
  assert.match(automations, /snapshot\?\.presence !== false/)
  assert.match(automations, /kind\.value === 'presence' && !hasPresence/)
  assert.match(api, /presence\?: boolean/)
})

// A restore that carries a rule this installation cannot arm would be refused
// as a whole, taking every other rule with it.
test('restore disables rules the target installation cannot run', () => {
  assert.match(backup, /METRIC_CAPABILITY/)
  assert.match(backup, /rule\.trigger\.type === 'device_offline'/)
  assert.match(backup, /rule\.trigger\.type === 'device_online'/)
  assert.match(backup, /source\?\.reports_reachability/)
  assert.match(backup, /rule\.trigger\.type === 'presence' && !presence/)
})

test('a draft is only savable once its trigger is complete', () => {
  assert.match(automations, /function draftIsComplete/)
  assert.match(automations, /disabled=\{saving \|\| !draftIsComplete\(draft\)\}/)
})

test('the editor exposes bounded action guards and non-blocking timed steps', () => {
  assert.match(api, /when\?: AutomationCondition\[\]/)
  assert.match(api, /offset_minutes\?: number/)
  assert.match(api, /skipped\?: boolean/)
  assert.match(automations, /Only if \(checked before action\)/)
  assert.match(automations, /Run after start/)
  assert.match(automations, /max="1439"/)
  assert.match(automations, /Run first step/)
  assert.match(automations, /result\.skipped/)
})
