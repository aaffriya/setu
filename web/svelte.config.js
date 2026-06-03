import { vitePreprocess } from '@sveltejs/vite-plugin-svelte'

// vitePreprocess enables <script lang="ts"> and modern CSS in .svelte files.
export default {
  preprocess: vitePreprocess(),
}
