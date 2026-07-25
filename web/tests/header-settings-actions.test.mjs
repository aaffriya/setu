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
const appCSS = readFileSync(new URL('../src/app.css', import.meta.url), 'utf8')

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
  assert.match(app, /function focusSettingsOnMount\(node: HTMLElement\)/)
  assert.match(app, /if \(node\.isConnected\) node\.focus\(\{ preventScroll: true \}\)/)
  assert.match(app, /use:focusSettingsOnMount/)
  assert.doesNotMatch(focusTrap, /items\(\)\[0\].*\.focus\(\)/)
  assert.match(focusTrap, /document\.activeElement === node/)
  assert.match(focusTrap, /event\.shiftKey \? last : first/)
  assert.match(app, /aria-label="Close settings"/)
  assert.match(app, /id="token-input"[\s\S]*?autocomplete="off"/)
})

test('shared dialog focus preserves intentional field autofocus', () => {
  assert.match(focusTrap, /active && !node\.contains\(document\.activeElement\)/)
  assert.doesNotMatch(focusTrap, /focusDialog\(true\)|\bforce\b/)
  assert.match(
    focusTrap,
    /node\.contains\(document\.activeElement\) \|\| document\.activeElement === document\.body/,
  )
  assert.match(focusTrap, /if \(stillOwnsFocus && previous\?\.isConnected\) previous\.focus\(\)/)
  assert.match(scenes, /function focusOnMount\(node: HTMLInputElement\)[\s\S]*?node\.focus\(\)/)
  assert.match(scenes, /aria-label="New scene"[\s\S]*?use:focusOnMount/)
})

test('arrange mode has a visible Done action outside Settings', () => {
  assert.match(app, /aria-label="Done arranging devices"/)
  assert.match(app, /onclick=\{\(\) => \(organizing = false\)\}/)
  assert.match(app, /organizing = true\s+showSettings = false/)
})

test('refresh keeps the same symbol and rotates it while active', () => {
  assert.match(app, /\{#if refreshing\}[\s\S]*?animate-spin[\s\S]*?\{:else\}/)
  assert.equal(app.split('<path d="M20 11a8 8 0 10-2.3 5.7" />').length - 1, 2)
  assert.equal(app.split('<path d="M20 4v7h-7" />').length - 1, 2)
  assert.doesNotMatch(app, /<circle class="opacity-20"/)
  assert.match(app, /manualRefreshFeedbackMs = 300/)
  assert.match(app, /Promise\.all\(\[/)
})

test('device cards use fixed portrait widths through the smallest viewport', () => {
  assert.match(app, /repeat\(auto-fit,288px\)/)
  assert.match(app, /min-\[352px\]:\[grid-template-columns:repeat\(auto-fit,320px\)\]/)
  assert.doesNotMatch(app, /grid-template-columns:repeat\(auto-fit,minmax\(min\(320px,100%\),320px\)\)/)
})

test('Settings locks background scroll and modal scrollers contain gestures', () => {
  assert.match(app, /onpointerdown=\{rememberSettingsScroll\}/)
  assert.match(app, /if \(event\.detail === 0\) rememberSettingsScroll\(\)/)
  assert.match(app, /document\.documentElement\.classList\.add\('setu-scroll-locked'\)/)
  assert.match(app, /window\.scrollTo\(0, scrollY\)/)
  assert.match(app, /overflow-y-auto overscroll-contain/)
  assert.match(appCSS, /html\.setu-scroll-locked body/)
  assert.match(appCSS, /color-mix\(in srgb, rgb\(var\(--page\)\) 50%, black 50%\)/)
  assert.match(appCSS, /position: fixed/)
})

test('automation mobile layout and form focus stay inside their bounds', () => {
  assert.match(automations, /max-w-lg flex-col overflow-hidden/)
  assert.match(automations, /overflow-x-hidden overflow-y-auto overscroll-contain/)
  assert.match(automations, /overflow-hidden rounded-lg[\s\S]*?whitespace-nowrap/)
  assert.match(appCSS, /:focus-visible/)
  assert.match(appCSS, /:not\(\[type='search'\]\)/)
  assert.match(appCSS, /box-shadow: inset 0 0 0 2px/)
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
