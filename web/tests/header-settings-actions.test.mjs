import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const app = readFileSync(new URL('../src/App.svelte', import.meta.url), 'utf8')
const scenes = readFileSync(
  new URL('../src/lib/components/Scenes.svelte', import.meta.url),
  'utf8',
)
const automations = readFileSync(
  new URL('../src/lib/components/Automations.svelte', import.meta.url),
  'utf8',
)

test('header keeps refresh and search while device tools live in Settings', () => {
  const [beforeSettings, settings] = app.split('{#if showSettings}')
  assert.match(beforeSettings, /onclick=\{manualRefresh\}/)
  assert.match(beforeSettings, /onclick=\{\(\) => \(searching = true\)\}/)
  assert.doesNotMatch(beforeSettings, /<Scenes\b/)
  assert.doesNotMatch(beforeSettings, /<Automations\b/)
  assert.doesNotMatch(beforeSettings, /Arrange &amp; group devices/)

  assert.match(settings, />Device tools</)
  assert.match(settings, /<Scenes\b/)
  assert.match(settings, /<Automations\b/)
  assert.match(settings, /Arrange devices/)
})

test('moved tools use full-width Settings rows', () => {
  assert.match(scenes, /flex w-full items-center gap-3/)
  assert.match(automations, /flex w-full items-center gap-3/)
})

test('child dialogs own Escape and keep Settings open', () => {
  assert.match(app, /showSettings\s*&&\s*!activeSettingsTool/)
  assert.match(scenes, /stopPropagation\(\)/)
  assert.match(automations, /stopPropagation\(\)/)
  assert.match(app, /use:trapFocus/)
  assert.match(scenes, /use:trapFocus/)
  assert.match(automations, /use:trapFocus/)
})

test('automation delay keeps its seconds suffix clear of the number input', () => {
  assert.match(automations, /flex items-center gap-2 text-\[11px\]/)
  assert.match(automations, /class="w-16 shrink-0[^\"]*"/)
  assert.match(automations, /<span class="shrink-0">s<\/span>/)
})
