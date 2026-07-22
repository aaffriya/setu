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
const focusTrap = readFileSync(new URL('../src/lib/focus-trap.ts', import.meta.url), 'utf8')
const store = readFileSync(new URL('../src/lib/store.ts', import.meta.url), 'utf8')

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

test('Settings starts on its dialog and exposes an immediate close action', () => {
  assert.match(focusTrap, /node\.focus\(\)/)
  assert.doesNotMatch(focusTrap, /items\(\)\[0\].*\.focus\(\)/)
  assert.match(focusTrap, /document\.activeElement === node/)
  assert.match(focusTrap, /event\.shiftKey \? last : first/)
  assert.match(app, /aria-label="Close settings"/)
})

test('arrange mode has a visible Done action outside Settings', () => {
  assert.match(app, /aria-label="Done arranging devices"/)
  assert.match(app, /onclick=\{\(\) => \(organizing = false\)\}/)
  assert.match(app, /organizing = true\s+showSettings = false/)
})

test('refresh swaps its icon for one spinner while active', () => {
  assert.match(app, /\{#if refreshing\}[\s\S]*?animate-spin[\s\S]*?\{:else\}/)
  assert.match(app, /<circle class="opacity-20" cx="12" cy="12" r="8" \/>/)
  assert.match(app, /<path d="M12 4a8 8 0 0 1 8 8" \/>/)
  assert.doesNotMatch(app, /h-5 w-5 \{refreshing \? 'animate-spin'/)
  assert.match(app, /manualRefreshFeedbackMs = 300/)
  assert.match(app, /Promise\.all\(\[/)
})

test('automation editor preserves typed catalog values and supports nested rules', () => {
  assert.match(automations, /<select bind:value=\{action\.value\}/)
  assert.match(automations, /action:\s*'run_automation'/)
  assert.match(automations, /bind:value=\{action\.automation_id\}/)
  assert.match(automations, /cascadeUnavailableCallers/)
  assert.match(automations, /!enabled\.has\(action\.automation_id\)/)
})

test('late command responses cannot replace a newer command state', () => {
  assert.match(store, /commandGenerations\.get\(id\) === generation/)
  assert.match(store, /const commandQueues = new Map<string, Promise<void>>\(\)/)
  assert.match(store, /previous\.then\(\(\) => sendCommand\(id, action, value\)\)/)
  assert.match(store, /authoritativeVersions\.get\(id\).*=== authoritativeVersion/)
  assert.match(store, /err instanceof ApiError && err\.device\?\.id === id/)
})
