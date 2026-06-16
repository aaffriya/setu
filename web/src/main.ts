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

// Register the PWA service worker (app-shell cache). Service workers only run in
// a secure context (HTTPS or localhost); browsers block them on plain
// http://<lan-ip>, so we guard on isSecureContext and fail soft otherwise.
if ('serviceWorker' in navigator && window.isSecureContext) {
  window.addEventListener('load', () => {
    navigator.serviceWorker.register('/service-worker.js').catch((err) => {
      console.warn('service worker registration failed:', err)
    })
  })
}

export default app
