<script lang="ts">
  import { onMount, onDestroy } from 'svelte'
  import { get } from 'svelte/store'
  import { fade, fly } from 'svelte/transition'
  import { flip } from 'svelte/animate'
  import {
    devices,
    connection,
    lastError,
    lastUpdated,
    rooms,
    order,
    orderDevices,
    moveDevice,
    setRoom,
    refresh,
    connect,
    disconnect,
    resume,
    command,
    type ConnectionStatus,
  } from './lib/store'
  import { getToken, setToken } from './lib/api'
  import { getTheme, setTheme, type Theme } from './lib/theme'
  import DeviceCard from './lib/components/DeviceCard.svelte'
  import Scenes from './lib/components/Scenes.svelte'

  let token = $state(getToken())
  let tokenDraft = $state(getToken())
  let showSettings = $state(false)
  let themeChoice = $state<Theme>(getTheme())
  let confirmReset = $state(false)
  // Forget the reset confirmation whenever the dialog closes.
  $effect(() => {
    if (!showSettings) confirmReset = false
  })

  const themes: Theme[] = ['system', 'light', 'dark']
  function chooseTheme(t: Theme) {
    themeChoice = t
    setTheme(t) // applies live
  }
  // Reset wipes every client-side preference (scenes, rooms, layout, theme, …)
  // but keeps the access token so the user stays signed in.
  function resetApp() {
    let t: string | null = null
    try {
      t = localStorage.getItem('setu.token')
      localStorage.clear()
      if (t) localStorage.setItem('setu.token', t)
    } catch {
      // storage disabled — nothing to clear
    }
    location.reload()
  }

  // Show the connect card when we have no token, or the server rejected it.
  let needsToken = $derived(!token || $connection === 'unauthorized')

  // --- list organisation (search / rooms / manual order) ---------------------
  let query = $state('')
  let activeRoom = $state('')
  let organizing = $state(false)
  let searching = $state(false)
  let hasDevices = $derived(!needsToken && $devices.length > 0)

  // Room names actually in use by the current devices (for the filter chips).
  let roomNames = $derived.by(() => {
    const set = new Set<string>()
    for (const d of $devices) {
      const r = $rooms[d.id]
      if (r) set.add(r)
    }
    return [...set].sort()
  })
  // If the active room filter disappears (its last device was reassigned), drop it.
  $effect(() => {
    if (activeRoom && !roomNames.includes(activeRoom)) activeRoom = ''
  })

  // The grid list: manual order applied, then filtered by room + search. The same
  // filter applies while organizing, so reordering within a room view is coherent
  // (moveDevice still rewrites the *global* order — see endDrag).
  let displayDevices = $derived.by(() => {
    const ordered = orderDevices($devices, $order)
    const q = query.trim().toLowerCase()
    // Default case (no manual order, no active filter): pass the list straight
    // through — no copy/sort/filter allocation on each WS state update.
    if (!q && !activeRoom) return ordered
    return ordered.filter((d) => {
      if (activeRoom && ($rooms[d.id] ?? '') !== activeRoom) return false
      if (q && !`${d.name} ${d.brand} ${d.series ?? ''} ${d.model}`.toLowerCase().includes(q))
        return false
      return true
    })
  })

  // --- offline "updated Xs ago" hint -----------------------------------------
  // Tick a clock only while not live, so the relative label stays current without
  // a timer running on the happy path.
  let now = $state(Date.now())
  $effect(() => {
    if ($connection === 'online') return
    const t = setInterval(() => (now = Date.now()), 5000)
    return () => clearInterval(t)
  })
  let agoLabel = $derived.by(() => {
    if ($connection === 'online' || !$lastUpdated) return ''
    const secs = Math.max(0, Math.round((now - $lastUpdated) / 1000))
    if (secs < 60) return `${secs}s ago`
    const mins = Math.round(secs / 60)
    return mins < 60 ? `${mins}m ago` : `${Math.round(mins / 60)}h ago`
  })

  // If we have no devices and stay stuck "connecting" (a hung/unreachable server
  // that never refuses), fall back to the clear "can't reach" screen after a
  // few seconds instead of spinning forever. Resets the moment things change.
  let stalled = $state(false)
  $effect(() => {
    if ($devices.length > 0 || $connection !== 'connecting') {
      stalled = false
      return
    }
    const t = setTimeout(() => (stalled = true), 8000)
    return () => clearTimeout(t)
  })

  // --- drag-to-reorder (organize mode) ---------------------------------------
  // The dragged card lifts and follows the pointer (a transform, so it's smooth
  // and cheap); the card under the pointer is highlighted as the drop target. We
  // commit the reorder only on release — no mid-drag reflow, no localStorage
  // churn — and the grid's flip animation settles the card into its new slot.
  // Pointer-based → works on touch and mouse. No library.
  let draggingId = $state<string | null>(null)
  let overId = $state<string | null>(null)
  let dragDX = $state(0)
  let dragDY = $state(0)
  let startX = 0
  let startY = 0
  function startDrag(e: PointerEvent, id: string) {
    draggingId = id
    overId = null
    startX = e.clientX
    startY = e.clientY
    dragDX = 0
    dragDY = 0
    ;(e.currentTarget as HTMLElement).setPointerCapture?.(e.pointerId)
    window.addEventListener('pointermove', onDragMove)
    window.addEventListener('pointerup', endDrag)
    window.addEventListener('pointercancel', endDrag)
  }
  function onDragMove(e: PointerEvent) {
    if (!draggingId) return
    dragDX = e.clientX - startX
    dragDY = e.clientY - startY
    // The dragged card has pointer-events:none, so elementFromPoint sees the card
    // beneath it — that's the drop target to highlight.
    const t = (document.elementFromPoint(e.clientX, e.clientY) as HTMLElement | null)
      ?.closest<HTMLElement>('[data-devid]')?.dataset.devid
    overId = t && t !== draggingId ? t : null
  }
  function endDrag() {
    if (draggingId && overId) {
      // Reorder within the *full* global order (not the filtered view) so a drag
      // inside a room keeps every other device in place.
      const ids = orderDevices(get(devices), get(order)).map((d) => d.id)
      moveDevice(ids, draggingId, overId)
    }
    draggingId = null
    overId = null
    dragDX = 0
    dragDY = 0
    window.removeEventListener('pointermove', onDragMove)
    window.removeEventListener('pointerup', endDrag)
    window.removeEventListener('pointercancel', endDrag)
  }
  function dragStyle(id: string): string {
    if (draggingId !== id) return ''
    return `transform: translate(${dragDX}px, ${dragDY}px) scale(1.03); transition: none; pointer-events: none; position: relative; z-index: 50; cursor: grabbing; box-shadow: 0 22px 50px -12px rgba(2,6,23,.5);`
  }

  function onVisibility() {
    if (document.visibilityState === 'visible') resume()
  }

  // Manifest shortcuts (long-press / jump-list) launch the app at /?do=all_off|all_on.
  // Fire the matching power command at every switchable device, then strip the
  // param so a refresh can't replay it. Runs after the first refresh so it acts
  // on the live device list, not just whatever was cached.
  function runLaunchAction() {
    const params = new URLSearchParams(location.search)
    const action = params.get('do')
    if (action !== 'all_off' && action !== 'all_on') return
    const want = action === 'all_off' ? 'off' : 'on'
    for (const d of get(devices)) {
      if (d.capabilities.includes('switch')) void command(d.id, want)
    }
    params.delete('do')
    const qs = params.toString()
    history.replaceState(null, '', location.pathname + (qs ? `?${qs}` : '') + location.hash)
  }

  onMount(() => {
    refresh().then(runLaunchAction)
    connect()
    document.addEventListener('visibilitychange', onVisibility)
    window.addEventListener('online', resume)
  })

  // Clean up listeners and the socket so nothing leaks (matters on mobile).
  onDestroy(() => {
    document.removeEventListener('visibilitychange', onVisibility)
    window.removeEventListener('online', resume)
    disconnect()
  })

  function saveToken() {
    token = tokenDraft.trim()
    setToken(token)
    showSettings = false
    refresh()
    // Drop any existing socket first: it authenticated with the previous
    // token, and connect() alone deliberately refuses to replace a live one
    // (the one-socket rule in store.ts).
    disconnect()
    connect()
  }

  function closeSearch() {
    searching = false
    query = ''
  }
  function focusOnMount(node: HTMLInputElement) {
    node.focus()
  }
  function onWindowKey(e: KeyboardEvent) {
    if (e.key !== 'Escape') return
    if (showSettings) showSettings = false
    else if (searching) closeSearch()
    else if (organizing) organizing = false
  }

  const statusLabel: Record<ConnectionStatus, string> = {
    connecting: 'Connecting…',
    online: 'Live',
    offline: 'Offline',
    unauthorized: 'Locked',
  }
  const statusDot: Record<ConnectionStatus, string> = {
    connecting: 'bg-amber-400',
    online: 'bg-emerald-400',
    offline: 'bg-rose-400',
    unauthorized: 'bg-rose-400',
  }

  const iconBtn =
    'grid h-9 w-9 shrink-0 place-items-center rounded-full bg-ink/5 text-ink/70 transition hover:bg-ink/10 hover:text-ink'
  const iconBtnActive =
    'grid h-9 w-9 shrink-0 place-items-center rounded-full bg-indigo-500/15 text-indigo-500 transition dark:text-indigo-300'
  const chip = 'shrink-0 rounded-full px-3 py-1 text-xs font-medium transition'
</script>

<svelte:window onkeydown={onWindowKey} />

<div class="app-shell min-h-[100dvh] min-w-[320px]">
  <!-- One cohesive, full-width, sticky frosted app bar. -->
  <header
    class="sticky top-0 z-20 border-b border-ink/10 bg-[rgb(var(--page))]/80 backdrop-blur-xl"
    style="padding-top: max(0.6rem, env(safe-area-inset-top))"
  >
    <div class="mx-auto max-w-3xl px-4 lg:max-w-5xl xl:max-w-7xl">
      <div class="flex h-14 items-center gap-2">
        {#if searching}
          <svg class="h-5 w-5 shrink-0 text-ink/40" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" aria-hidden="true">
            <circle cx="11" cy="11" r="7" /><path d="m21 21-4.3-4.3" />
          </svg>
          <input
            class="min-w-0 flex-1 bg-transparent text-base outline-none placeholder:text-ink/40"
            type="search"
            placeholder="Search devices…"
            bind:value={query}
            use:focusOnMount
            aria-label="Search devices"
          />
          <button onclick={closeSearch} class={iconBtn} aria-label="Close search">
            <svg class="h-5 w-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" aria-hidden="true"><path d="M6 6l12 12M18 6 6 18" /></svg>
          </button>
        {:else}
          <div class="grid h-9 w-9 shrink-0 place-items-center rounded-2xl bg-gradient-to-br from-indigo-500 to-fuchsia-500 shadow-lg shadow-indigo-500/30">
            <svg class="h-5 w-5 text-white" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true">
              <path d="M3 16h18" /><path d="M5 16a7 7 0 0114 0" /><path d="M12 9v7M8 12.5V16M16 12.5V16" />
            </svg>
          </div>
          <h1 class="text-lg font-semibold tracking-tight">सेतु</h1>
          <span
            class="flex items-center gap-1.5 rounded-full bg-ink/5 px-2.5 py-1 text-xs text-ink/60"
            title={statusLabel[$connection]}
          >
            <span class="relative flex h-2 w-2">
              {#if $connection === 'connecting' || $connection === 'online'}
                <span class="absolute inline-flex h-full w-full animate-ping rounded-full opacity-60 {statusDot[$connection]}"></span>
              {/if}
              <span class="relative inline-flex h-2 w-2 rounded-full {statusDot[$connection]}"></span>
            </span>
            <!-- status always visible; the precise "· Xs ago" stays for sm+ to save width on phones -->
            <span>{statusLabel[$connection]}</span>{#if agoLabel}<span class="hidden sm:inline">&nbsp;· {agoLabel}</span>{/if}
          </span>

          <div class="ml-auto flex items-center gap-1.5">
            {#if hasDevices}
              <button onclick={() => (searching = true)} class={iconBtn} aria-label="Search devices">
                <svg class="h-5 w-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" aria-hidden="true">
                  <circle cx="11" cy="11" r="7" /><path d="m21 21-4.3-4.3" />
                </svg>
              </button>
              <Scenes disabled={$connection !== 'online'} />
              <button
                onclick={() => (organizing = !organizing)}
                class={organizing ? iconBtnActive : iconBtn}
                aria-pressed={organizing}
                aria-label="Arrange &amp; group devices"
              >
                <svg class="h-5 w-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                  <path d="M8 6h13M8 12h13M8 18h13M3 6h.01M3 12h.01M3 18h.01" />
                </svg>
              </button>
            {/if}
            <button
              onclick={() => {
                tokenDraft = token
                showSettings = true
              }}
              class={iconBtn}
              aria-label="Settings"
            >
              <svg class="h-5 w-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true">
                <circle cx="12" cy="12" r="3" />
                <path d="M19.4 15a1.65 1.65 0 00.33 1.82l.06.06a2 2 0 11-2.83 2.83l-.06-.06a1.65 1.65 0 00-1.82-.33 1.65 1.65 0 00-1 1.51V21a2 2 0 11-4 0v-.09A1.65 1.65 0 009 19.4a1.65 1.65 0 00-1.82.33l-.06.06a2 2 0 11-2.83-2.83l.06-.06a1.65 1.65 0 00.33-1.82 1.65 1.65 0 00-1.51-1H3a2 2 0 110-4h.09A1.65 1.65 0 004.6 9a1.65 1.65 0 00-.33-1.82l-.06-.06a2 2 0 112.83-2.83l.06.06a1.65 1.65 0 001.82.33H9a1.65 1.65 0 001-1.51V3a2 2 0 114 0v.09a1.65 1.65 0 001 1.51 1.65 1.65 0 001.82-.33l.06-.06a2 2 0 112.83 2.83l-.06.06a1.65 1.65 0 00-.33 1.82V9a1.65 1.65 0 001.51 1H21a2 2 0 110 4h-.09a1.65 1.65 0 00-1.51 1z" />
              </svg>
            </button>
          </div>
        {/if}
      </div>

      <!-- Room filter chips: present in every mode once devices are grouped, so the
           header height never changes (no card jump on search / organize). -->
      {#if hasDevices && roomNames.length}
        <div class="-mx-4 flex gap-1.5 overflow-x-auto px-4 pb-2 [&::-webkit-scrollbar]:hidden" style="scrollbar-width:none">
          <button onclick={() => (activeRoom = '')} class="{chip} {activeRoom === '' ? 'bg-indigo-500/15 text-indigo-600 dark:text-indigo-300' : 'bg-ink/5 text-ink/60 hover:bg-ink/10'}">All</button>
          {#each roomNames as room (room)}
            <button onclick={() => (activeRoom = activeRoom === room ? '' : room)} class="{chip} {activeRoom === room ? 'bg-indigo-500/15 text-indigo-600 dark:text-indigo-300' : 'bg-ink/5 text-ink/60 hover:bg-ink/10'}">{room}</button>
          {/each}
        </div>
      {/if}
    </div>
  </header>

  <div class="mx-auto max-w-3xl px-4 pb-16 pt-4 lg:max-w-5xl xl:max-w-7xl">
    {#if needsToken}
      <div in:fade={{ duration: 200 }} class="mx-auto mt-12 w-full max-w-sm rounded-3xl border border-ink/10 bg-ink/[0.06] p-6 text-center backdrop-blur-xl">
        <div class="mx-auto mb-4 grid h-12 w-12 place-items-center rounded-2xl bg-ink/10">
          <svg class="h-6 w-6 text-ink/80" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true">
            <rect x="4" y="11" width="16" height="9" rx="2" />
            <path d="M8 11V7a4 4 0 118 0v4" />
          </svg>
        </div>
        <h2 class="text-lg font-semibold">Connect to Setu</h2>
        <p class="mt-1 text-sm text-ink/50">Enter the access token from your <code class="rounded bg-ink/10 px-1">config.yaml</code>.</p>
        <input
          class="mt-4 w-full rounded-xl border border-ink/10 bg-ink/5 px-4 py-2.5 text-center outline-none ring-indigo-400/50 focus:ring-2"
          type="password"
          placeholder="access token"
          bind:value={tokenDraft}
          onkeydown={(e) => e.key === 'Enter' && saveToken()}
        />
        <button
          onclick={saveToken}
          class="mt-3 w-full rounded-xl bg-gradient-to-r from-indigo-500 to-fuchsia-500 py-2.5 font-medium text-white shadow-lg shadow-indigo-500/30 transition hover:opacity-95 active:scale-[0.99]"
        >
          Connect
        </button>
      </div>
    {:else if $devices.length === 0 && $connection === 'connecting' && !stalled}
      <!-- Cold start with no cache: a calm pulsing logo instead of an empty flash. -->
      <div in:fade={{ duration: 200 }} class="flex flex-1 flex-col items-center justify-center py-24 text-center">
        <div class="relative grid h-16 w-16 place-items-center rounded-3xl bg-gradient-to-br from-indigo-500 to-fuchsia-500 shadow-lg shadow-indigo-500/30">
          <span class="absolute inset-0 animate-ping rounded-3xl bg-indigo-500/40"></span>
          <svg class="relative h-8 w-8 text-white" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true">
            <path d="M3 16h18" /><path d="M5 16a7 7 0 0114 0" /><path d="M12 9v7M8 12.5V16M16 12.5V16" />
          </svg>
        </div>
        <p class="mt-4 text-sm text-ink/50">Connecting…</p>
      </div>
    {:else if $devices.length === 0 && ($connection === 'offline' || $connection === 'connecting')}
      <!-- Can't reach the server and nothing cached: say so, don't show blank. -->
      <div in:fade={{ duration: 200 }} class="flex flex-1 flex-col items-center justify-center py-20 text-center">
        <div class="grid h-16 w-16 place-items-center rounded-3xl bg-rose-500/10 ring-1 ring-rose-500/20">
          <svg class="h-8 w-8 text-rose-500 dark:text-rose-300" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <path d="M1 1l22 22" /><path d="M16.7 11.1A6.6 6.6 0 0121 12.5M5 12.5a6.6 6.6 0 014.4-1.9M2 8.8A11 11 0 016.2 6.7M22 8.8a11 11 0 00-6.3-2.6 11 11 0 00-3 .1M8.5 15.9a3.5 3.5 0 014.7.3" /><path d="M12 20h.01" />
          </svg>
        </div>
        <h2 class="mt-4 text-lg font-semibold">Can’t reach Setu</h2>
        <p class="mt-1.5 max-w-xs text-sm leading-relaxed text-ink/50">
          {$lastError || 'The Setu server isn’t responding. Check that it’s running and that you’re on the same network.'}
        </p>
        <button
          onclick={resume}
          class="mt-4 rounded-full bg-gradient-to-r from-indigo-500 to-fuchsia-500 px-5 py-2 text-sm font-medium text-white shadow-lg shadow-indigo-500/30 transition hover:opacity-95 active:scale-[0.99]"
        >
          Retry
        </button>
      </div>
    {:else if $devices.length === 0}
      <div in:fade={{ duration: 200 }} class="flex flex-1 flex-col items-center justify-center py-20 text-center">
        <div class="grid h-20 w-20 place-items-center rounded-3xl bg-ink/5 ring-1 ring-ink/10">
          <svg class="h-10 w-10 text-ink/40" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" aria-hidden="true">
            <path d="M3 16h18" />
            <path d="M5 16a7 7 0 0114 0" />
            <path d="M12 9v7M8 12.5V16M16 12.5V16" />
          </svg>
        </div>
        <h2 class="mt-5 text-xl font-semibold">No devices yet</h2>
        <p class="mt-2 max-w-xs text-sm leading-relaxed text-ink/50">
          Add one in <code class="rounded bg-ink/10 px-1">config.yaml</code> by its brand, model and MAC — then restart Setu.
        </p>
      </div>
    {:else if displayDevices.length === 0}
      <div in:fade={{ duration: 150 }} class="flex flex-col items-center justify-center py-16 text-center">
        <p class="text-sm text-ink/50">No devices match your filter.</p>
        <button
          onclick={() => {
            query = ''
            activeRoom = ''
          }}
          class="mt-3 rounded-full bg-ink/5 px-4 py-1.5 text-xs font-medium text-ink/70 transition hover:bg-ink/10"
        >
          Clear filters
        </button>
      </div>
    {:else}
      <!-- Phones: one full-width card per row. From sm up, cards take a FIXED
           320px width and only the column *count* changes — leftover space is
           centered margin, so a card never stretches or shrinks as the desktop
           window resizes. The page also has a 320px min-width so it can't
           collapse below a usable size. -->
      <main class="grid grid-cols-1 gap-4 sm:justify-center sm:[grid-template-columns:repeat(auto-fit,minmax(min(320px,100%),320px))]">
        {#each displayDevices as device, i (device.id)}
          <div
            data-devid={device.id}
            class="rounded-3xl {overId === device.id ? 'ring-2 ring-indigo-400/80' : ''}"
            style={dragStyle(device.id)}
            in:fly={{ y: 16, duration: 280, delay: Math.min(i * 45, 270) }}
            animate:flip={{ duration: 220 }}
          >
            {#if organizing}
              <div class="mb-2 flex items-center gap-2">
                <button
                  type="button"
                  onpointerdown={(e) => startDrag(e, device.id)}
                  aria-label={`Drag to reorder ${device.name || device.id}`}
                  class="grid h-9 w-9 shrink-0 cursor-grab touch-none place-items-center rounded-xl bg-ink/10 text-ink/50 transition hover:bg-ink/15 hover:text-ink/70 active:cursor-grabbing"
                >
                  <svg class="h-5 w-5" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
                    <circle cx="9" cy="6" r="1.6" /><circle cx="15" cy="6" r="1.6" />
                    <circle cx="9" cy="12" r="1.6" /><circle cx="15" cy="12" r="1.6" />
                    <circle cx="9" cy="18" r="1.6" /><circle cx="15" cy="18" r="1.6" />
                  </svg>
                </button>
                <input
                  list="setu-rooms"
                  class="h-9 min-w-0 flex-1 rounded-xl border border-ink/10 bg-ink/5 px-3 text-sm text-ink/80 outline-none ring-indigo-400/50 placeholder:text-ink/40 focus:ring-2"
                  type="text"
                  maxlength="24"
                  placeholder="Room (e.g. Living room)"
                  value={$rooms[device.id] ?? ''}
                  onchange={(e) => setRoom(device.id, e.currentTarget.value.trim())}
                  aria-label={`Room for ${device.name || device.id}`}
                />
              </div>
            {/if}
            <DeviceCard {device} />
          </div>
        {/each}
      </main>
      <datalist id="setu-rooms">
        {#each roomNames as room (room)}<option value={room}></option>{/each}
      </datalist>
    {/if}
  </div>
</div>

{#if $lastError && !needsToken}
  <div
    in:fly={{ y: 20, duration: 200 }}
    out:fade
    class="fixed inset-x-0 z-20 mx-auto w-fit max-w-[90vw] rounded-full border border-rose-500/30 bg-rose-500/15 px-4 py-2 text-sm text-rose-700 backdrop-blur-md dark:border-rose-400/30 dark:bg-rose-500/20 dark:text-rose-100"
    style="bottom: calc(env(safe-area-inset-bottom, 0px) + 1.5rem)"
  >
    {$lastError}
  </div>
{/if}

{#if showSettings}
  <!-- svelte-ignore a11y_click_events_have_key_events -->
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <div
    class="fixed inset-0 z-30 grid place-items-center bg-black/50 p-4 backdrop-blur-sm"
    transition:fade={{ duration: 150 }}
    onclick={(e) => e.target === e.currentTarget && (showSettings = false)}
  >
    <div
      class="w-full max-w-sm rounded-3xl border border-ink/10 bg-panel p-6 shadow-2xl"
      role="dialog"
      aria-modal="true"
      aria-label="Settings"
    >
      <h2 class="text-lg font-semibold">Settings</h2>
      <label class="mt-4 block text-sm text-ink/60" for="token-input">Access token</label>
      <input
        id="token-input"
        class="mt-1.5 w-full rounded-xl border border-ink/10 bg-ink/5 px-4 py-2.5 outline-none ring-indigo-400/50 focus:ring-2"
        type="password"
        placeholder="access token"
        bind:value={tokenDraft}
        onkeydown={(e) => e.key === 'Enter' && saveToken()}
      />

      <span class="mt-4 block text-sm text-ink/60">Theme</span>
      <div class="mt-1.5 grid grid-cols-3 gap-1 rounded-xl bg-ink/5 p-1">
        {#each themes as t (t)}
          <button
            onclick={() => chooseTheme(t)}
            aria-pressed={themeChoice === t}
            class="rounded-lg py-2 text-sm font-medium capitalize transition
                   {themeChoice === t ? 'bg-panel text-ink shadow-sm' : 'text-ink/55 hover:text-ink/80'}"
          >
            {t}
          </button>
        {/each}
      </div>

      <div class="mt-5 flex gap-2">
        <button
          onclick={() => (showSettings = false)}
          class="flex-1 rounded-xl bg-ink/5 py-2.5 font-medium text-ink/70 transition hover:bg-ink/10"
        >
          Cancel
        </button>
        <button
          onclick={saveToken}
          class="flex-1 rounded-xl bg-gradient-to-r from-indigo-500 to-fuchsia-500 py-2.5 font-medium text-white shadow-lg shadow-indigo-500/30 transition hover:opacity-95"
        >
          Save
        </button>
      </div>

      <div class="mt-4 border-t border-ink/10 pt-4">
        <button
          onclick={() => (confirmReset ? resetApp() : (confirmReset = true))}
          class="w-full rounded-xl py-2.5 text-sm font-medium transition
                 {confirmReset
            ? 'bg-rose-500 text-white hover:bg-rose-600'
            : 'bg-rose-500/10 text-rose-600 hover:bg-rose-500/20 dark:text-rose-300'}"
        >
          {confirmReset ? 'Tap again to reset everything' : 'Reset app'}
        </button>
        <p class="mt-1.5 text-xs leading-relaxed text-ink/40">
          Clears scenes, rooms, favourites, layout and theme on this device. Your access token stays, so you won’t need to sign in again.
        </p>
      </div>
    </div>
  </div>
{/if}
