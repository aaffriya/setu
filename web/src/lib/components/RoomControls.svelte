<script lang="ts">
  import { onDestroy, tick } from 'svelte'
  import { devices, rooms, command } from '../store'
  import { haptics } from '../haptics'
  import { fade, fly } from 'svelte/transition'
  import { trapFocus } from '../focus-trap'

  let {
    disabled = false,
    onmodalchange = () => {},
  }: { disabled?: boolean; onmodalchange?: (open: boolean) => void } = $props()

  let open = $state(false)
  let selectedRoom = $state('')
  let picks = $state<Record<string, boolean>>({})
  let busy = $state(false)
  let result = $state('')
  let resultOK = $state(false)

  const maxConcurrentCommands = 4

  let roomNames = $derived.by(() => {
    const names = new Set<string>()
    for (const device of $devices) {
      const room = $rooms[device.id]
      if (room) names.add(room)
    }
    return [...names].sort()
  })
  let roomDevices = $derived($devices.filter((device) => $rooms[device.id] === selectedRoom))
  let switchableDevices = $derived(
    roomDevices.filter((device) => device.capabilities.includes('switch')),
  )
  let selectedDevices = $derived(switchableDevices.filter((device) => picks[device.id]))
  let allSelected = $derived(
    switchableDevices.length > 0 &&
      switchableDevices.every((device) => picks[device.id] === true),
  )

  function chooseRoom(room: string) {
    selectedRoom = room
    picks = Object.fromEntries(
      $devices
        .filter(
          (device) => $rooms[device.id] === room && device.capabilities.includes('switch'),
        )
        .map((device) => [device.id, true]),
    )
    result = ''
  }

  function openDialog() {
    if (disabled || busy || roomNames.length === 0) return
    haptics.tap()
    chooseRoom(roomNames[0])
    open = true
  }

  function closeDialog() {
    open = false
  }

  function toggleAll() {
    const next = !allSelected
    picks = Object.fromEntries(switchableDevices.map((device) => [device.id, next]))
    result = ''
  }

  async function run(action: 'on' | 'off', source: HTMLButtonElement) {
    if (busy || disabled || selectedDevices.length === 0) return
    haptics.tap()
    busy = true
    result = ''
    const targets = [...selectedDevices]
    const outcomes = new Array<boolean>(targets.length)
    let cursor = 0

    async function worker() {
      while (cursor < targets.length) {
        const index = cursor++
        outcomes[index] = await command(targets[index].id, action)
      }
    }

    try {
      const workers = Array.from(
        { length: Math.min(maxConcurrentCommands, targets.length) },
        () => worker(),
      )
      await Promise.all(workers)
      const succeeded = outcomes.filter(Boolean).length
      const failed = outcomes.length - succeeded
      resultOK = failed === 0
      result =
        failed === 0
          ? `${succeeded} ${succeeded === 1 ? 'device' : 'devices'} turned ${action}.`
          : `${succeeded} succeeded · ${failed} failed.`
    } finally {
      busy = false
      await tick()
      if (source.isConnected) source.focus()
    }
  }

  $effect(() => {
    if (open && !roomNames.includes(selectedRoom)) {
      if (roomNames.length) chooseRoom(roomNames[0])
      else open = false
    }
  })
  $effect(() => onmodalchange(open))
  onDestroy(() => onmodalchange(false))

  $effect(() => {
    if (!open) return
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return
      event.preventDefault()
      event.stopPropagation()
      closeDialog()
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  })
</script>

<button
  type="button"
  onclick={openDialog}
  disabled={disabled || busy || roomNames.length === 0}
  class="flex w-full items-center gap-3 rounded-xl bg-ink/5 px-3 py-2.5 text-left transition hover:bg-ink/10 disabled:opacity-40"
>
  <span class="grid h-8 w-8 shrink-0 place-items-center rounded-lg bg-indigo-500/10 text-indigo-500 dark:text-indigo-300">
    <svg class="h-[18px] w-[18px]" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      <path d="M3 10.5 12 3l9 7.5" /><path d="M5 9.5V21h14V9.5" /><path d="M9 21v-6h6v6" />
    </svg>
  </span>
  <span class="min-w-0 flex-1">
    <span class="block text-sm font-medium text-ink/75">Room controls</span>
    <span class="block text-xs text-ink/40">
      {roomNames.length ? 'Control selected devices together' : 'Assign rooms in Arrange devices first'}
    </span>
  </span>
  {#if roomNames.length}
    <span class="rounded-full bg-indigo-500 px-1.5 py-0.5 text-[10px] font-semibold text-white">{roomNames.length}</span>
  {/if}
  <span class="text-lg text-ink/30" aria-hidden="true">›</span>
</button>

{#if open}
  <!-- svelte-ignore a11y_click_events_have_key_events -->
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <div
    class="fixed inset-0 z-40 grid place-items-center bg-black/50 p-4 backdrop-blur-sm"
    transition:fade={{ duration: 150 }}
    onclick={(event) => event.target === event.currentTarget && closeDialog()}
  >
    <div
      transition:fly={{ y: 6, duration: 150 }}
      class="flex max-h-[85dvh] w-full max-w-sm flex-col overflow-hidden rounded-2xl border border-ink/10 bg-panel p-4 shadow-2xl"
      role="dialog"
      aria-modal="true"
      aria-label="Room controls"
      tabindex="-1"
      use:trapFocus
    >
      <div class="flex items-center gap-2">
        <h3 class="min-w-0 flex-1 text-base font-semibold">Room controls</h3>
        <button
          type="button"
          onclick={closeDialog}
          class="grid h-8 w-8 place-items-center rounded-full bg-ink/5 text-ink/55"
          aria-label="Close room controls"
        >×</button>
      </div>
      <p class="mt-1 text-xs leading-relaxed text-ink/45">
        Choose exactly which on/off devices receive this room action.
      </p>

      <label for="room-control-room" class="mt-3 text-xs font-medium text-ink/55">Room</label>
      <select
        id="room-control-room"
        value={selectedRoom}
        onchange={(event) => chooseRoom(event.currentTarget.value)}
        disabled={busy}
        class="mt-1 w-full rounded-xl border border-ink/10 bg-ink/5 px-3 py-2 text-sm text-ink/75 outline-none"
      >
        {#each roomNames as room (room)}<option value={room}>{room}</option>{/each}
      </select>

      <div class="mt-3 flex items-center justify-between">
        <span class="text-xs font-medium text-ink/55">Targets</span>
        {#if switchableDevices.length}
          <button
            type="button"
            onclick={toggleAll}
            disabled={busy}
            class="text-xs font-medium text-indigo-500 disabled:opacity-40 dark:text-indigo-300"
          >{allSelected ? 'Clear all' : 'Select all'}</button>
        {/if}
      </div>
      <div class="mt-1.5 min-h-0 flex-1 space-y-1 overflow-y-auto overscroll-contain rounded-xl bg-ink/[0.03] p-1.5">
        {#each roomDevices as device, index (device.id)}
          {@const switchable = device.capabilities.includes('switch')}
          <label
            for={`room-target-${index}`}
            class="flex items-center gap-2.5 rounded-lg px-2 py-2 {switchable ? 'text-ink/75' : 'text-ink/35'}"
          >
            <input
              id={`room-target-${index}`}
              type="checkbox"
              disabled={!switchable || busy}
              bind:checked={picks[device.id]}
              onchange={() => (result = '')}
              class="h-4 w-4 shrink-0 accent-indigo-500"
            />
            <span class="min-w-0 flex-1 truncate text-sm">{device.name || device.id}</span>
            {#if !switchable}<span class="shrink-0 text-[10px]">No on/off</span>{/if}
          </label>
        {:else}
          <p class="px-2 py-5 text-center text-xs text-ink/40">No devices in this room.</p>
        {/each}
      </div>

      {#if result}
        <p
          class="mt-2 rounded-lg px-2.5 py-2 text-xs {resultOK
            ? 'bg-emerald-500/10 text-emerald-700 dark:text-emerald-300'
            : 'bg-rose-500/10 text-rose-700 dark:text-rose-300'}"
          aria-live="polite"
        >{result}</p>
      {/if}

      <div class="mt-3 grid grid-cols-2 gap-2">
        <button
          type="button"
          onclick={(event) => run('off', event.currentTarget)}
          disabled={busy || disabled || selectedDevices.length === 0}
          class="rounded-xl bg-ink/5 py-2.5 text-sm font-semibold text-ink/70 transition hover:bg-ink/10 disabled:opacity-40"
        >{busy ? 'Working…' : 'Turn off'}</button>
        <button
          type="button"
          onclick={(event) => run('on', event.currentTarget)}
          disabled={busy || disabled || selectedDevices.length === 0}
          class="rounded-xl bg-gradient-to-r from-indigo-500 to-fuchsia-500 py-2.5 text-sm font-semibold text-white shadow-sm transition hover:opacity-95 disabled:opacity-40"
        >{busy ? 'Working…' : 'Turn on'}</button>
      </div>
    </div>
  </div>
{/if}
