<script lang="ts">
  import { onDestroy } from 'svelte'
  import { fade, fly } from 'svelte/transition'
  import { devices } from '../store'
  import {
    getAutomations,
    rotateAutomationToken,
    runAutomation,
    saveAutomations,
    type AutomationAction,
    type AutomationActionName,
    type AutomationRule,
    type AutomationSnapshot,
    type AutomationState,
    type Color,
    type Device,
  } from '../api'
  import { haptics } from '../haptics'
  import { trapFocus } from '../focus-trap'

  let {
    disabled = false,
    onmodalchange = () => {},
  }: { disabled?: boolean; onmodalchange?: (open: boolean) => void } = $props()
  let open = $state(false)
  let loading = $state(false)
  let saving = $state(false)
  let message = $state('')
  let snapshot = $state<AutomationSnapshot | null>(null)
  let draft = $state<AutomationRule | null>(null)
  let shownToken = $state<{ id: string; token: string } | null>(null)

  $effect(() => onmodalchange(open))
  onDestroy(() => onmodalchange(false))

  $effect(() => {
    if (!open) return
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return
      event.preventDefault()
      event.stopPropagation()
      if (draft) draft = null
      else open = false
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  })

  const weekdays = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat']
  let switchDevices = $derived($devices.filter((device) => device.capabilities.includes('switch')))
  let actionDevices = $derived($devices.filter((device) => actionOptions(device).length > 0))

  function uid(): string {
    const bytes = new Uint8Array(9)
    crypto.getRandomValues(bytes)
    return `auto_${Array.from(bytes, (byte) => byte.toString(16).padStart(2, '0')).join('')}`
  }

  function cloneRule(rule: AutomationRule): AutomationRule {
    return JSON.parse(JSON.stringify(rule)) as AutomationRule
  }

  function firstAction(device: Device | undefined): AutomationAction {
    const action = device?.capabilities.includes('switch')
      ? 'on'
      : device?.capabilities.includes('wol')
        ? 'wake'
        : actionOptions(device)[0]?.value ?? 'on'
    return { device_id: device?.id ?? '', action }
  }

  function newRule() {
    const device = actionDevices[0]
    draft = {
      id: uid(),
      name: '',
      enabled: true,
      trigger: {
        type: 'schedule',
        schedule: {
          time: new Date().toTimeString().slice(0, 5),
          weekdays: [0, 1, 2, 3, 4, 5, 6],
          utc_offset_minutes: -new Date().getTimezoneOffset(),
        },
      },
      conditions: [],
      actions: [firstAction(device)],
      cooldown_seconds: 2,
    }
    message = ''
  }

  async function show() {
    open = true
    draft = null
    shownToken = null
    await load()
  }

  async function load() {
    loading = true
    message = ''
    try {
      snapshot = await getAutomations()
    } catch (error) {
      message = error instanceof Error ? error.message : 'Could not load automations.'
    } finally {
      loading = false
    }
  }

  function editableState(items: AutomationRule[], paused = snapshot?.paused ?? false): AutomationState {
    return {
      version: 1,
      revision: snapshot?.revision ?? 0,
      paused,
      items,
    }
  }

  async function persist(items: AutomationRule[], paused = snapshot?.paused ?? false) {
    if (!snapshot || saving) return
    saving = true
    message = ''
    try {
      const update = await saveAutomations(editableState(items, paused))
      snapshot = { ...update.state, runs: snapshot.runs }
      const generated = Object.entries(update.generated_tokens ?? {})[0]
      if (generated) shownToken = { id: generated[0], token: generated[1] }
    } catch (error) {
      message = error instanceof Error ? error.message : 'Could not save automation.'
      throw error
    } finally {
      saving = false
    }
  }

  async function saveDraft() {
    if (!snapshot || !draft || !draft.name.trim() || draft.actions.length === 0) return
    draft.name = draft.name.trim()
    const index = snapshot.items.findIndex((rule) => rule.id === draft?.id)
    const items = snapshot.items.map(cloneRule)
    if (index < 0) items.push(cloneRule(draft))
    else items[index] = cloneRule(draft)
    try {
      await persist(items)
      draft = null
    } catch {
      // Error is already shown; keep the editor open for correction.
    }
  }

  async function removeRule(id: string) {
    if (!snapshot) return
    try {
      await persist(snapshot.items.filter((rule) => rule.id !== id).map(cloneRule))
    } catch {
      // persist owns the message
    }
  }

  async function toggleRule(rule: AutomationRule) {
    if (!snapshot) return
    const items = snapshot.items.map((item) =>
      item.id === rule.id ? { ...cloneRule(item), enabled: !item.enabled } : cloneRule(item),
    )
    try {
      await persist(items)
    } catch {
      // persist owns the message
    }
  }

  async function togglePause() {
    if (!snapshot) return
    try {
      await persist(snapshot.items.map(cloneRule), !snapshot.paused)
    } catch {
      // persist owns the message
    }
  }

  async function run(rule: AutomationRule) {
    message = ''
    try {
      const result = await runAutomation(rule.id)
      message = result.status === 'queued' ? `${rule.name} queued.` : `Skipped: ${result.status.replaceAll('_', ' ')}.`
      setTimeout(() => void load(), 1200)
    } catch (error) {
      message = error instanceof Error ? error.message : 'Could not run automation.'
    }
  }

  async function rotate(rule: AutomationRule) {
    saving = true
    message = ''
    try {
      const result = await rotateAutomationToken(rule.id)
      snapshot = snapshot ? { ...result.state, runs: snapshot.runs } : snapshot
      shownToken = { id: rule.id, token: result.token }
    } catch (error) {
      message = error instanceof Error ? error.message : 'Could not rotate token.'
    } finally {
      saving = false
    }
  }

  function setTrigger(type: 'schedule' | 'device_state' | 'webhook') {
    if (!draft) return
    if (type === 'schedule') {
      draft.trigger = {
        type,
        schedule: {
          time: '18:00',
          weekdays: [0, 1, 2, 3, 4, 5, 6],
          utc_offset_minutes: -new Date().getTimezoneOffset(),
        },
      }
    } else if (type === 'device_state') {
      draft.trigger = {
        type,
        device: { device_id: switchDevices[0]?.id ?? '', on: true, stable_seconds: 0 },
      }
    } else {
      draft.trigger = { type, webhook: {} }
    }
  }

  function toggleDay(day: number) {
    if (!draft || draft.trigger.type !== 'schedule') return
    const days = draft.trigger.schedule.weekdays
    draft.trigger.schedule.weekdays = days.includes(day)
      ? days.filter((item) => item !== day)
      : [...days, day].sort()
  }

  function addCondition() {
    if (!draft || !switchDevices.length || (draft.conditions?.length ?? 0) >= 4) return
    draft.conditions = [...(draft.conditions ?? []), { device_id: switchDevices[0].id, on: true }]
  }

  function addAction() {
    if (!draft || !actionDevices.length || draft.actions.length >= 16) return
    draft.actions = [...draft.actions, firstAction(actionDevices[0])]
  }

  function deviceFor(id: string): Device | undefined {
    return $devices.find((device) => device.id === id)
  }

  function actionOptions(device: Device | undefined): Array<{ value: AutomationActionName; label: string }> {
    if (!device) return []
    const caps = new Set(device.capabilities)
    const out: Array<{ value: AutomationActionName; label: string }> = []
    if (caps.has('switch')) out.push({ value: 'on', label: 'Turn on' }, { value: 'off', label: 'Turn off' })
    if (caps.has('brightness')) out.push({ value: 'set_brightness', label: 'Brightness' })
    if (caps.has('color_temp')) out.push({ value: 'set_color_temp', label: 'White temperature' })
    if (caps.has('color')) out.push({ value: 'set_color', label: 'Color' })
    if (caps.has('scene')) out.push({ value: 'set_scene', label: 'Device scene' })
    if (caps.has('volume')) out.push({ value: 'set_volume', label: 'Volume' })
    if (caps.has('app') && (device.apps?.length ?? 0) > 0) out.push({ value: 'launch_app', label: 'Launch app' })
    if (caps.has('wol')) out.push({ value: 'wake', label: 'Wake' })
    return out
  }

  function resetAction(action: AutomationAction, deviceID?: string) {
    if (deviceID !== undefined) action.device_id = deviceID
    const options = actionOptions(deviceFor(action.device_id))
    if (!options.some((option) => option.value === action.action)) action.action = options[0]?.value ?? 'on'
    switch (action.action) {
      case 'set_brightness':
      case 'set_volume':
        action.value = 50
        break
      case 'set_color_temp':
        action.value = deviceFor(action.device_id)?.color_temp_min ?? 2700
        break
      case 'set_color':
        action.value = { r: 255, g: 255, b: 255 }
        break
      case 'set_scene':
        action.value = deviceFor(action.device_id)?.scenes?.[0]?.id ?? 1
        break
      case 'launch_app':
        action.value = deviceFor(action.device_id)?.apps?.[0]?.id ?? ''
        break
      default:
        delete action.value
    }
  }

  function setAction(action: AutomationAction, value: string) {
    action.action = value as AutomationActionName
    resetAction(action)
  }

  function setNumber(action: AutomationAction, value: string) {
    action.value = Number(value)
  }

  function colorHex(value: AutomationAction['value']): string {
    const color = value as Color | undefined
    if (!color || typeof color !== 'object') return '#ffffff'
    return `#${[color.r, color.g, color.b].map((item) => Number(item).toString(16).padStart(2, '0')).join('')}`
  }

  function setColor(action: AutomationAction, value: string) {
    const number = Number.parseInt(value.slice(1), 16)
    action.value = { r: (number >> 16) & 255, g: (number >> 8) & 255, b: number & 255 }
  }

  function missingDevices(rule: AutomationRule): string[] {
    const available = new Set($devices.map((device) => device.id))
    const ids = [...(rule.conditions ?? []).map((condition) => condition.device_id), ...rule.actions.map((action) => action.device_id)]
    if (rule.trigger.type === 'device_state') ids.push(rule.trigger.device.device_id)
    return [...new Set(ids.filter((id) => !available.has(id)))]
  }

  function webhookURL(id: string): string {
    return `${location.origin}/api/automation-hooks/${encodeURIComponent(id)}`
  }

  function copyWebhook() {
    if (!shownToken) return
    const command = `curl -X POST '${webhookURL(shownToken.id)}' -H 'Authorization: Bearer ${shownToken.token}' -H 'Idempotency-Key: unique-event-id'`
    if (!navigator.clipboard) {
      message = 'Clipboard is unavailable; copy the token manually.'
      return
    }
    void navigator.clipboard.writeText(command).then(
      () => (message = 'Webhook curl command copied.'),
      () => (message = 'Clipboard permission was denied.'),
    )
  }
</script>

<button
  type="button"
  {disabled}
  onclick={() => {
    haptics.tap()
    void show()
  }}
  class="flex w-full items-center gap-3 rounded-xl bg-ink/5 px-3 py-2.5 text-left transition hover:bg-ink/10 disabled:opacity-40"
>
  <span class="grid h-8 w-8 shrink-0 place-items-center rounded-lg bg-indigo-500/10 text-indigo-500 dark:text-indigo-300">
    <svg class="h-[18px] w-[18px]" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      <path d="M13 2 5 14h6l-1 8 8-12h-6z" />
    </svg>
  </span>
  <span class="min-w-0 flex-1">
    <span class="block text-sm font-medium text-ink/75">Automations</span>
    <span class="block text-xs text-ink/40">Schedules, relations and webhooks</span>
  </span>
  <span class="text-lg text-ink/30" aria-hidden="true">›</span>
</button>

{#if open}
  <!-- svelte-ignore a11y_click_events_have_key_events -->
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <div class="fixed inset-0 z-40 grid place-items-center bg-black/50 p-3 backdrop-blur-sm" transition:fade={{ duration: 150 }} onclick={(event) => event.target === event.currentTarget && (open = false)}>
    <div class="flex max-h-[92dvh] w-full max-w-lg flex-col rounded-3xl border border-ink/10 bg-panel p-5 shadow-2xl" role="dialog" aria-modal="true" aria-label="Automations" tabindex="-1" use:trapFocus>
      <div class="flex items-center gap-2">
        {#if draft}
          <button type="button" onclick={() => (draft = null)} class="grid h-8 w-8 place-items-center rounded-full bg-ink/5 text-ink/65" aria-label="Back to automations">←</button>
        {/if}
        <div class="min-w-0 flex-1">
          <h2 class="text-lg font-semibold">{draft ? (snapshot?.items.some((rule) => rule.id === draft?.id) ? 'Edit automation' : 'New automation') : 'Automations'}</h2>
          <p class="text-xs text-ink/45">Schedules, device relations and incoming webhooks.</p>
        </div>
        <button type="button" onclick={() => (open = false)} class="grid h-8 w-8 place-items-center rounded-full bg-ink/5 text-ink/60" aria-label="Close automations">×</button>
      </div>

      {#if loading}
        <div class="grid min-h-40 place-items-center text-sm text-ink/45">Loading…</div>
      {:else if draft}
        <div class="mt-4 min-h-0 flex-1 space-y-4 overflow-y-auto pr-1">
          <label class="block text-xs text-ink/55">Name
            <input bind:value={draft.name} maxlength="64" placeholder="Evening lights" class="mt-1 w-full rounded-xl border border-ink/10 bg-ink/5 px-3 py-2 text-sm outline-none ring-indigo-400/50 focus:ring-2" />
          </label>

          <div>
            <span class="text-xs text-ink/55">Trigger</span>
            <div class="mt-1 grid grid-cols-3 gap-1 rounded-xl bg-ink/5 p-1">
              {#each ['schedule', 'device_state', 'webhook'] as type (type)}
                <button type="button" onclick={() => setTrigger(type as 'schedule' | 'device_state' | 'webhook')} class="rounded-lg py-2 text-xs font-medium {draft.trigger.type === type ? 'bg-panel shadow-sm' : 'text-ink/50'}">
                  {type === 'device_state' ? 'Device' : type[0].toUpperCase() + type.slice(1)}
                </button>
              {/each}
            </div>
          </div>

          {#if draft.trigger.type === 'schedule'}
            <div class="rounded-xl bg-ink/[0.03] p-3">
              <input type="time" bind:value={draft.trigger.schedule.time} class="w-full rounded-lg border border-ink/10 bg-ink/5 px-3 py-2 text-sm" />
              <div class="mt-2 flex flex-wrap gap-1">
                {#each weekdays as day, index (day)}
                  <button type="button" onclick={() => toggleDay(index)} class="rounded-full px-2 py-1 text-[11px] font-medium {draft.trigger.schedule.weekdays.includes(index) ? 'bg-indigo-500 text-white' : 'bg-ink/5 text-ink/50'}">{day}</button>
                {/each}
              </div>
              <p class="mt-2 text-[11px] text-ink/40">Uses this browser’s current UTC offset. Update the rule if daylight-saving offset changes.</p>
            </div>
          {:else if draft.trigger.type === 'device_state'}
            <div class="grid grid-cols-2 gap-2 rounded-xl bg-ink/[0.03] p-3">
              <select bind:value={draft.trigger.device.device_id} class="rounded-lg border border-ink/10 bg-ink/5 px-2 py-2 text-sm">
                {#each switchDevices as device (device.id)}<option value={device.id}>{device.name || device.id}</option>{/each}
              </select>
              <select bind:value={draft.trigger.device.on} class="rounded-lg border border-ink/10 bg-ink/5 px-2 py-2 text-sm"><option value={true}>Turns on</option><option value={false}>Turns off</option></select>
              <label class="col-span-2 text-[11px] text-ink/45">Must stay changed for
                <input type="number" min="0" max="300" bind:value={draft.trigger.device.stable_seconds} class="ml-2 w-20 rounded-lg border border-ink/10 bg-ink/5 px-2 py-1 text-xs" /> seconds
              </label>
            </div>
          {:else}
            <div class="rounded-xl bg-indigo-500/10 p-3 text-xs leading-relaxed text-ink/60">
              Saving creates a separate high-entropy token. External calls can trigger only this automation’s predefined actions.
            </div>
          {/if}

          <div>
            <div class="flex items-center justify-between"><span class="text-xs text-ink/55">Conditions (all must match)</span><button type="button" onclick={addCondition} disabled={(draft.conditions?.length ?? 0) >= 4 || !switchDevices.length} class="text-xs font-medium text-indigo-500 disabled:opacity-40">+ Add</button></div>
            {#each draft.conditions ?? [] as condition, index (index)}
              <div class="mt-1 flex gap-1">
                <select bind:value={condition.device_id} class="min-w-0 flex-1 rounded-lg border border-ink/10 bg-ink/5 px-2 py-1.5 text-xs">{#each switchDevices as device (device.id)}<option value={device.id}>{device.name || device.id}</option>{/each}</select>
                <select bind:value={condition.on} class="rounded-lg border border-ink/10 bg-ink/5 px-2 py-1.5 text-xs"><option value={true}>is on</option><option value={false}>is off</option></select>
                <button type="button" onclick={() => draft && (draft.conditions = draft.conditions?.filter((_, item) => item !== index))} class="h-8 w-8 rounded-lg text-rose-500">×</button>
              </div>
            {/each}
          </div>

          <div>
            <div class="flex items-center justify-between"><span class="text-xs text-ink/55">Actions (in order)</span><button type="button" onclick={addAction} disabled={draft.actions.length >= 16 || !actionDevices.length} class="text-xs font-medium text-indigo-500 disabled:opacity-40">+ Add</button></div>
            <div class="mt-1 space-y-2">
              {#each draft.actions as action, index (index)}
                <div class="rounded-xl bg-ink/[0.03] p-2">
                  <div class="flex gap-1">
                    <select value={action.device_id} onchange={(event) => resetAction(action, event.currentTarget.value)} class="min-w-0 flex-1 rounded-lg border border-ink/10 bg-ink/5 px-2 py-1.5 text-xs">{#each actionDevices as device (device.id)}<option value={device.id}>{device.name || device.id}</option>{/each}</select>
                    <select value={action.action} onchange={(event) => setAction(action, event.currentTarget.value)} class="min-w-0 flex-1 rounded-lg border border-ink/10 bg-ink/5 px-2 py-1.5 text-xs">{#each actionOptions(deviceFor(action.device_id)) as option (option.value)}<option value={option.value}>{option.label}</option>{/each}</select>
                    <button type="button" onclick={() => draft && (draft.actions = draft.actions.filter((_, item) => item !== index))} disabled={draft.actions.length === 1} class="h-8 w-8 rounded-lg text-rose-500 disabled:opacity-30">×</button>
                  </div>
                  {#if action.action === 'set_color'}
                    <input type="color" value={colorHex(action.value)} onchange={(event) => setColor(action, event.currentTarget.value)} class="mt-2 h-8 w-full rounded-lg bg-transparent" aria-label="Automation color" />
                  {:else if action.action === 'set_scene'}
                    <select value={String(action.value ?? '')} onchange={(event) => setNumber(action, event.currentTarget.value)} class="mt-2 w-full rounded-lg border border-ink/10 bg-ink/5 px-2 py-1.5 text-xs">{#each deviceFor(action.device_id)?.scenes ?? [] as scene (scene.id)}<option value={scene.id}>{scene.name}</option>{/each}</select>
                  {:else if action.action === 'launch_app'}
                    <select bind:value={action.value} class="mt-2 w-full rounded-lg border border-ink/10 bg-ink/5 px-2 py-1.5 text-xs">{#each deviceFor(action.device_id)?.apps ?? [] as app (app.id)}<option value={app.id}>{app.name}</option>{/each}</select>
                  {:else if ['set_brightness', 'set_color_temp', 'set_volume'].includes(action.action)}
                    <input type="number" value={Number(action.value ?? 0)} oninput={(event) => setNumber(action, event.currentTarget.value)} min={action.action === 'set_color_temp' ? deviceFor(action.device_id)?.color_temp_min ?? 1000 : 0} max={action.action === 'set_color_temp' ? deviceFor(action.device_id)?.color_temp_max ?? 10000 : 100} class="mt-2 w-full rounded-lg border border-ink/10 bg-ink/5 px-2 py-1.5 text-xs" />
                  {/if}
                  <label class="mt-2 flex items-center gap-2 text-[11px] text-ink/40">
                    <span class="min-w-0 flex-1">Wait before action</span>
                    <input type="number" min="0" max="60" bind:value={action.delay_seconds} class="w-16 shrink-0 rounded-lg border border-ink/10 bg-ink/5 px-2 py-1" />
                    <span class="shrink-0">s</span>
                  </label>
                </div>
              {/each}
            </div>
          </div>

          <div class="grid grid-cols-2 gap-2">
            <label class="text-xs text-ink/50">Cooldown seconds<input type="number" min="0" max="3600" bind:value={draft.cooldown_seconds} class="mt-1 w-full rounded-lg border border-ink/10 bg-ink/5 px-2 py-1.5 text-xs" /></label>
            <label class="flex items-end gap-2 rounded-lg px-2 pb-1.5 text-xs text-ink/60"><input type="checkbox" bind:checked={draft.enabled} class="h-4 w-4 accent-indigo-500" /> Enabled</label>
          </div>
        </div>
        <div class="mt-4 flex gap-2">
          <button type="button" onclick={() => (draft = null)} class="flex-1 rounded-xl bg-ink/5 py-2.5 text-sm font-medium text-ink/65">Cancel</button>
          <button type="button" onclick={saveDraft} disabled={saving || !draft.name.trim() || draft.actions.length === 0 || (draft.trigger.type === 'schedule' && draft.trigger.schedule.weekdays.length === 0)} class="flex-1 rounded-xl bg-indigo-500 py-2.5 text-sm font-semibold text-white disabled:opacity-40">{saving ? 'Saving…' : 'Save'}</button>
        </div>
      {:else}
        <div class="mt-4 flex items-center gap-2">
          <button type="button" onclick={togglePause} disabled={saving || !snapshot} class="rounded-xl px-3 py-2 text-xs font-medium {snapshot?.paused ? 'bg-amber-500 text-white' : 'bg-ink/5 text-ink/65'}">{snapshot?.paused ? 'Resume all' : 'Pause all'}</button>
          <button type="button" onclick={newRule} disabled={!snapshot || !actionDevices.length} class="ml-auto rounded-xl bg-indigo-500 px-3 py-2 text-xs font-semibold text-white disabled:opacity-40">+ New automation</button>
        </div>

        <div class="mt-3 min-h-0 flex-1 space-y-1.5 overflow-y-auto">
          {#if !snapshot?.items.length}
            <div class="rounded-2xl bg-ink/[0.03] p-6 text-center text-sm text-ink/45">No automations yet.</div>
          {:else}
            {#each snapshot.items as rule (rule.id)}
              {@const missing = missingDevices(rule)}
              <div class="rounded-2xl border border-ink/10 bg-ink/[0.025] p-3" in:fly={{ y: 5, duration: 120 }}>
                <div class="flex items-center gap-2">
                  <button type="button" onclick={() => toggleRule(rule)} disabled={saving || missing.length > 0} aria-label={rule.enabled ? `Disable ${rule.name}` : `Enable ${rule.name}`} class="h-5 w-9 rounded-full p-0.5 transition {rule.enabled ? 'bg-emerald-500' : 'bg-ink/15'} disabled:opacity-40"><span class="block h-4 w-4 rounded-full bg-white shadow transition {rule.enabled ? 'translate-x-4' : ''}"></span></button>
                  <button type="button" onclick={() => (draft = cloneRule(rule))} class="min-w-0 flex-1 text-left"><span class="block truncate text-sm font-medium">{rule.name}</span><span class="block text-[11px] text-ink/40">{rule.trigger.type.replace('_', ' ')} · {rule.actions.length} action{rule.actions.length === 1 ? '' : 's'}</span></button>
                  <button type="button" onclick={() => run(rule)} disabled={saving || snapshot?.paused || !rule.enabled || missing.length > 0} class="rounded-lg bg-indigo-500/10 px-2 py-1.5 text-[11px] font-medium text-indigo-600 disabled:opacity-30 dark:text-indigo-300">Run</button>
                  <button type="button" onclick={() => removeRule(rule.id)} disabled={saving} class="grid h-8 w-8 place-items-center rounded-lg text-rose-500">×</button>
                </div>
                {#if missing.length}<p class="mt-1 text-[11px] text-amber-600 dark:text-amber-300">Missing device: {missing.join(', ')}. Kept disabled after restore.</p>{/if}
                {#if rule.trigger.type === 'webhook'}
                  <div class="mt-2 flex items-center gap-2 rounded-lg bg-ink/[0.03] px-2 py-1.5"><code class="min-w-0 flex-1 truncate text-[10px] text-ink/45">{webhookURL(rule.id)}</code><button type="button" onclick={() => rotate(rule)} disabled={saving} class="text-[10px] font-medium text-indigo-500">New token</button></div>
                {/if}
              </div>
            {/each}
          {/if}

          {#if snapshot?.runs.length}
            <h3 class="px-1 pt-3 text-xs font-medium text-ink/50">Recent runs (memory only)</h3>
            {#each snapshot.runs.slice(0, 5) as run (run.id)}
              <div class="flex items-center gap-2 rounded-xl px-2 py-1.5 text-xs"><span class="h-2 w-2 rounded-full {run.ok ? 'bg-emerald-500' : 'bg-rose-500'}"></span><span class="min-w-0 flex-1 truncate">{run.rule_name}</span><span class="text-[10px] text-ink/35">{run.source}</span></div>
            {/each}
          {/if}
        </div>
      {/if}

      {#if shownToken}
        <div class="mt-3 rounded-xl border border-amber-500/25 bg-amber-500/10 p-3">
          <p class="text-xs font-medium text-ink/70">Copy this webhook token now—it will not be shown again.</p>
          <code class="mt-1 block break-all text-[10px] text-ink/55">{shownToken.token}</code>
          <button type="button" onclick={copyWebhook} class="mt-2 rounded-lg bg-amber-500 px-3 py-1.5 text-xs font-semibold text-white">Copy curl command</button>
        </div>
      {/if}
      {#if message}<p class="mt-3 text-xs {message.includes('queued') || message.includes('copied') ? 'text-emerald-600 dark:text-emerald-300' : 'text-rose-600 dark:text-rose-300'}">{message}</p>{/if}
    </div>
  </div>
{/if}
