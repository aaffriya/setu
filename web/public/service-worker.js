// Setu service worker — caches the app shell so the PWA loads instantly and
// survives brief network drops. Runtime caching only: we NEVER cache the JSON
// API (/api) or the WebSocket (/ws), which must always be live.
//
// The cache id and Vite's hashed boot assets are injected after each production
// build (see web/vite.config.ts). The marker entry is filtered out in dev, where
// public/ is served directly without that post-build step.

const CACHE = 'setu-shell-__BUILD_ID__'
const BOOT_ASSETS = ['__PRECACHE_ASSET__']
const IS_BUILT = BOOT_ASSETS.some((asset) => asset.startsWith('/assets/'))
const SHELL = [
  '/',
  '/manifest.webmanifest',
  '/icon.svg',
  '/favicon.svg',
  ...BOOT_ASSETS.filter((asset) => asset.startsWith('/assets/')),
]

self.addEventListener('install', (event) => {
  // Vite serves public/ verbatim in dev, before the build markers are injected.
  // Keep that worker inert so it can never cache-first stale source modules.
  if (!IS_BUILT) {
    event.waitUntil(self.skipWaiting())
    return
  }
  event.waitUntil(
    caches
      .open(CACHE)
      .then((cache) => cache.addAll(SHELL))
      .then(() => self.skipWaiting()),
  )
})

self.addEventListener('activate', (event) => {
  if (!IS_BUILT) {
    // Self-heal anyone who previously ran the caching worker on localhost.
    event.waitUntil(
      caches
        .delete(CACHE)
        .catch(() => {})
        .then(() => self.clients.claim()),
    )
    return
  }
  event.waitUntil(
    Promise.all([
      // Remove the redirect-tainted legacy key from installations created by
      // the broken worker. Navigations no longer read it, but cleanup makes the
      // upgrade self-healing without asking the user to clear site data.
      caches.open(CACHE).then((cache) => cache.delete('/index.html')),
      caches.keys().then((keys) =>
        Promise.all(
          keys.filter((k) => k.startsWith('setu-shell-') && k !== CACHE).map((k) => caches.delete(k)),
        ),
      ),
    ])
      // Cleanup is best-effort: a broken/quota-limited CacheStorage must not
      // prevent the new worker from activating and serving live network data.
      .catch(() => {})
      .then(() => self.clients.claim()),
  )
})

self.addEventListener('fetch', (event) => {
  if (!IS_BUILT) return
  const req = event.request
  const url = new URL(req.url)

  // Only handle same-origin GETs; leave the API and WebSocket untouched.
  if (req.method !== 'GET' || url.origin !== self.location.origin) return
  if (url.pathname.startsWith('/api') || url.pathname.startsWith('/ws')) return

  // Navigations: the versioned cached shell wins immediately. Waiting for a LAN
  // fetch to reject can leave an installed PWA on its native blank screen for a
  // long time after the mobile OS killed it. Worker updates still stay fresh:
  // each build installs a new content-derived cache, and the next natural launch
  // is controlled by that worker.
  if (req.mode === 'navigate') {
    event.respondWith(
      caches
        .open(CACHE)
        .then((cache) => cache.match('/'))
        .catch(() => undefined)
        .then((cached) => {
          // Cache only the canonical root. Go's file server historically
          // redirected /index.html to ./; browsers reject a redirected Response
          // returned directly by a service worker on navigation, which made the
          // first controlled refresh blank until site data was cleared.
          return cached && !cached.redirected ? cached : fetch(req)
        }),
    )
    return
  }

  // Static assets (hashed JS/CSS, icons): cache-first, then network + cache.
  event.respondWith(
    caches
      .match(req)
      .catch(() => undefined)
      .then(
        (cached) =>
          cached ||
          fetch(req).then(async (res) => {
            // Cache only good, non-HTML responses. The Go server answers an
            // unknown non-asset path with index.html (SPA fallback), so a request
            // for a stale hashed asset would otherwise poison the cache with
            // HTML under the asset URL — and cache-first would serve that
            // forever. Same guard keeps error responses out.
            const type = res.headers.get('content-type') || ''
            if (res.ok && !type.includes('text/html')) {
              const copy = res.clone()
              try {
                const cache = await caches.open(CACHE)
                await cache.put(req, copy)
              } catch {
                // Runtime caching is best-effort. Storage pressure must never turn
                // an otherwise-good network response into a failed app resource.
              }
            }
            return res
          }),
      ),
  )
})
