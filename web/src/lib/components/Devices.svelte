<script lang="ts">
  import { onDestroy } from 'svelte'
  import { fade, fly } from 'svelte/transition'
  import { devices, addDevice, renameDevice, removeDevice } from '../store'
  import {
    listDeviceTypes,
    scanNetwork,
    type Device,
    type DeviceType,
    type DiscoveredDevice,
    type DiscoveryResult,
  } from '../api'
  import { trapFocus } from '../focus-trap'

  let {
    disabled = false,
    onmodalchange = () => {},
  }: { disabled?: boolean; onmodalchange?: (open: boolean) => void } = $props()

  let open = $state(false)
  let busy = $state(false)
  let message = $state('')
  let error = $state('')
  let dialog = $state<HTMLElement>()

  // Scan
  let scanning = $state(false)
  let scanned = $state(false)
  let result = $state<DiscoveryResult>({ candidates: [], errors: [] })
  let scanGeneration = 0

  // Manual add
  let manual = $state(false)
  let types = $state<DeviceType[]>([])
  let manualType = $state('')
  let manualName = $state('')
  let manualMAC = $state('')

  // Remove needs a second tap: a device carries automations and preferences.
  let confirmRemove = $state('')

  function report(reason: unknown, fallback: string) {
    error = reason instanceof Error ? reason.message : fallback
    message = ''
  }

  function note(text: string) {
    message = text
    error = ''
  }

  async function loadTypes() {
    try {
      types = await listDeviceTypes()
      if (!manualType && types.length) manualType = typeKey(types[0])
    } catch (reason) {
      report(reason, 'Could not load the device catalog.')
    }
  }

  const typeKey = (type: DeviceType) => `${type.brand}/${type.driver}`
  const typeLabel = (type: DeviceType) => `${type.brand} · ${type.label}`

  // What to call a device that has not been named yet. The server labels each
  // driver ("Tizen TV"), so nothing here has to turn a driver key into English.
  function candidateName(candidate: DiscoveredDevice): string {
    return candidate.name || [candidate.brand, candidate.label].filter(Boolean).join(' ')
  }

  // The line under a scan result: what the device says it is, or failing that
  // what the driver that would run it is called.
  function candidateDetail(candidate: DiscoveredDevice): string {
    return [candidate.brand, candidate.model || candidate.label].filter(Boolean).join(' · ')
  }

  async function scan() {
    if (scanning) return
    const generation = ++scanGeneration
    scanning = true
    error = ''
    message = ''
    try {
      const next = await scanNetwork()
      if (generation !== scanGeneration) return
      result = next
      scanned = true
    } catch (reason) {
      if (generation !== scanGeneration) return
      report(reason, 'Scan failed.')
    } finally {
      if (generation === scanGeneration) scanning = false
    }
  }

  async function addFound(candidate: DiscoveredDevice) {
    if (busy) return
    busy = true
    try {
      const added = await addDevice({
        brand: candidate.brand,
        driver: candidate.driver,
        // Keep what the scan read off the hardware. Asking the user to retype
        // a model number Setu has already been told is busywork.
        model: candidate.model,
        name: candidateName(candidate),
        mac: candidate.mac,
      })
      // Re-mark it in place instead of rescanning: the network answered once
      // already, and a second broadcast to confirm what we just did is waste.
      result = {
        ...result,
        candidates: result.candidates.map((item) =>
          item.mac === candidate.mac && item.brand === candidate.brand
            ? { ...item, configured: true, device_id: added.id }
            : item,
        ),
      }
      note(`Added ${added.name}. Rename it above if you like.`)
    } catch (reason) {
      report(reason, 'Could not add that device.')
    } finally {
      busy = false
    }
  }

  async function addManual() {
    if (busy) return
    const type = types.find((candidate) => typeKey(candidate) === manualType)
    if (!type) {
      report(new Error('Pick a device type.'), 'Pick a device type.')
      return
    }
    busy = true
    try {
      const added = await addDevice({
        brand: type.brand,
        driver: type.driver,
        name: manualName.trim() || `${type.brand} ${type.label}`,
        mac: manualMAC.trim(),
      })
      manualName = ''
      manualMAC = ''
      manual = false
      note(`Added ${added.name}.`)
    } catch (reason) {
      report(reason, 'Could not add that device.')
    } finally {
      busy = false
    }
  }

  // One field at a time, so saving the name can never carry a stale model (or
  // the other way round) while the previous save is still in flight. The input
  // is put back on failure: text that was refused must not sit there looking
  // saved.
  async function rename(
    device: Device,
    field: 'name' | 'model',
    input: HTMLInputElement,
  ) {
    const value = input.value.trim()
    if (value === (device[field] ?? '')) return
    try {
      await renameDevice(device.id, { [field]: value })
      note('Saved.')
    } catch (reason) {
      input.value = device[field] ?? ''
      report(reason, 'Could not save that change.')
    }
  }

  async function remove(device: Device) {
    if (confirmRemove !== device.id) {
      confirmRemove = device.id
      return
    }
    confirmRemove = ''
    busy = true
    try {
      await removeDevice(device.id)
      // A removed device may still be listed as a scan result; it is now new
      // again, and should be addable.
      result = {
        ...result,
        candidates: result.candidates.map((item) =>
          item.device_id === device.id ? { ...item, configured: false, device_id: undefined } : item,
        ),
      }
      note(`Removed ${device.name}.`)
    } catch (reason) {
      report(reason, 'Could not remove that device.')
    } finally {
      busy = false
    }
  }

  function openDialog() {
    if (disabled) return
    open = true
    message = ''
    error = ''
    void loadTypes()
  }

  function closeDialog() {
    open = false
    scanGeneration++
    scanning = false
    confirmRemove = ''
  }

  $effect(() => onmodalchange(open))
  onDestroy(() => {
    scanGeneration++
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

  const field =
    'min-w-0 rounded-lg border border-ink/10 bg-ink/5 px-2 py-1.5 text-sm text-ink/80 outline-none ring-indigo-400/50 placeholder:text-ink/35 focus:ring-2'
</script>

<button
  type="button"
  onclick={openDialog}
  {disabled}
  class="flex w-full items-center gap-3 rounded-xl bg-ink/5 px-3 py-2.5 text-left transition hover:bg-ink/10 disabled:opacity-40"
>
  <span class="grid h-8 w-8 shrink-0 place-items-center rounded-lg bg-sky-500/10 text-sky-600 dark:text-sky-300">
    <svg class="h-[18px] w-[18px]" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      <path d="M12 20h.01" /><path d="M8.5 16.4a5 5 0 0 1 7 0" /><path d="M5 12.9a10 10 0 0 1 14 0" /><path d="M1.5 9.4a15 15 0 0 1 21 0" />
    </svg>
  </span>
  <span class="min-w-0 flex-1">
    <span class="block text-sm font-medium text-ink/75">Devices</span>
    <span class="block text-xs text-ink/40">Scan, add, rename or remove your devices</span>
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
      aria-label="Devices"
      tabindex="-1"
      use:trapFocus
    >
      <div class="flex items-center gap-2">
        <h3 class="min-w-0 flex-1 text-base font-semibold">Devices</h3>
        <button
          type="button"
          onclick={closeDialog}
          class="grid h-8 w-8 place-items-center rounded-full bg-ink/5 text-ink/55"
          aria-label="Close devices"
        >×</button>
      </div>
      <p class="mt-1 text-xs leading-relaxed text-ink/45">
        Setu keeps your devices itself — nothing to edit on disk. Changes apply straight away.
      </p>

      <div class="mt-3 min-h-0 flex-1 space-y-4 overflow-y-auto overscroll-contain pr-0.5">
        <!-- Your devices -->
        <section>
          <h4 class="text-xs font-medium uppercase tracking-wide text-ink/40">Your devices</h4>
          {#if $devices.length === 0}
            <p class="mt-1.5 rounded-xl border border-dashed border-ink/15 px-3 py-4 text-center text-xs leading-relaxed text-ink/40">
              None yet. Scan the network below, or add one by hand.
            </p>
          {:else}
            <div class="mt-1.5 space-y-2">
              {#each $devices as device (device.id)}
                <div class="rounded-xl border border-ink/10 bg-ink/[0.03] p-2.5">
                  <div class="flex gap-1.5">
                    <input
                      class="{field} flex-1"
                      value={device.name}
                      maxlength="48"
                      aria-label={`Name for ${device.name}`}
                      onchange={(event) => rename(device, 'name', event.currentTarget)}
                    />
                    <input
                      class="{field} w-20 shrink-0"
                      value={device.model ?? ''}
                      maxlength="32"
                      placeholder="model"
                      aria-label={`Model for ${device.name}`}
                      onchange={(event) => rename(device, 'model', event.currentTarget)}
                    />
                  </div>
                  <div class="mt-1.5 flex items-center gap-2">
                    <span class="min-w-0 flex-1 truncate text-[11px] text-ink/40">
                      {device.brand} · <span class="font-mono">{device.mac}</span>
                    </span>
                    <button
                      type="button"
                      onclick={() => remove(device)}
                      disabled={busy}
                      class="shrink-0 rounded-lg px-2 py-1 text-[11px] font-medium transition disabled:opacity-40
                             {confirmRemove === device.id
                        ? 'bg-rose-500 text-white'
                        : 'bg-rose-500/10 text-rose-600 hover:bg-rose-500/20 dark:text-rose-300'}"
                    >
                      {confirmRemove === device.id ? 'Tap to confirm' : 'Remove'}
                    </button>
                  </div>
                </div>
              {/each}
            </div>
          {/if}
        </section>

        <!-- Scan -->
        <section>
          <h4 class="text-xs font-medium uppercase tracking-wide text-ink/40">Add from your network</h4>
          <button
            type="button"
            onclick={scan}
            disabled={scanning}
            class="mt-1.5 inline-flex w-full items-center justify-center gap-1.5 rounded-xl bg-gradient-to-r from-indigo-500 to-fuchsia-500 py-2.5 text-sm font-semibold text-white shadow-lg shadow-indigo-500/30 transition hover:opacity-95 disabled:opacity-50"
          >
            {#if scanning}
              <svg class="h-4 w-4 animate-spin motion-reduce:animate-none" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M20 11a8 8 0 10-2.3 5.7" /><path d="M20 4v7h-7" /></svg>
              Scanning the network…
            {:else}
              {scanned ? 'Scan again' : 'Scan network'}
            {/if}
          </button>

          {#if scanned && result.candidates.length === 0}
            <p class="mt-2 text-center text-xs leading-relaxed text-ink/40">
              Nothing answered. Check the device is powered on and that Setu runs on
              the same network segment (host networking, not a bridge).
            </p>
          {:else if result.candidates.length > 0}
            <div class="mt-2 space-y-2">
              {#each result.candidates as candidate (candidate.brand + candidate.mac)}
                <div class="flex items-start gap-2.5 rounded-xl border border-ink/10 bg-ink/[0.03] p-2.5">
                  <span class="mt-1 h-2.5 w-2.5 shrink-0 rounded-full {candidate.configured ? 'bg-emerald-400' : candidate.driver ? 'bg-indigo-400' : 'bg-amber-400'}"></span>
                  <div class="min-w-0 flex-1">
                    <p class="truncate text-sm font-medium text-ink/80">{candidateName(candidate)}</p>
                    <p class="mt-0.5 truncate text-[11px] text-ink/45">{candidateDetail(candidate)}</p>
                    <p class="mt-0.5 truncate font-mono text-[11px] text-ink/35">
                      {candidate.mac}{candidate.ip ? ` · ${candidate.ip}` : ''}
                    </p>
                  </div>
                  {#if candidate.configured}
                    <span class="shrink-0 rounded-full bg-emerald-500/10 px-2 py-0.5 text-[10px] font-medium text-emerald-600 dark:text-emerald-300">Added</span>
                  {:else if !candidate.driver}
                    <span class="shrink-0 rounded-full bg-amber-500/10 px-2 py-0.5 text-[10px] font-medium text-amber-600 dark:text-amber-300" title="Setu has no driver for this hardware yet">No driver</span>
                  {:else}
                    <button
                      type="button"
                      onclick={() => addFound(candidate)}
                      disabled={busy}
                      class="shrink-0 rounded-lg bg-indigo-500 px-2.5 py-1 text-[11px] font-semibold text-white transition hover:bg-indigo-600 disabled:opacity-40"
                    >
                      Add
                    </button>
                  {/if}
                </div>
              {/each}
            </div>
          {/if}

          {#each result.errors as reason (reason)}
            <p class="mt-2 break-words rounded-lg bg-amber-500/10 px-2.5 py-2 text-[11px] text-amber-700 dark:text-amber-300">
              {reason}
            </p>
          {/each}
        </section>

        <!-- Manual add -->
        <section>
          <button
            type="button"
            onclick={() => (manual = !manual)}
            aria-expanded={manual}
            class="flex w-full items-center gap-2 text-left"
          >
            <h4 class="min-w-0 flex-1 text-xs font-medium uppercase tracking-wide text-ink/40">Add by hand</h4>
            <span class="text-xs text-ink/40">{manual ? 'Hide' : 'Show'}</span>
          </button>
          {#if manual}
            <p class="mt-1.5 text-[11px] leading-relaxed text-ink/40">
              For hardware that answers no scan — a Wake-on-LAN target, or a device
              that was asleep. The MAC is its identity; Setu finds the IP itself.
            </p>
            <div class="mt-2 space-y-1.5">
              <select class="{field} w-full" bind:value={manualType} aria-label="Device type">
                {#each types as type (typeKey(type))}
                  <option value={typeKey(type)}>{typeLabel(type)}</option>
                {/each}
              </select>
              <input class="{field} w-full" bind:value={manualName} maxlength="48" placeholder="Name (e.g. Home NAS)" aria-label="Device name" />
              <input
                class="{field} w-full font-mono"
                bind:value={manualMAC}
                maxlength="17"
                placeholder="MAC (aa:bb:cc:dd:ee:ff)"
                aria-label="MAC address"
                autocapitalize="none"
                autocomplete="off"
                spellcheck="false"
                onkeydown={(event) => event.key === 'Enter' && addManual()}
              />
              <button
                type="button"
                onclick={addManual}
                disabled={busy || !manualMAC.trim()}
                class="w-full rounded-lg bg-indigo-500 py-2 text-xs font-semibold text-white transition hover:bg-indigo-600 disabled:opacity-40"
              >
                Add device
              </button>
            </div>
          {/if}
        </section>
      </div>

      {#if message}
        <p class="mt-2 shrink-0 text-[11px] leading-relaxed text-emerald-600 dark:text-emerald-300">{message}</p>
      {/if}
      {#if error}
        <p class="mt-2 shrink-0 break-words rounded-lg bg-rose-500/10 px-2.5 py-2 text-[11px] text-rose-700 dark:text-rose-300" role="alert">
          {error}
        </p>
      {/if}
    </div>
  </div>
{/if}
