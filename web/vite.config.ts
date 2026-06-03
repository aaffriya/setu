import { defineConfig } from 'vite'
import { svelte } from '@sveltejs/vite-plugin-svelte'

// Setu's frontend is a single static build (web/dist) embedded into the Go
// binary. During development, `npm run dev` proxies /api and /ws to the Go
// server on :8080 so the SPA talks to a real backend with no CORS fuss.
export default defineConfig({
  plugins: [svelte()],
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
