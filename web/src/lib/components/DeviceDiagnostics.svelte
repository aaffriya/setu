<script lang="ts">
  import { onDestroy, tick } from 'svelte'
  import { devices, refreshDevice } from '../store'
  import { getDiagnostics, type DeviceDiagnostics } from '../api'
  import { fade, fly } from 'svelte/transition'
  import { trapFocus } from '../focus-trap'

  let {
    disabled = false,
    onmodalchange = () => {},
  }: { disabled?: boolean; onmodalchange?: (open: boolean) => void } = $props()

  let open = $state(false)
  let loading = $state(false)
  let refreshingID = $state('')
  let records = $state<DeviceDiagnostics[]>([])
  let error = $state('')
  let dialog = $state<HTMLElement>()
  let loadGeneration = 0
  let byID = $derived(new Map(records.map((record) => [record.id, record])))

  const timeFormat = new Intl.DateTimeFormat(undefined, {
    hour: 'numeric',
    minute: '2-digit',
    second: '2-digit',
  })

  function formatTime(value: number): string {
    return value > 0 ? timeFormat.format(new Date(value)) : 'Not yet'
  }

  // Polling backs off to as much as 6h while the app is idle, so a bare clock
  // time cannot say whether a record still describes the device now — "3h ago"
  // can. The exact time stays available as the element's title.
  function formatAge(value: number, at: number): string {
    const secs = Math.max(0, Math.round((at - value) / 1000))
    if (secs < 5) return 'just now'
    if (secs < 60) return `${secs}s ago`
    const mins = Math.round(secs / 60)
    if (mins < 60) return `${mins}m ago`
    const hours = Math.round(mins / 60)
    return hours < 24 ? `${hours}h ago` : `${Math.round(hours / 24)}d ago`
  }

  // Tick only while the panel is open, so no timer runs on the happy path.
  let now = $state(Date.now())
  $effect(() => {
    if (!open) return
    now = Date.now()
    const tick = setInterval(() => (now = Date.now()), 5000)
    return () => clearInterval(tick)
  })

  async function load() {
    const generation = ++loadGeneration
    loading = true
    error = ''
    try {
      const next = await getDiagnostics()
      if (generation !== loadGeneration) return
      records = next
    } catch (reason) {
      if (generation !== loadGeneration) return
      error = reason instanceof Error ? reason.message : 'Could not load diagnostics.'
    } finally {
      if (generation === loadGeneration) loading = false
    }
  }

  function openDialog() {
    if (disabled || refreshingID) return
    open = true
    void load()
  }

  function closeDialog() {
    open = false
    loadGeneration++
    loading = false
  }

  async function refreshOne(id: string, source: HTMLButtonElement) {
    if (disabled || refreshingID || loading) return
    refreshingID = id
    try {
      await refreshDevice(id)
      if (open) await load()
    } finally {
      refreshingID = ''
      await tick()
      if (source.isConnected) source.focus()
      else if (dialog?.isConnected) dialog.focus()
    }
  }

  $effect(() => onmodalchange(open))
  onDestroy(() => {
    loadGeneration++
    onmodalchange(false)
  })

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
  disabled={disabled || Boolean(refreshingID)}
  class="flex w-full items-center gap-3 rounded-xl bg-ink/5 px-3 py-2.5 text-left transition hover:bg-ink/10 disabled:opacity-40"
>
  <span class="grid h-8 w-8 shrink-0 place-items-center rounded-lg bg-emerald-500/10 text-emerald-600 dark:text-emerald-300">
    <svg class="h-[18px] w-[18px]" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      <path d="M3 12h4l2-5 4 10 2-5h6" /><path d="M5 3h14a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2Z" />
    </svg>
  </span>
  <span class="min-w-0 flex-1">
    <span class="block text-sm font-medium text-ink/75">Device diagnostics</span>
    <span class="block text-xs text-ink/40">Status and last operation result</span>
  </span>
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
      bind:this={dialog}
      transition:fly={{ y: 6, duration: 150 }}
      class="flex max-h-[85dvh] w-full max-w-sm flex-col overflow-hidden rounded-2xl border border-ink/10 bg-panel p-4 shadow-2xl"
      role="dialog"
      aria-modal="true"
      aria-label="Device diagnostics"
      tabindex="-1"
      use:trapFocus
    >
      <div class="flex items-center gap-2">
        <h3 class="min-w-0 flex-1 text-base font-semibold">Device diagnostics</h3>
        <button
          type="button"
          onclick={closeDialog}
          class="grid h-8 w-8 place-items-center rounded-full bg-ink/5 text-ink/55"
          aria-label="Close device diagnostics"
        >×</button>
      </div>
      <p class="mt-1 text-xs leading-relaxed text-ink/45">
        Latest status only. Diagnostics stay in memory and reset when Setu restarts.
      </p>

      {#if loading && records.length === 0}
        <div class="grid min-h-28 place-items-center text-xs text-ink/45">Loading diagnostics…</div>
      {:else}
        <div class="mt-3 min-h-0 flex-1 space-y-2 overflow-y-auto overscroll-contain">
          {#each $devices as device (device.id)}
            {@const record = byID.get(device.id)}
            {@const checked = Boolean(record?.pollable && record.last_poll_at)}
            {@const checkFailed = Boolean(checked && record?.last_poll_error)}
            {@const checkOK = checked && !checkFailed}
            <section class="rounded-xl border border-ink/10 bg-ink/[0.03] p-3">
              <div class="flex items-center gap-2">
                <span class="h-2.5 w-2.5 shrink-0 rounded-full {checkOK && device.state.online ? 'bg-emerald-400' : checkFailed || (checkOK && !device.state.online) ? 'bg-rose-400' : 'bg-amber-400'}"></span>
                <div class="min-w-0 flex-1">
                  <h4 class="truncate text-sm font-medium text-ink/80">{device.name || device.id}</h4>
                  <p class="truncate text-[11px] text-ink/40">{device.series || device.model}</p>
                </div>
                <span class="text-[11px] font-medium {checkOK && device.state.online ? 'text-emerald-600 dark:text-emerald-300' : checkFailed || (checkOK && !device.state.online) ? 'text-rose-600 dark:text-rose-300' : 'text-amber-600 dark:text-amber-300'}">
                  {record
                    ? !record.pollable
                      ? 'Unverified'
                      : !record.last_poll_at
                        ? 'Not checked'
                        : record.last_poll_error
                          ? 'No response'
                          : device.state.online
                            ? 'Online'
                            : 'Offline'
                    : 'Unknown'}
                </span>
              </div>

              <div class="mt-2 grid grid-cols-[auto_1fr] gap-x-2 gap-y-1 text-[11px]">
                <span class="text-ink/40">Last check</span>
                <span
                  class="min-w-0 text-right text-ink/65"
                  title={record?.last_poll_at ? formatTime(record.last_poll_at) : undefined}
                >
                  {record?.last_poll_at
                    ? `${formatAge(record.last_poll_at, now)} · ${record.last_poll_error ? 'No response' : 'OK'}`
                    : 'Not yet'}
                </span>
                <span class="text-ink/40">Last command</span>
                <span
                  class="min-w-0 truncate text-right text-ink/65"
                  title={record?.last_command_at ? formatTime(record.last_command_at) : undefined}
                >
                  {record?.last_command_at
                    ? `${record.last_command_action.replaceAll('_', ' ')} · ${record.last_command_error ? 'Failed' : 'OK'} · ${formatAge(record.last_command_at, now)}`
                    : 'Not yet'}
                </span>
              </div>

              {#if record?.last_poll_error}
                <p class="mt-2 break-words rounded-lg bg-rose-500/10 px-2 py-1.5 text-[11px] text-rose-700 dark:text-rose-300">
                  {record.last_poll_error}
                </p>
              {/if}
              {#if record?.last_command_error}
                <p class="mt-1 break-words rounded-lg bg-rose-500/10 px-2 py-1.5 text-[11px] text-rose-700 dark:text-rose-300">
                  {record.last_command_error}
                </p>
              {/if}

              {#if !record}
                <p class="mt-2 text-center text-[11px] text-ink/35">Status details are unavailable.</p>
              {:else if record.pollable}
                <button
                  type="button"
                  onclick={(event) => refreshOne(device.id, event.currentTarget)}
                  disabled={disabled || Boolean(refreshingID) || loading}
                  class="mt-2 inline-flex w-full items-center justify-center gap-1.5 rounded-lg bg-ink/5 py-2 text-xs font-medium text-ink/70 transition hover:bg-ink/10 disabled:opacity-40"
                >
                  {#if refreshingID === device.id}
                    <svg class="h-3.5 w-3.5 animate-spin motion-reduce:animate-none" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M20 11a8 8 0 10-2.3 5.7" /><path d="M20 4v7h-7" /></svg>
                    Refreshing…
                  {:else}
                    Refresh this device
                  {/if}
                </button>
              {:else}
                <p class="mt-2 text-center text-[11px] text-ink/35">Status refresh is not supported by this device.</p>
              {/if}
            </section>
          {/each}
        </div>
      {/if}

      {#if error}
        <div class="mt-2 flex items-center gap-2 rounded-lg bg-rose-500/10 px-2.5 py-2 text-xs text-rose-700 dark:text-rose-300" role="alert">
          <span class="min-w-0 flex-1">{error}</span>
          <button type="button" onclick={load} disabled={disabled || loading} class="shrink-0 font-semibold">Retry</button>
        </div>
      {/if}
    </div>
  </div>
{/if}
