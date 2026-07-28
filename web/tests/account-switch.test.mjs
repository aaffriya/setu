import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const app = readFileSync(new URL('../src/App.svelte', import.meta.url), 'utf8')
const store = readFileSync(new URL('../src/lib/store.ts', import.meta.url), 'utf8')

// The device cache is per-browser, not per-account. Saving a different token
// has to empty it in the same breath, or the new account paints from the
// previous one's list until its own refresh lands — indefinitely when that
// refresh cannot reach the server.
test('saving a token forgets the previous account state before refreshing', () => {
  const start = app.indexOf('function saveToken()')
  assert.notEqual(start, -1, 'saveToken is missing')
  const body = app.slice(start, app.indexOf('\n  }', start))

  assert.match(body, /forgetAccountState\(\)/, 'saveToken does not clear the previous account')
  assert.ok(
    body.indexOf('forgetAccountState()') < body.indexOf('refreshDevices()'),
    'the cache must be cleared before the first refresh for the new token',
  )
  assert.match(app, /forgetAccountState,/, 'forgetAccountState is not imported')
})

test('forgetting account state clears the cached list, its mirror, and the session', () => {
  const start = store.indexOf('export function forgetAccountState()')
  assert.notEqual(start, -1, 'forgetAccountState is missing')
  const body = store.slice(start, store.indexOf('\n}', start))

  assert.match(body, /devices\.set\(\[\]\)/, 'the in-memory device list is not cleared')
  assert.match(body, /session\.set\(null\)/, 'the previous account session is not cleared')
  assert.match(body, /localStorage\.removeItem\(CACHE_KEY\)/, 'the mirrored cache is not removed')
  // A queued coalesced write would otherwise restore the list moments later.
  assert.match(body, /pendingCache = null/, 'a pending cache write can undo the removal')
})
