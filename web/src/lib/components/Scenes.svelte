<script lang="ts">
  import { devices, scenes, createScene, removeScene, runScene, type ScenePick } from '../store'
  import { haptics } from '../haptics'
  import { fade, fly } from 'svelte/transition'

  // Scenes live behind a labelled popover button. A scene is a one-tap snapshot
  // of the devices you choose (power, brightness, colour, volume) that you can
  // restore later. Creating opens an editor where you pick which devices to
  // include and, for a TV, optionally a source/app to switch to (the TV's input
  // isn't in device state, so it can't be captured — you choose it here).
  let { disabled = false }: { disabled?: boolean } = $props()

  let open = $state(false)
  let editing = $state(false)
  let rootEl: HTMLElement | undefined
  let buttonEl: HTMLButtonElement | undefined
  // The popover is positioned `fixed` and clamped to the viewport: it prefers to
  // sit under the button's right edge, but its left edge and width are pinned
  // inside a small margin so it can never be clipped on either side — on any
  // window width. (Previously, right-anchoring let it run off the left edge.)
  let menuStyle = $state('')
  function position() {
    if (!buttonEl) return
    const m = 8 // viewport margin
    const r = buttonEl.getBoundingClientRect()
    const width = Math.min(288, window.innerWidth - m * 2) // 288 = w-72
    let left = r.right - width // right edge under the button
    left = Math.min(left, window.innerWidth - m - width) // keep inside right margin
    left = Math.max(left, m) // …and inside left margin
    menuStyle = `top:${Math.round(r.bottom + 8)}px; left:${Math.round(left)}px; width:${Math.round(width)}px;`
  }

  // --- creation editor (modal) ---
  let creating = $state(false)
  let draft = $state('')
  let picks = $state<Record<string, { include: boolean; launch: string }>>({})

  const isMedia = (caps: string[]) => caps.includes('app') || caps.includes('key')

  // WoL devices are just a Wake trigger — no power, brightness, colour or volume,
  // so a scene has nothing to capture from them. Keep them out of the picker.
  let sceneDevices = $derived($devices.filter((d) => !d.capabilities.includes('wol')))

  function openCreate() {
    haptics.tap()
    draft = ''
    picks = Object.fromEntries(sceneDevices.map((d) => [d.id, { include: true, launch: '' }]))
    creating = true
    open = false
  }
  function saveScene() {
    const name = draft.trim()
    if (!name) return
    const sel: ScenePick[] = sceneDevices
      .filter((d) => picks[d.id]?.include)
      .map((d) => ({ deviceId: d.id, launch: picks[d.id].launch || undefined }))
    if (sel.length === 0) return
    haptics.tap()
    createScene(name, sel)
    creating = false
  }
  let anyPicked = $derived(Object.values(picks).some((p) => p.include))

  function run(id: string) {
    const s = $scenes.find((x) => x.id === id)
    if (!s) return
    haptics.tap()
    runScene(s)
    open = false
  }
  function focusOnMount(node: HTMLInputElement) {
    node.focus()
  }

  // Position on open, reposition on resize/scroll, and close on outside
  // pointerdown / Escape (the modal handles its own).
  $effect(() => {
    if (!open) return
    position()
    const onDown = (e: PointerEvent) => {
      if (rootEl && !rootEl.contains(e.target as Node)) open = false
    }
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') open = false
    }
    document.addEventListener('pointerdown', onDown)
    document.addEventListener('keydown', onKey)
    window.addEventListener('resize', position)
    return () => {
      document.removeEventListener('pointerdown', onDown)
      document.removeEventListener('keydown', onKey)
      window.removeEventListener('resize', position)
    }
  })
</script>

<div class="relative" bind:this={rootEl}>
  <button
    type="button"
    bind:this={buttonEl}
    onclick={() => {
      haptics.tap()
      open = !open
    }}
    aria-label="Scenes"
    aria-expanded={open}
    class="relative grid h-8 w-8 place-items-center rounded-full transition min-[360px]:h-9 min-[360px]:w-9
           {open ? 'bg-indigo-500/15 text-indigo-500 dark:text-indigo-300' : 'bg-ink/5 text-ink/70 hover:bg-ink/10 hover:text-ink'}"
  >
    <svg class="h-[18px] w-[18px]" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
      <path d="M12 2l1.6 4.6L18 8l-4.4 1.4L12 14l-1.6-4.6L6 8l4.4-1.4L12 2z" />
      <path d="M19 14l.8 2.2L22 17l-2.2.8L19 20l-.8-2.2L16 17l2.2-.8L19 14z" opacity="0.7" />
    </svg>
    {#if $scenes.length}
      <span class="absolute -right-0.5 -top-0.5 grid h-4 min-w-4 place-items-center rounded-full bg-indigo-500 px-1 text-[10px] font-semibold leading-none text-white">{$scenes.length}</span>
    {/if}
  </button>

  {#if open}
    <div
      transition:fly={{ y: -6, duration: 150 }}
      class="fixed z-40 rounded-2xl border border-ink/10 bg-panel p-3 shadow-2xl"
      style={menuStyle}
      role="dialog"
      aria-label="Scenes"
    >
      <div class="flex items-center justify-between px-1">
        <h3 class="text-sm font-semibold">Scenes</h3>
        {#if $scenes.length}
          <button
            type="button"
            onclick={() => (editing = !editing)}
            aria-pressed={editing}
            class="text-xs font-medium transition {editing ? 'text-rose-500 dark:text-rose-300' : 'text-ink/45 hover:text-ink/70'}"
          >
            {editing ? 'Done' : 'Edit'}
          </button>
        {/if}
      </div>
      <p class="mt-1 px-1 text-xs leading-relaxed text-ink/45">
        Save the look of the devices you choose, then restore it later with one tap.
      </p>

      {#if $scenes.length}
        <div class="mt-2 space-y-0.5">
          {#each $scenes as scene (scene.id)}
            <div class="flex items-center gap-1">
              <button
                type="button"
                {disabled}
                onclick={() => run(scene.id)}
                class="flex min-w-0 flex-1 items-center gap-2 rounded-xl px-2 py-2 text-left text-sm transition hover:bg-ink/5 disabled:opacity-40"
              >
                <span class="grid h-6 w-6 shrink-0 place-items-center rounded-lg bg-indigo-500/15 text-indigo-500 dark:text-indigo-300">
                  <svg class="h-3.5 w-3.5" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><path d="M8 6 18 12 8 18z" /></svg>
                </span>
                <span class="truncate">{scene.name}</span>
              </button>
              {#if editing}
                <button
                  type="button"
                  onclick={() => {
                    haptics.tap()
                    removeScene(scene.id)
                  }}
                  aria-label={`Delete scene ${scene.name}`}
                  class="grid h-8 w-8 shrink-0 place-items-center rounded-lg text-ink/40 transition hover:bg-rose-500/10 hover:text-rose-500"
                >
                  <svg class="h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                    <path d="M3 6h18M8 6V4h8v2M19 6l-1 14H6L5 6" />
                  </svg>
                </button>
              {/if}
            </div>
          {/each}
        </div>
      {/if}

      <div class="mt-2 border-t border-ink/10 pt-2">
        <button
          type="button"
          onclick={openCreate}
          class="flex w-full items-center justify-center gap-2 rounded-xl bg-ink/5 py-2 text-sm font-medium text-ink/70 transition hover:bg-ink/10"
        >
          <svg class="h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M12 5v14M5 12h14" /></svg>
          New scene
        </button>
      </div>
    </div>
  {/if}
</div>

{#if creating}
  <!-- svelte-ignore a11y_click_events_have_key_events -->
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <div
    class="fixed inset-0 z-40 grid place-items-center bg-black/50 p-4 backdrop-blur-sm"
    transition:fade={{ duration: 150 }}
    onclick={(e) => e.target === e.currentTarget && (creating = false)}
  >
    <div class="flex max-h-[85vh] w-full max-w-sm flex-col rounded-3xl border border-ink/10 bg-panel p-5 shadow-2xl" role="dialog" aria-modal="true" aria-label="New scene">
      <h2 class="text-lg font-semibold">New scene</h2>
      <p class="mt-1 text-xs text-ink/50">Pick the devices to include — their current look is saved now.</p>

      <input
        class="mt-3 w-full rounded-xl border border-ink/10 bg-ink/5 px-4 py-2.5 text-sm outline-none ring-indigo-400/50 focus:ring-2"
        type="text"
        maxlength="24"
        placeholder="Scene name (e.g. Movie mode)"
        bind:value={draft}
        use:focusOnMount
        aria-label="Scene name"
      />

      <div class="mt-3 min-h-0 flex-1 space-y-0.5 overflow-y-auto rounded-xl bg-ink/[0.03] p-1">
        {#each sceneDevices as d (d.id)}
          {#if picks[d.id]}
          <div class="flex items-center gap-2.5 rounded-lg px-2 py-2">
            <input
              id={`pick-${d.id}`}
              type="checkbox"
              bind:checked={picks[d.id].include}
              class="h-4 w-4 shrink-0 accent-indigo-500"
            />
            <label for={`pick-${d.id}`} class="min-w-0 flex-1 truncate text-sm {picks[d.id].include ? '' : 'text-ink/40'}">{d.name || d.id}</label>
            {#if isMedia(d.capabilities) && picks[d.id].include}
              <select
                bind:value={picks[d.id].launch}
                class="max-w-[44%] shrink-0 rounded-lg border border-ink/10 bg-ink/5 py-1 pl-2 pr-1 text-xs text-ink/70 outline-none"
                aria-label={`Source or app for ${d.name || d.id}`}
              >
                <option value="">Keep as-is</option>
                {#if d.capabilities.includes('key')}<option value="key:KEY_HDMI">Source: HDMI</option>{/if}
                {#each d.apps ?? [] as app (app.id)}<option value={`app:${app.id}`}>{app.name}</option>{/each}
              </select>
            {/if}
          </div>
          {/if}
        {/each}
      </div>

      <div class="mt-4 flex gap-2">
        <button
          onclick={() => (creating = false)}
          class="flex-1 rounded-xl bg-ink/5 py-2.5 font-medium text-ink/70 transition hover:bg-ink/10"
        >
          Cancel
        </button>
        <button
          onclick={saveScene}
          disabled={!draft.trim() || !anyPicked}
          class="flex-1 rounded-xl bg-gradient-to-r from-indigo-500 to-fuchsia-500 py-2.5 font-medium text-white shadow-lg shadow-indigo-500/30 transition hover:opacity-95 disabled:opacity-40"
        >
          Save scene
        </button>
      </div>
    </div>
  </div>
{/if}
