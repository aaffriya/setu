<script lang="ts">
  import type { Scene } from '../api'
  import { haptics } from '../haptics'

  // Predefined-scene selector. A native <select> is the compact, accessible
  // choice for a long brand-defined list (WiZ has 32). Scenes come from the
  // device data, so this component stays device-agnostic.
  let {
    scenes = [],
    value = 0,
    disabled = false,
    onPick,
  }: {
    scenes?: Scene[]
    value?: number
    disabled?: boolean
    onPick?: (id: number) => void
  } = $props()

  function handle(event: Event) {
    const id = Number((event.target as HTMLSelectElement).value)
    if (id) {
      haptics.tap()
      onPick?.(id)
    }
  }
</script>

<div class="flex items-center gap-3">
  <svg class="h-4 w-4 shrink-0 text-ink/50" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
    <path d="M12 2l1.6 4.6L18 8l-4.4 1.4L12 14l-1.6-4.6L6 8l4.4-1.4z" />
    <path d="M18 14l.8 2.2L21 17l-2.2.8L18 20l-.8-2.2L15 17l2.2-.8z" />
  </svg>
  <select
    class="w-full appearance-none rounded-xl border border-ink/10 bg-ink/5 px-3 py-2 text-sm text-ink/90 outline-none transition focus:border-indigo-400/50 disabled:cursor-not-allowed disabled:opacity-40"
    value={value}
    {disabled}
    onchange={handle}
    aria-label="Scene"
  >
    <option value={0} disabled>Choose a scene…</option>
    {#each scenes as scene (scene.id)}
      <option value={scene.id}>{scene.name}</option>
    {/each}
  </select>
</div>
