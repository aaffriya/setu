<script lang="ts">
  import type { Device } from '../api'
  import { favorites, addFavorite, removeFavorite, applyFavorite, type Favorite } from '../store'

  // Saved presets (color / white-temp / scene) per device. Tap a chip to apply,
  // the heart to save the current look, the × to remove. Stored in localStorage.
  let { device, disabled = false }: { device: Device; disabled?: boolean } = $props()

  let list = $derived($favorites[device.id] ?? [])

  function saveCurrent() {
    const s = device.state
    if (s.scene) {
      const name = device.scenes?.find((x) => x.id === s.scene)?.name ?? `Scene ${s.scene}`
      addFavorite(device.id, { kind: 'scene', value: s.scene, label: name })
    } else if (s.color_temp) {
      addFavorite(device.id, { kind: 'color_temp', value: s.color_temp, label: `${s.color_temp}K` })
    } else {
      addFavorite(device.id, { kind: 'color', value: s.color, label: 'Color' })
    }
  }

  function dotStyle(f: Favorite): string {
    if (f.kind === 'color') {
      const c = f.value as { r: number; g: number; b: number }
      return `background: rgb(${c.r} ${c.g} ${c.b})`
    }
    if (f.kind === 'color_temp') return 'background:#ffd9a0'
    return 'background: conic-gradient(from 0deg,#f87171,#fbbf24,#34d399,#60a5fa,#a78bfa,#f472b6,#f87171)'
  }
</script>

<div class="flex flex-wrap items-center gap-2">
  <span class="w-9 shrink-0 text-xs font-medium uppercase tracking-wider text-ink/40">Favs</span>

  {#each list as fav (fav.id)}
    <div class="relative">
      <button
        type="button"
        {disabled}
        onclick={() => applyFavorite(device.id, fav)}
        class="flex items-center gap-1.5 rounded-full bg-ink/5 py-1 pl-1.5 pr-2.5 text-xs text-ink/80 transition hover:bg-ink/10 disabled:opacity-40"
      >
        <span class="h-4 w-4 rounded-full ring-1 ring-ink/15" style={dotStyle(fav)}></span>
        {fav.label}
      </button>
      <button
        type="button"
        onclick={() => removeFavorite(device.id, fav.id)}
        aria-label="Remove favourite"
        class="absolute -right-1.5 -top-1.5 grid h-4 w-4 place-items-center rounded-full bg-rose-500/90 text-[10px] leading-none text-white shadow transition hover:bg-rose-500"
      >×</button>
    </div>
  {/each}

  <button
    type="button"
    {disabled}
    onclick={saveCurrent}
    aria-label="Save current as favourite"
    class="grid h-7 w-7 place-items-center rounded-full bg-ink/5 text-ink/60 transition hover:bg-ink/10 hover:text-rose-300 disabled:cursor-not-allowed disabled:opacity-40"
  >
    <svg class="h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true">
      <path d="M12 21s-7-4.5-9.5-8.5C1 9 2.5 6 5.5 6c1.8 0 3 1 2.5 2 .5-1 1.7-2 3.5-2 3 0 4.5 3 3 6.5C19 16.5 12 21 12 21z" />
    </svg>
  </button>
</div>
