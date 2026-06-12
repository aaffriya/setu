import { readFileSync, writeFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { defineConfig, type Plugin } from 'vite'
import { svelte } from '@sveltejs/vite-plugin-svelte'

// public/ files are copied to dist verbatim (not bundled), so vite's `define`
// cannot reach service-worker.js. Stamp its __BUILD_ID__ after the build
// instead: each deploy then gets a byte-different worker with its own cache
// name, which is what lets the worker's `activate` evict the previous build's
// cached assets (see public/service-worker.js). replaceAll, not replace: the
// marker also appears in the worker's own comments, and a first-match replace
// would stamp the comment and leave the actual cache name unstamped.
function stampServiceWorker(): Plugin {
  return {
    name: 'setu-sw-build-id',
    apply: 'build',
    closeBundle() {
      const file = fileURLToPath(new URL('./dist/service-worker.js', import.meta.url))
      const id = Date.now().toString(36)
      writeFileSync(file, readFileSync(file, 'utf8').replaceAll('__BUILD_ID__', id))
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
