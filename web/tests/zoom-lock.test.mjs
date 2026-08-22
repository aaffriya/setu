import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const html = readFileSync(new URL('../index.html', import.meta.url), 'utf8')
const css = readFileSync(new URL('../src/app.css', import.meta.url), 'utf8')
const main = readFileSync(new URL('../src/main.ts', import.meta.url), 'utf8')

// Setu is an app surface, so it stays at one scale on every OS: no pinch, no
// double-tap, and no jump when the keyboard opens over a text field.
test('the viewport is fixed at scale 1', () => {
  const viewport = html.slice(html.indexOf('name="viewport"'), html.indexOf('theme-color'))
  assert.match(viewport, /initial-scale=1/)
  assert.match(viewport, /maximum-scale=1/)
  assert.match(viewport, /user-scalable=no/)
  // The notch/safe-area opt-in must survive alongside the scale lock.
  assert.match(viewport, /viewport-fit=cover/)
})

test('touch gestures may only scroll', () => {
  const root = css.slice(css.indexOf('  html {'), css.indexOf('html.setu-scroll-locked'))
  assert.match(root, /touch-action: pan-x pan-y/)
  // `manipulation` still permits zooming, which is what this replaced.
  assert.doesNotMatch(root, /touch-action: manipulation/)
})

test('a desktop trackpad pinch is refused too', () => {
  assert.match(main, /window\.addEventListener\(\s*'wheel'/)
  assert.match(main, /if \(event\.ctrlKey \|\| event\.metaKey\) event\.preventDefault\(\)/)
})

test('iOS Safari pinch gestures are refused before mount', () => {
  assert.match(main, /'gesturestart', 'gesturechange', 'gestureend'/)
  assert.match(main, /document\.addEventListener\(gesture, \(event\) => event\.preventDefault\(\)/)
  assert.match(main, /\{ passive: false \}/)
  assert.ok(
    main.indexOf('gesturestart') < main.indexOf("import('./app.css')"),
    'the gesture lock must be installed before the app mounts',
  )
})
