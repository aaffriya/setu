import { mount } from 'svelte'
import App from './App.svelte'
import { getTheme, applyTheme } from './lib/theme'

// Apply any forced theme before mount so there's no flash of the OS theme.
applyTheme(getTheme())

// Setu is a fixed-scale app surface (see index.html's viewport). Every browser
// but one honours that: iOS Safari applies `user-scalable=no` to the automatic
// zoom into a focused field and to a standalone PWA, yet still allows the pinch
// gesture in a tab. These WebKit-only events are what is left to refuse, so an
// accidental two-finger touch cannot leave the app scrolling around a zoomed
// page. Registered once, before mount, so it covers the splash screen too.
for (const gesture of ['gesturestart', 'gesturechange', 'gestureend']) {
  document.addEventListener(gesture, (event) => event.preventDefault(), { passive: false })
}

// A desktop trackpad pinch arrives as a wheel event with ctrl held, and that one
// IS cancellable — so the same two-finger gesture is refused on a laptop as on a
// phone. The keyboard shortcuts and the browser's own zoom control are user-agent
// accelerators: no page can intercept those, and page zoom is remembered per
// origin, so a level set there stays until the user resets it.
window.addEventListener(
  'wheel',
  (event) => {
    if (event.ctrlKey || event.metaKey) event.preventDefault()
  },
  { passive: false },
)

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
