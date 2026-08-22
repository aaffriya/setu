import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const store = readFileSync(new URL('../src/lib/store.ts', import.meta.url), 'utf8')
const handlers = readFileSync(
  new URL('../../internal/api/handlers.go', import.meta.url),
  'utf8',
)

const refresh = store.slice(
  store.indexOf('export async function refresh('),
  store.indexOf('// --- managing which devices exist'),
)
const replace = store.slice(
  store.indexOf('function replaceAuthoritativeDevices('),
  store.indexOf('export async function refresh('),
)

// A hardware refresh reads every device and can take seconds; a tap during that
// window is answered long before it. Both halves of the round trip have to hold
// for the card not to flip back: the server must not reply with the poll's own
// readings, and the browser must not install a list that predates its state.
test('a device list request records what it already knew before awaiting', () => {
  assert.match(refresh, /const before = new Map\(authoritativeVersions\)/)
  const snapshot = refresh.indexOf('const before = new Map(authoritativeVersions)')
  const request = refresh.indexOf('await listDevices(')
  assert.ok(snapshot >= 0 && request > snapshot, 'versions must be read before the request')
  assert.match(refresh, /replaceAuthoritativeDevices\(next, before\)/)
})

test('a device whose state advanced during the request keeps it', () => {
  assert.match(
    replace,
    /\(authoritativeVersions\.get\(device\.id\) \?\? 0\) !== \(before\.get\(device\.id\) \?\? 0\)/,
  )
  // Metadata still comes from the list — only the state is held back.
  assert.match(replace, /newer && current \? \{ \.\.\.device, state: current\.state \} : device/)
})

test('a hardware refresh answers from the read model, not the poll result', () => {
  const list = handlers.slice(
    handlers.indexOf('func (s *Server) handleListDevices('),
    handlers.indexOf('// granted keeps only the devices'),
  )
  assert.match(list, /if err := s\.poller\.Refresh\(r\.Context\(\)\); err != nil/)
  assert.match(list, /writeJSON\(w, http\.StatusOK, granted\(principal, s\.mgr\.Snapshot\(\)\)\)/)
  assert.doesNotMatch(list, /views\[i\]\.State = state/)
})
