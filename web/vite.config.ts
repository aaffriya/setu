import { createHash } from 'node:crypto'
import { readFileSync, readdirSync, writeFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { defineConfig, type Plugin } from 'vite'
import { svelte } from '@sveltejs/vite-plugin-svelte'

// public/ files are copied to dist verbatim, so Vite cannot inject the hashed
// JS/CSS names into service-worker.js for us. Finish the worker after bundling:
// discover the emitted boot assets, precache them, and derive a stable cache id
// from all emitted content. An unchanged rebuild then does not churn every
// installed client, while any real UI/public-asset change gets a new cache.
function stampServiceWorker(): Plugin {
  return {
    name: 'setu-sw-build-id',
    apply: 'build',
    closeBundle() {
      const dist = new URL('./dist/', import.meta.url)
      const indexFile = fileURLToPath(new URL('index.html', dist))
      const workerFile = fileURLToPath(new URL('service-worker.js', dist))
      const index = readFileSync(indexFile, 'utf8')
      const worker = readFileSync(workerFile, 'utf8')
      // Vite may load CSS through a dynamic import so it can paint the inline
      // splash first; scan the (small, flat) output directory instead of relying
      // only on direct index.html references.
      const assetsDir = new URL('assets/', dist)
      const bootAssets = readdirSync(assetsDir, { withFileTypes: true })
        .filter((entry) => entry.isFile())
        .map((entry) => `/assets/${entry.name}`)
        .sort()
      const publicFiles = readdirSync(dist, { withFileTypes: true })
        .filter(
          (entry) =>
            entry.isFile() &&
            entry.name !== 'index.html' &&
            entry.name !== 'service-worker.js' &&
            entry.name !== '.gitkeep',
        )
        .map((entry) => entry.name)
        .sort()

      if (bootAssets.length === 0) throw new Error('Setu build produced no boot assets to precache')

      // Include the unstamped worker template too: a worker-only bug fix must
      // get a fresh cache namespace instead of mutating the cache still owned by
      // the previous active worker.
      const hash = createHash('sha256').update(index).update(worker)
      for (const asset of bootAssets) {
        hash.update(asset)
        hash.update(readFileSync(fileURLToPath(new URL(`.${asset}`, dist))))
      }
      for (const file of publicFiles) {
        hash.update(file)
        hash.update(readFileSync(fileURLToPath(new URL(file, dist))))
      }

      const id = hash.digest('hex').slice(0, 12)
      const marker = "['__PRECACHE_ASSET__']"
      if (!worker.includes(marker)) throw new Error('Setu service-worker precache marker is missing')

      writeFileSync(
        workerFile,
        worker.replaceAll('__BUILD_ID__', id).replace(marker, JSON.stringify(bootAssets)),
      )
    },
  }
}

// Setu's frontend is a single static build (web/dist) embedded into the Go
// binary. During development, `npm run dev` proxies /api and /ws to the Go
// server on :8080 so the SPA talks to a real backend with no CORS fuss. The
// shipped binary defaults to port 80; for sudo-free hot-reload dev, run the
// backend with `listen.port: 8080` to match this proxy (see README).
export default defineConfig({
  plugins: [svelte(), stampServiceWorker()],
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    target: 'es2020',
  },
  server: {
    port: 5173,
    proxy: {
      '/api': 'http://localhost:8080',
      '/ws': { target: 'ws://localhost:8080', ws: true },
    },
  },
})
