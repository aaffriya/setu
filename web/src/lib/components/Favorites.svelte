<script lang="ts">
  import type { Device } from '../api'
  import { favorites, addFavorite, removeFavorite, applyFavorite, type Favorite } from '../store'
  import { haptics } from '../haptics'

  // Saved presets (color / white-temp / scene) per device. Tap a chip to apply,
  // the heart to save the current look. Removal is hidden behind an edit toggle
  // (the pencil): chips show no cross until you enter edit mode, so a stray tap
  // can't delete a favourite. Stored in localStorage.
  let { device, disabled = false }: { device: Device; disabled?: boolean } = $props()

  let list = $derived($favorites[device.id] ?? [])
  let editing = $state(false)

  // Nothing left to edit → leave edit mode, so re-adding a favourite later starts
  // calm (no cross showing until the user opts back in).
  $effect(() => {
    if (list.length === 0 && editing) editing = false
  })

  function saveCurrent() {
    const s = device.state
    // Capture the current brightness too (when the device dims and is lit), so a
    // favourite restores the whole look. Shown in the chip label as "· NN%".
    const bri =
      device.capabilities.includes('brightness') && s.brightness > 0 ? s.brightness : undefined
    const suffix = bri ? ` · ${bri}%` : ''
    if (s.scene) {
      const name = device.scenes?.find((x) => x.id === s.scene)?.name ?? `Scene ${s.scene}`
      addFavorite(device.id, { kind: 'scene', value: s.scene, label: name + suffix, brightness: bri })
    } else if (s.color_temp) {
      addFavorite(device.id, {
        kind: 'color_temp',
        value: s.color_temp,
        label: `${s.color_temp}K${suffix}`,
        brightness: bri,
      })
    } else {
      addFavorite(device.id, { kind: 'color', value: s.color, label: `Color${suffix}`, brightness: bri })
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
        onclick={() => {
          haptics.tap()
          applyFavorite(device.id, fav)
        }}
        class="flex items-center gap-1.5 rounded-full bg-ink/5 py-1 pl-1.5 pr-2.5 text-xs text-ink/80 transition hover:bg-ink/10 disabled:opacity-40"
      >
        <span class="h-4 w-4 rounded-full ring-1 ring-ink/15" style={dotStyle(fav)}></span>
        {fav.label}
      </button>
      {#if editing}
        <button
          type="button"
          onclick={() => {
            haptics.tap()
            removeFavorite(device.id, fav.id)
          }}
          aria-label={`Remove favourite ${fav.label}`}
          class="absolute -right-1.5 -top-1.5 grid h-4 w-4 place-items-center rounded-full bg-rose-500/90 text-[10px] leading-none text-white shadow transition hover:bg-rose-500"
        >×</button>
      {/if}
    </div>
  {/each}

  <button
    type="button"
    {disabled}
    onclick={() => {
      haptics.tap()
      saveCurrent()
    }}
    aria-label="Save current as favourite"
    class="grid h-7 w-7 place-items-center rounded-full bg-ink/5 text-ink/60 transition hover:bg-ink/10 hover:text-rose-300 disabled:cursor-not-allowed disabled:opacity-40"
  >
    <!-- heart + plus: "add the current look to favourites". -->
    <svg class="h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      <path d="M12 21s-7-4.5-9.5-8.5C1 9 2.5 6 5.5 6c1.8 0 3 1 2.5 2 .5-1 1.7-2 3.5-2 3 0 4.5 3 3 6.5C19 16.5 12 21 12 21z" />
      <path d="M12 9.3v3.7M10.2 11.1h3.6" />
    </svg>
  </button>

  {#if list.length > 0}
    <button
      type="button"
      onclick={() => {
        haptics.tap()
        editing = !editing
      }}
      aria-pressed={editing}
      aria-label={editing ? 'Done editing favourites' : 'Edit favourites'}
      class="grid h-7 w-7 place-items-center rounded-full transition
             {editing ? 'bg-rose-500/15 text-rose-500 dark:text-rose-300' : 'bg-ink/5 text-ink/60 hover:bg-ink/10'}"
    >
      {#if editing}
        <!-- check: done editing -->
        <svg class="h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <path d="M5 13l4 4L19 7" />
        </svg>
      {:else}
        <!-- pencil: edit (reveals remove buttons) -->
        <svg class="h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <path d="M12 20h9" />
          <path d="M16.5 3.5a2.12 2.12 0 0 1 3 3L7 19l-4 1 1-4z" />
        </svg>
      {/if}
    </button>
  {/if}
</div>
