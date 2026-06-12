// Setu service worker — caches the app shell so the PWA loads instantly and
// survives brief network drops. Runtime caching only: we NEVER cache the JSON
// API (/api) or the WebSocket (/ws), which must always be live.
//
// __BUILD_ID__ is stamped per build (see web/vite.config.ts: public/ files are
// copied verbatim, so it can't use vite's `define`). A new build therefore
// byte-changes this file → the browser reinstalls the worker → `activate`
// evicts the previous build's cache. No manual version bump to forget, and old
// hashed assets can't accumulate forever under one cache name. In dev the
// placeholder itself serves as the cache name, which is fine.

const CACHE = 'setu-shell-__BUILD_ID__'
const SHELL = ['/', '/index.html', '/manifest.webmanifest', '/icon.svg', '/favicon.svg']

self.addEventListener('install', (event) => {
  event.waitUntil(
    caches
      .open(CACHE)
      .then((cache) => cache.addAll(SHELL))
      .then(() => self.skipWaiting()),
  )
})

self.addEventListener('activate', (event) => {
  event.waitUntil(
    caches
      .keys()
      .then((keys) => Promise.all(keys.filter((k) => k !== CACHE).map((k) => caches.delete(k))))
      .then(() => self.clients.claim()),
  )
})

self.addEventListener('fetch', (event) => {
  const req = event.request
  const url = new URL(req.url)

  // Only handle same-origin GETs; leave the API and WebSocket untouched.
  if (req.method !== 'GET' || url.origin !== self.location.origin) return
  if (url.pathname.startsWith('/api') || url.pathname.startsWith('/ws')) return

  // Navigations: network-first, falling back to the cached shell when offline.
  if (req.mode === 'navigate') {
    event.respondWith(
      fetch(req).catch(() => caches.match('/index.html').then((r) => r || caches.match('/'))),
    )
    return
  }

  // Static assets (hashed JS/CSS, icons): cache-first, then network + cache.
  event.respondWith(
    caches.match(req).then(
      (cached) =>
        cached ||
        fetch(req).then((res) => {
          // Cache only good, non-HTML responses. The Go server answers ANY
          // unknown path with 200 + index.html (SPA fallback), so a request
          // for a stale hashed asset would otherwise poison the cache with
          // HTML under the asset URL — and cache-first would serve that
          // forever. Same guard keeps error responses out.
          const type = res.headers.get('content-type') || ''
          if (res.ok && !type.includes('text/html')) {
            const copy = res.clone()
            caches.open(CACHE).then((cache) => cache.put(req, copy))
          }
          return res
        }),
    ),
  )
})
