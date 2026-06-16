// PWA install affordance. A browser only offers to install in a secure context
// (HTTPS or localhost) with a registered service worker — on a plain
// http://<lan-ip> address none of that holds and no install option appears at
// all (this is exactly why the optional TLS listener exists; see config.yaml).
//
// When the browser does consider the app installable it fires
// `beforeinstallprompt`; we capture it so the app can show its own Install
// button, since the browser's built-in affordance is easy to miss. iOS Safari
// never fires that event — there you install via Share → "Add to Home Screen" —
// so we detect iOS to show a hint instead. All feature-detected and fail-soft,
// like haptics/wakelock.

import { writable } from 'svelte/store'

type InstallPrompt = Event & { prompt: () => Promise<void> }

let deferred: InstallPrompt | null = null

// True once the browser tells us the app is installable (Chrome/Edge/Android).
export const canInstall = writable(false)

// Already launched as an installed (standalone) app — nothing left to offer.
export const isStandalone =
  typeof window !== 'undefined' &&
  (window.matchMedia?.('(display-mode: standalone)').matches === true ||
    (navigator as Navigator & { standalone?: boolean }).standalone === true)

// iOS can install (Add to Home Screen) but never fires beforeinstallprompt.
export const isIOS =
  typeof navigator !== 'undefined' && /iphone|ipad|ipod/i.test(navigator.userAgent)

// A secure context is the hard prerequisite for any install path on non-iOS.
export const secureContext = typeof window !== 'undefined' && window.isSecureContext

if (typeof window !== 'undefined') {
  window.addEventListener('beforeinstallprompt', (e) => {
    e.preventDefault() // keep our own button in charge of when to prompt
    deferred = e as InstallPrompt
    canInstall.set(true)
  })
  window.addEventListener('appinstalled', () => {
    deferred = null
    canInstall.set(false)
  })
}

// Show the browser's install dialog. No-op if no prompt was captured.
export async function promptInstall(): Promise<void> {
  if (!deferred) return
  await deferred.prompt()
  deferred = null
  canInstall.set(false)
}
