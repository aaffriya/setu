import { mount } from 'svelte'
import './app.css'
import App from './App.svelte'
import { getTheme, applyTheme } from './lib/theme'

// Apply any forced theme before mount so there's no flash of the OS theme.
applyTheme(getTheme())

// Lock zoom for an app-like feel. The viewport meta's `user-scalable=no` is
// ignored by iOS Safari, so block pinch-zoom (any multi-touch move) and the
// iOS pinch gesture explicitly. Single-finger scrolling and taps are untouched;
// double-tap zoom is already handled by `touch-action: manipulation` in app.css.
document.addEventListener(
  'touchmove',
  (e) => {
    if (e.touches.length > 1) e.preventDefault()
  },
  { passive: false },
)
document.addEventListener('gesturestart', (e) => e.preventDefault())

const app = mount(App, { target: document.getElementById('app')! })

// Tear down the pre-app splash (see index.html). We've mounted, so cancel its
// watchdog (no offline card can flash now) and fade the loader out — the cross-
// fade lands on the app shell's own fade-in. If the server is unreachable the
// app itself shows the richer "Can't reach Setu" screen with a retry.
clearTimeout(window.__setuSplashWatchdog)
const splash = document.getElementById('splash')
if (splash) {
  splash.classList.add('hide')
  setTimeout(() => splash.remove(), 450)
}

// Register the PWA service worker (app-shell cache). Service workers only run in
// a secure context (HTTPS or localhost); browsers block them on plain
// http://<lan-ip>, so we guard on isSecureContext and fail soft otherwise.
if ('serviceWorker' in navigator && window.isSecureContext) {
  // Self-healing updates: a new build ships a byte-different worker (its cache id
  // is stamped per build), which installs, skipWaiting-activates and claims the
  // page. The page that's already open was loaded under the OLD worker, though,
  // so it keeps showing the old cached shell until the next manual reload — the
  // classic "I rebuilt but still see the old/blank screen" trap. So when a *new*
  // worker takes control (not the first-ever install), reload once to pick up the
  // fresh shell. The `controller` check skips the first install; the `reloading`
  // latch prevents any reload loop.
  let reloading = false
  const hadController = !!navigator.serviceWorker.controller
  navigator.serviceWorker.addEventListener('controllerchange', () => {
    if (!hadController || reloading) return
    reloading = true
    location.reload()
  })
  window.addEventListener('load', () => {
    navigator.serviceWorker.register('/service-worker.js').catch((err) => {
      console.warn('service worker registration failed:', err)
    })
  })
}

export default app
