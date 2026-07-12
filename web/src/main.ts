import { mount } from 'svelte'
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

// Load the processed app stylesheet asynchronously so Vite does not emit a
// render-blocking <link> ahead of the inline splash. The splash remains on top
// until both CSS and Svelte are ready, so a slow first request still has a real
// first paint instead of an empty standalone-PWA surface.
void import('./app.css')
  .then(() => {
    mount(App, { target: document.getElementById('app')! })

    // Tear down the pre-app splash only after the styled app has mounted. If the
    // JS/CSS cannot load, index.html's watchdog keeps the useful offline card.
    clearTimeout(window.__setuSplashWatchdog)
    const splash = document.getElementById('splash')
    if (splash) {
      splash.classList.add('hide')
      setTimeout(() => splash.remove(), 450)
    }
  })
  .catch((err) => console.error('app startup failed:', err))

// Register the PWA service worker (app-shell cache). Service workers only run in
// a secure context (HTTPS or localhost); browsers block them on plain
// http://<lan-ip>, so we guard on isSecureContext and fail soft otherwise.
if ('serviceWorker' in navigator && window.isSecureContext) {
  // An activated worker owns the next natural navigation. Do not force-reload
  // existing clients on controllerchange: activation often happens while a PWA
  // is backgrounded, and reloading a suspended page is exactly how users return
  // to a blank/cold-started app.
  window.addEventListener('load', () => {
    navigator.serviceWorker.register('/service-worker.js').catch((err) => {
      console.warn('service worker registration failed:', err)
    })
  })
}
