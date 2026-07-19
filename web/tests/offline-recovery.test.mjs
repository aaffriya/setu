import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const app = readFileSync(new URL('../src/App.svelte', import.meta.url), 'utf8')

test('no-device offline screen opens the server recovery page', () => {
  const branchStart = app.indexOf(
    "{:else if $devices.length === 0 && ($connection === 'offline' || $connection === 'connecting')}",
  )
  const branchEnd = app.indexOf('{:else if $devices.length === 0}', branchStart + 1)

  assert.notEqual(branchStart, -1, 'no-device offline branch is missing')
  assert.notEqual(branchEnd, -1, 'no-device offline branch has no closing branch')

  const offlineScreen = app.slice(branchStart, branchEnd)
  assert.match(offlineScreen, /<a\s+href="\/api\/recover"/)
  assert.match(offlineScreen, />\s*Fix app cache\s*<\/a>/)
  assert.doesNotMatch(offlineScreen, /localStorage/)
})
