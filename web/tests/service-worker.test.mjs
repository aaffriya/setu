import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import vm from 'node:vm'

const worker = readFileSync(new URL('../public/service-worker.js', import.meta.url), 'utf8')
  .replaceAll('__BUILD_ID__', 'test-build')
  .replace("['__PRECACHE_ASSET__']", "['/assets/app.css', '/assets/app.js']")

function loadWorker(cachedRoot, options = {}) {
  const handlers = {}
  const added = []
  const network = {
    source: 'network',
    redirected: false,
    ok: true,
    headers: { get: () => 'text/javascript' },
    clone() {
      return this
    },
  }
  let networkCalls = 0
  let claimCalls = 0

  const cache = {
    addAll: async (paths) => added.push(...paths),
    delete: async () => {
      if (options.rejectCacheDelete) throw new Error('cache delete failed')
      return true
    },
    match: async (path) => {
      assert.equal(path, '/')
      if (options.rejectCacheRead) throw new Error('cache read failed')
      return cachedRoot
    },
    put: async () => {},
  }
  const context = {
    URL,
    caches: {
      delete: async () => {
        if (options.rejectCacheDelete) throw new Error('cache delete failed')
        return true
      },
      keys: async () => {
        if (options.rejectCacheKeys) throw new Error('cache keys failed')
        return options.cacheKeys ?? []
      },
      match: async () => {
        if (options.rejectCacheRead) throw new Error('cache read failed')
        return options.cachedAsset
      },
      open: async () => {
        if (options.rejectCacheOpen) throw new Error('cache open failed')
        return cache
      },
    },
    fetch: async () => {
      networkCalls++
      return network
    },
    self: {
      clients: {
        claim: async () => {
          claimCalls++
        },
      },
      location: { origin: 'https://setu.test' },
      skipWaiting: async () => {},
      addEventListener: (type, handler) => {
        handlers[type] = handler
      },
    },
  }

  vm.runInNewContext(worker, context)
  return {
    added,
    handlers,
    network,
    claimCalls: () => claimCalls,
    networkCalls: () => networkCalls,
  }
}

async function install(app) {
  let work
  app.handlers.install({ waitUntil: (promise) => (work = promise) })
  await work
}

async function navigate(app) {
  let response
  app.handlers.fetch({
    request: { method: 'GET', mode: 'navigate', url: 'https://setu.test/' },
    respondWith: (promise) => (response = promise),
  })
  return response
}

async function fetchStatic(app) {
  let response
  app.handlers.fetch({
    request: { method: 'GET', mode: 'cors', url: 'https://setu.test/assets/app.js' },
    respondWith: (promise) => (response = promise),
  })
  return response
}

async function activate(app) {
  let work
  app.handlers.activate({ waitUntil: (promise) => (work = promise) })
  await work
}

test('precache uses only the canonical document URL', async () => {
  const app = loadWorker(null)
  await install(app)

  assert.ok(app.added.includes('/'))
  assert.ok(!app.added.includes('/index.html'))
  assert.ok(app.added.includes('/assets/app.css'))
  assert.ok(app.added.includes('/assets/app.js'))
})

test('canonical cached root serves refresh without the network', async () => {
  const cached = { source: 'cache', redirected: false }
  const app = loadWorker(cached)

  assert.equal(await navigate(app), cached)
  assert.equal(app.networkCalls(), 0)
})

test('redirected cached root is rejected and refreshed from the network', async () => {
  const app = loadWorker({ source: 'cache', redirected: true })

  assert.equal(await navigate(app), app.network)
  assert.equal(app.networkCalls(), 1)
})

test('navigation falls back to the network when the cache cannot be opened or read', async () => {
  for (const options of [{ rejectCacheOpen: true }, { rejectCacheRead: true }]) {
    const app = loadWorker(null, options)

    assert.equal(await navigate(app), app.network)
    assert.equal(app.networkCalls(), 1)
  }
})

test('static assets fall back to the network when the cache read rejects', async () => {
  const app = loadWorker(null, { rejectCacheRead: true })

  assert.equal(await fetchStatic(app), app.network)
  assert.equal(app.networkCalls(), 1)
})

test('cache cleanup errors do not prevent worker activation', async () => {
  const app = loadWorker(null, { rejectCacheOpen: true, rejectCacheKeys: true })

  await activate(app)
  assert.equal(app.claimCalls(), 1)
})
