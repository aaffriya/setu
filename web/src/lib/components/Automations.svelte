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
    type AutomationComparison,
    type AutomationMetric,
    type AutomationRule,
    type AutomationSnapshot,
    type AutomationState,
    type Color,
    type Device,
  } from '../api'
  import { haptics } from '../haptics'
  import { trapFocus } from '../focus-trap'

  // canModify is what the signed-in account may do. An account that may only
  // control its devices still sees its automations and can run them by hand —
  // that is the same power it already has — but the editing paths are hidden
  // rather than offered and then refused by the server.
  //
  // isAdmin gates only the installation-wide pause: it stops rules a restricted
  // account was never shown, so the server keeps whatever the administrator set
  // and this hides the switch rather than offering a refused one.
  let {
    disabled = false,
    canModify = true,
    isAdmin = true,
    onmodalchange = () => {},
  }: {
    disabled?: boolean
    canModify?: boolean
    isAdmin?: boolean
    onmodalchange?: (open: boolean) => void
  } = $props()
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

  // Keep the persisted values compatible with JavaScript's Sunday=0 numbering,
  // but present the week in the more familiar Monday-first order.
  const weekdays = [
    { value: 1, label: 'Mon' },
    { value: 2, label: 'Tue' },
    { value: 3, label: 'Wed' },
    { value: 4, label: 'Thu' },
    { value: 5, label: 'Fri' },
    { value: 6, label: 'Sat' },
    { value: 0, label: 'Sun' },
  ]
  const AUTOMATION_TARGET = '@automation'

  // The trigger kinds, in the order they appear as tabs. Presence needs a host
  // that can read its neighbour table; where it cannot, the server refuses such
  // a rule, so the tab is disabled rather than offered and then rejected.
  type TriggerKind =
    | 'schedule'
    | 'device_state'
    | 'device_offline'
    | 'device_online'
    | 'presence'
    | 'webhook'
  const triggerKinds: Array<{ value: TriggerKind; label: string }> = [
    { value: 'schedule', label: 'Time' },
    { value: 'device_state', label: 'Device' },
    { value: 'device_offline', label: 'Offline' },
    { value: 'device_online', label: 'Online' },
    { value: 'presence', label: 'Presence' },
    { value: 'webhook', label: 'Webhook' },
  ]
  let hasPresence = $derived(snapshot?.presence !== false)

  // The numbers a device trigger can watch, and the capability each one needs.
  // "power" is the on/off edge and is what an absent metric means.
  const metrics: Array<{ value: AutomationMetric; label: string; capability: string }> = [
    { value: 'power', label: 'Power', capability: 'switch' },
    { value: 'brightness', label: 'Brightness', capability: 'brightness' },
    { value: 'speed', label: 'Fan speed', capability: 'speed' },
    { value: 'volume', label: 'Volume', capability: 'volume' },
    { value: 'color_temp', label: 'White temperature', capability: 'color_temp' },
    { value: 'timer_hours', label: 'Timer hours', capability: 'timer' },
  ]
  const comparisons: Array<{ value: AutomationComparison; label: string }> = [
    { value: 'above', label: 'rises above' },
    { value: 'below', label: 'falls below' },
    { value: 'equals', label: 'reaches' },
  ]
  const onlineComparisons: Array<{
    value: AutomationComparison
    label: string
    symbol: string
  }> = [
    { value: 'below', label: 'less than', symbol: '<' },
    { value: 'equals', label: 'exactly', symbol: '=' },
    { value: 'above', label: 'more than', symbol: '>' },
  ]

  let switchDevices = $derived($devices.filter((device) => device.capabilities.includes('switch')))
  // Only a device Setu can read back is ever online/offline in a sense a rule
  // can act on. This is driver truth, not a capability-name heuristic.
  let reachableDevices = $derived($devices.filter((device) => device.reports_reachability))
  let actionDevices = $derived($devices.filter((device) => actionOptions(device).length > 0))
  let callableRules = $derived(
    (snapshot?.items ?? []).filter((rule) => rule.id !== draft?.id && rule.enabled),
  )
  let canAddAction = $derived(actionDevices.length > 0 || callableRules.length > 0)

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

  function firstAutomationAction(): AutomationAction {
    return {
      device_id: '',
      action: 'run_automation',
      automation_id: callableRules[0]?.id ?? '',
    }
  }

  function firstDraftAction(): AutomationAction {
    return actionDevices.length ? firstAction(actionDevices[0]) : firstAutomationAction()
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
      actions: [device ? firstAction(device) : firstAutomationAction()],
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
      await persist(
        cascadeUnavailableCallers(
          snapshot.items.filter((rule) => rule.id !== id).map(cloneRule),
        ),
      )
    } catch {
      // persist owns the message
    }
  }

  async function toggleRule(rule: AutomationRule) {
    if (!snapshot) return
    const items = cascadeUnavailableCallers(
      snapshot.items.map((item) =>
        item.id === rule.id ? { ...cloneRule(item), enabled: !item.enabled } : cloneRule(item),
      ),
    )
    try {
      await persist(items)
    } catch {
      // persist owns the message
    }
  }

  function cascadeUnavailableCallers(items: AutomationRule[]): AutomationRule[] {
    let changed = true
    while (changed) {
      changed = false
      const enabled = new Set(items.filter((item) => item.enabled).map((item) => item.id))
      for (const item of items) {
        if (
          item.enabled &&
          item.actions.some(
            (action) =>
              action.action === 'run_automation' &&
              (!action.automation_id || !enabled.has(action.automation_id)),
          )
        ) {
          item.enabled = false
          changed = true
        }
      }
    }
    return items
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

  function setTrigger(type: TriggerKind) {
    if (!draft) return
    if (type !== 'schedule') {
      for (const action of draft.actions) delete action.offset_minutes
    }
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
        device: { device_id: switchDevices[0]?.id ?? '', on: true, stable_seconds: 0, metric: 'power' },
      }
    } else if (type === 'device_offline') {
      draft.trigger = {
        type,
        offline: { device_id: reachableDevices[0]?.id ?? '', minutes: 10 },
      }
    } else if (type === 'device_online') {
      draft.trigger = {
        type,
        online: {
          device_id: reachableDevices[0]?.id ?? '',
          operator: 'above',
          minutes: 10,
        },
      }
    } else if (type === 'presence') {
      draft.trigger = { type, presence: { mac: '', present: true, stable_seconds: 300 } }
    } else {
      draft.trigger = { type, webhook: {} }
    }
  }

  // Which devices can answer the metric this trigger watches. Power devices are
  // the switchable ones; everything else needs its own capability.
  function metricDevices(metric: AutomationMetric): Device[] {
    const capability = metrics.find((item) => item.value === metric)?.capability ?? 'switch'
    return $devices.filter((device) => device.capabilities.includes(capability))
  }

  // setMetric keeps the trigger consistent: switching to a number needs a
  // comparison and a starting value, and switching back to power needs neither.
  // The watched device follows too, since not every device reports every value.
  function setMetric(metric: AutomationMetric) {
    if (!draft || draft.trigger.type !== 'device_state') return
    const trigger = draft.trigger.device
    trigger.metric = metric
    const candidates = metricDevices(metric)
    if (!candidates.some((device) => device.id === trigger.device_id)) {
      trigger.device_id = candidates[0]?.id ?? ''
    }
    if (metric === 'power') {
      delete trigger.operator
      delete trigger.value
      return
    }
    trigger.operator ??= 'above'
    trigger.value = defaultMetricValue(metric, trigger.device_id)
  }

  function defaultMetricValue(metric: AutomationMetric, deviceID: string): number {
    const device = deviceFor(deviceID)
    switch (metric) {
      case 'color_temp':
        return device?.color_temp_min ?? 2700
      case 'speed':
        return device?.speed_min ?? 1
      case 'timer_hours':
        return 1
      default:
        return 50
    }
  }

  function metricMin(trigger: { metric?: AutomationMetric; device_id: string }): number {
    const device = deviceFor(trigger.device_id)
    if (trigger.metric === 'color_temp') return device?.color_temp_min ?? 1000
    if (trigger.metric === 'speed') return device?.speed_min ?? 1
    return 0
  }

  function metricMax(trigger: { metric?: AutomationMetric; device_id: string }): number {
    const device = deviceFor(trigger.device_id)
    if (trigger.metric === 'color_temp') return device?.color_temp_max ?? 10000
    if (trigger.metric === 'speed') return device?.speed_max ?? 6
    if (trigger.metric === 'timer_hours') return 24
    return 100
  }

  // A MAC is the identity of a phone or laptop on the LAN. Accept the spellings
  // people copy from a router — colons, dashes, or nothing at all.
  function isMAC(value: string): boolean {
    return /^[0-9a-f]{12}$/.test(value.toLowerCase().replaceAll(/[:.-]/g, ''))
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
    if (!draft || !canAddAction || draft.actions.length >= 16) return
    draft.actions = [...draft.actions, firstDraftAction()]
  }

  function addActionCondition(action: AutomationAction) {
    if (!switchDevices.length || (action.when?.length ?? 0) >= 4) return
    action.when = [...(action.when ?? []), { device_id: switchDevices[0].id, on: true }]
  }

  function removeActionCondition(action: AutomationAction, index: number) {
    action.when = action.when?.filter((_, item) => item !== index)
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
    if (caps.has('speed')) out.push({ value: 'set_speed', label: 'Fan speed' })
    if (caps.has('sleep')) out.push({ value: 'set_sleep', label: 'Sleep mode' })
    if (caps.has('timer')) out.push({ value: 'set_timer', label: 'Timer' })
    if (caps.has('light')) out.push({ value: 'set_light', label: 'Light' })
    if (caps.has('volume')) out.push({ value: 'set_volume', label: 'Volume' })
    if (caps.has('app') && (device.apps?.length ?? 0) > 0) out.push({ value: 'launch_app', label: 'Launch app' })
    if (caps.has('wol')) out.push({ value: 'wake', label: 'Wake' })
    return out
  }

  function resetAction(action: AutomationAction, deviceID?: string) {
    if (deviceID !== undefined) action.device_id = deviceID
    delete action.automation_id
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
      case 'set_speed':
        action.value = deviceFor(action.device_id)?.speed_min ?? 1
        break
      case 'set_sleep':
      case 'set_light':
        action.value = true
        break
      case 'set_timer':
        // Default to the first real duration rather than 0, which would make
        // the action cancel a timer instead of setting one.
        action.value = deviceFor(action.device_id)?.timer_options?.find((h) => h > 0) ?? 1
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

  function actionTarget(action: AutomationAction): string {
    return action.action === 'run_automation' ? AUTOMATION_TARGET : action.device_id
  }

  function setActionTarget(action: AutomationAction, value: string) {
    if (value === AUTOMATION_TARGET) {
      action.device_id = ''
      action.action = 'run_automation'
      action.automation_id = callableRules[0]?.id ?? ''
      delete action.value
      return
    }
    action.device_id = value
    action.action = actionOptions(deviceFor(value))[0]?.value ?? 'on'
    resetAction(action)
  }

  function setNumber(action: AutomationAction, value: string) {
    action.value = Number(value)
  }

  function setActionOffset(action: AutomationAction, value: string) {
    action.offset_minutes = Number(value)
  }

  // A <select> hands back strings, so booleans need converting on the way in.
  function setBool(action: AutomationAction, value: string) {
    action.value = value === 'true'
  }

  // Numeric actions that take a plain number input, and the bounds each one
  // accepts. Kept as functions rather than inline ternaries because three
  // different ranges in one attribute is unreadable.
  const NUMERIC_ACTIONS = ['set_brightness', 'set_color_temp', 'set_volume', 'set_speed']

  function actionMin(action: AutomationAction): number {
    const device = deviceFor(action.device_id)
    if (action.action === 'set_color_temp') return device?.color_temp_min ?? 1000
    if (action.action === 'set_speed') return device?.speed_min ?? 1
    return 0
  }

  function actionMax(action: AutomationAction): number {
    const device = deviceFor(action.device_id)
    if (action.action === 'set_color_temp') return device?.color_temp_max ?? 10000
    if (action.action === 'set_speed') return device?.speed_max ?? 6
    return 100
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
    const ids = [
      ...(rule.conditions ?? []).map((condition) => condition.device_id),
      ...rule.actions.flatMap((action) =>
        (action.when ?? []).map((condition) => condition.device_id),
      ),
      ...rule.actions
        .filter((action) => action.action !== 'run_automation')
        .map((action) => action.device_id),
    ]
    if (rule.trigger.type === 'device_state') ids.push(rule.trigger.device.device_id)
    if (rule.trigger.type === 'device_offline') ids.push(rule.trigger.offline.device_id)
    if (rule.trigger.type === 'device_online') ids.push(rule.trigger.online.device_id)
    return [...new Set(ids.filter((id) => !available.has(id)))]
  }

  // What the trigger tab summary says under a rule's name.
  function triggerLabel(rule: AutomationRule): string {
    switch (rule.trigger.type) {
      case 'device_offline':
        return `offline ${rule.trigger.offline.minutes}m`
      case 'device_online': {
        const recovered = rule.trigger.online
        const comparison = onlineComparisons.find((item) => item.value === recovered.operator)
        return `online after ${comparison?.symbol ?? ''}${recovered.minutes}m offline`
      }
      case 'presence':
        return rule.trigger.presence.present ? 'arrives' : 'leaves'
      case 'device_state': {
        // Bound to a local: the narrowing above does not survive into the
        // closures below.
        const watched = rule.trigger.device
        const metric = watched.metric ?? 'power'
        if (metric === 'power') return watched.on ? 'turns on' : 'turns off'
        const label = metrics.find((item) => item.value === metric)?.label.toLowerCase() ?? metric
        const comparison = comparisons.find((item) => item.value === watched.operator)
        return `${label} ${comparison?.label ?? ''} ${watched.value ?? 0}`.trim()
      }
      default:
        return rule.trigger.type.replace('_', ' ')
    }
  }

  function isTimedRule(rule: AutomationRule): boolean {
    return (
      rule.trigger.type === 'schedule' &&
      rule.actions.some((action) => (action.offset_minutes ?? 0) > 0)
    )
  }

  function actionSummary(rule: AutomationRule): string {
    if (!isTimedRule(rule)) {
      return `${rule.actions.length} action${rule.actions.length === 1 ? '' : 's'}`
    }
    const steps = new Set(rule.actions.map((action) => action.offset_minutes ?? 0)).size
    return `${steps} timed step${steps === 1 ? '' : 's'}`
  }

  function runMeta(run: AutomationSnapshot['runs'][number]): string {
    const offset = (run.offset_minutes ?? 0) > 0 ? ` +${run.offset_minutes}m` : ''
    const skipped = run.results.filter((result) => result.skipped).length
    return `${run.source}${offset}${skipped ? ` · ${skipped} skipped` : ''}`
  }

  // A draft is savable when its trigger is actually complete. Each kind has one
  // field that cannot be defaulted sensibly.
  function draftIsComplete(rule: AutomationRule): boolean {
    if (!rule.name.trim() || rule.actions.length === 0) return false
    if (
      rule.actions.some(
        (action) =>
          (action.when?.length ?? 0) > 4 ||
          (action.offset_minutes ?? 0) < 0 ||
          (action.offset_minutes ?? 0) > 1439 ||
          (rule.trigger.type !== 'schedule' && (action.offset_minutes ?? 0) !== 0),
      )
    ) {
      return false
    }
    if (
      rule.trigger.type === 'schedule' &&
      rule.actions.some((action) => (action.offset_minutes ?? 0) > 0) &&
      !rule.actions.some((action) => (action.offset_minutes ?? 0) === 0)
    ) {
      return false
    }
    switch (rule.trigger.type) {
      case 'schedule':
        return rule.trigger.schedule.weekdays.length > 0
      case 'device_state':
        return rule.trigger.device.device_id !== ''
      case 'device_offline':
        return rule.trigger.offline.device_id !== '' && rule.trigger.offline.minutes >= 1
      case 'device_online': {
        const recovered = rule.trigger.online
        return (
          recovered.device_id !== '' &&
          recovered.minutes >= 1 &&
          recovered.minutes <= 1440 &&
          onlineComparisons.some((item) => item.value === recovered.operator)
        )
      }
      case 'presence':
        return isMAC(rule.trigger.presence.mac)
      default:
        return true
    }
  }

  function unavailableAutomations(rule: AutomationRule): string[] {
    const available = new Set(
      snapshot?.items.filter((item) => item.enabled).map((item) => item.id) ?? [],
    )
    return [
      ...new Set(
        rule.actions
          .filter((action) => action.action === 'run_automation')
          .map((action) => action.automation_id ?? '')
          .filter((id) => id && !available.has(id)),
      ),
    ]
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
  <div class="fixed inset-0 z-40 grid place-items-center overflow-hidden overscroll-none bg-black/50 p-3 backdrop-blur-sm" transition:fade={{ duration: 150 }} onclick={(event) => event.target === event.currentTarget && (open = false)}>
    <div class="flex max-h-[92dvh] min-w-0 w-full max-w-lg flex-col overflow-hidden rounded-3xl border border-ink/10 bg-panel p-4 shadow-2xl min-[360px]:p-5" role="dialog" aria-modal="true" aria-label="Automations" tabindex="-1" use:trapFocus>
      <div class="flex items-center gap-2">
        {#if draft}
          <button type="button" onclick={() => (draft = null)} class="grid h-8 w-8 shrink-0 place-items-center rounded-full bg-ink/5 text-ink/65" aria-label="Back to automations">←</button>
        {/if}
        <div class="min-w-0 flex-1">
          <h2 class="truncate text-lg font-semibold">{draft ? (snapshot?.items.some((rule) => rule.id === draft?.id) ? 'Edit automation' : 'New automation') : 'Automations'}</h2>
          <p class="truncate text-xs text-ink/45">Schedules, device relations and incoming webhooks.</p>
        </div>
        <button type="button" onclick={() => (open = false)} class="grid h-8 w-8 shrink-0 place-items-center rounded-full bg-ink/5 text-ink/60" aria-label="Close automations">×</button>
      </div>

      {#if loading}
        <div class="grid min-h-40 place-items-center text-sm text-ink/45">Loading…</div>
      {:else if draft}
        <div class="mt-4 min-h-0 min-w-0 flex-1 space-y-4 overflow-x-hidden overflow-y-auto overscroll-contain px-0.5 pb-0.5">
          <label class="block text-xs text-ink/55">Name
            <input bind:value={draft.name} maxlength="64" placeholder="Evening lights" class="mt-1 w-full rounded-xl border border-ink/10 bg-ink/5 px-3 py-2 text-sm outline-none ring-indigo-400/50 focus:ring-2" />
          </label>

          <div>
            <span class="text-xs text-ink/55">Trigger</span>
            <div class="mt-1 grid grid-cols-3 gap-1 rounded-xl bg-ink/5 p-1 sm:grid-cols-6">
              {#each triggerKinds as kind (kind.value)}
                <button
                  type="button"
                  onclick={() => setTrigger(kind.value)}
                  disabled={kind.value === 'presence' && !hasPresence}
                  title={kind.value === 'presence' && !hasPresence ? 'This host cannot read its LAN neighbour table.' : undefined}
                  class="rounded-lg py-2 text-[11px] font-medium disabled:opacity-30 {draft.trigger.type === kind.value ? 'bg-panel shadow-sm' : 'text-ink/50'}"
                >
                  {kind.label}
                </button>
              {/each}
            </div>
          </div>

          {#if draft.trigger.type === 'schedule'}
            <div class="rounded-xl bg-ink/[0.03] p-3">
              <input type="time" bind:value={draft.trigger.schedule.time} class="w-full rounded-lg border border-ink/10 bg-ink/5 px-3 py-2 text-sm" />
              <div class="mt-2 flex flex-wrap gap-1">
                {#each weekdays as day (day.value)}
                  <button type="button" onclick={() => toggleDay(day.value)} class="rounded-full px-2 py-1 text-[11px] font-medium {draft.trigger.schedule.weekdays.includes(day.value) ? 'bg-indigo-500 text-white' : 'bg-ink/5 text-ink/50'}">{day.label}</button>
                {/each}
              </div>
              <p class="mt-2 text-[11px] text-ink/40">Uses this browser’s current UTC offset. Update the rule if daylight-saving offset changes.</p>
            </div>
          {:else if draft.trigger.type === 'device_state'}
            {@const watched = draft.trigger.device}
            {@const metric = watched.metric ?? 'power'}
            <div class="grid grid-cols-2 gap-2 rounded-xl bg-ink/[0.03] p-3">
              <select value={metric} onchange={(event) => setMetric(event.currentTarget.value as AutomationMetric)} aria-label="Value to watch" class="rounded-lg border border-ink/10 bg-ink/5 px-2 py-2 text-sm">
                {#each metrics as item (item.value)}
                  <option value={item.value} disabled={metricDevices(item.value).length === 0}>{item.label}</option>
                {/each}
              </select>
              <select bind:value={watched.device_id} aria-label="Device to watch" class="rounded-lg border border-ink/10 bg-ink/5 px-2 py-2 text-sm">
                {#each metricDevices(metric) as device (device.id)}<option value={device.id}>{device.name || device.id}</option>{/each}
              </select>
              {#if metric === 'power'}
                <select bind:value={watched.on} class="col-span-2 rounded-lg border border-ink/10 bg-ink/5 px-2 py-2 text-sm"><option value={true}>Turns on</option><option value={false}>Turns off</option></select>
              {:else}
                <select bind:value={watched.operator} aria-label="Comparison" class="rounded-lg border border-ink/10 bg-ink/5 px-2 py-2 text-sm">
                  {#each comparisons as comparison (comparison.value)}<option value={comparison.value}>{comparison.label}</option>{/each}
                </select>
                <input type="number" bind:value={watched.value} min={metricMin(watched)} max={metricMax(watched)} aria-label="Compared value" class="rounded-lg border border-ink/10 bg-ink/5 px-2 py-2 text-sm" />
                <p class="col-span-2 text-[11px] leading-relaxed text-ink/40">
                  Runs on the crossing, not while the value stays there — and never while the device is unreachable.
                </p>
              {/if}
              <label class="col-span-2 text-[11px] text-ink/45">Must stay changed for
                <input type="number" min="0" max="300" bind:value={watched.stable_seconds} class="ml-2 w-20 rounded-lg border border-ink/10 bg-ink/5 px-2 py-1 text-xs" /> seconds
              </label>
            </div>
          {:else if draft.trigger.type === 'device_offline'}
            <div class="grid grid-cols-2 gap-2 rounded-xl bg-ink/[0.03] p-3">
              <select bind:value={draft.trigger.offline.device_id} aria-label="Device to watch" class="rounded-lg border border-ink/10 bg-ink/5 px-2 py-2 text-sm">
                {#each reachableDevices as device (device.id)}<option value={device.id}>{device.name || device.id}</option>{/each}
              </select>
              <label class="flex items-center gap-2 text-[11px] text-ink/45">
                <input type="number" min="1" max="1440" bind:value={draft.trigger.offline.minutes} class="w-20 rounded-lg border border-ink/10 bg-ink/5 px-2 py-2 text-sm" />
                minutes
              </label>
              <p class="col-span-2 text-[11px] leading-relaxed text-ink/40">
                Runs once when the device has been unreachable that long, and arms again only after it is seen back on the network.
              </p>
            </div>
          {:else if draft.trigger.type === 'device_online'}
            <div class="grid grid-cols-2 gap-2 rounded-xl bg-ink/[0.03] p-3">
              <select bind:value={draft.trigger.online.device_id} aria-label="Device to watch" class="col-span-2 rounded-lg border border-ink/10 bg-ink/5 px-2 py-2 text-sm">
                {#each reachableDevices as device (device.id)}<option value={device.id}>{device.name || device.id}</option>{/each}
              </select>
              <select bind:value={draft.trigger.online.operator} aria-label="Recovery offline-duration comparison" class="rounded-lg border border-ink/10 bg-ink/5 px-2 py-2 text-sm">
                {#each onlineComparisons as comparison (comparison.value)}
                  <option value={comparison.value}>{comparison.label}</option>
                {/each}
              </select>
              <label class="flex items-center gap-2 text-[11px] text-ink/45">
                <input type="number" min="1" max="1440" bind:value={draft.trigger.online.minutes} class="w-20 rounded-lg border border-ink/10 bg-ink/5 px-2 py-2 text-sm" />
                minutes
              </label>
              <p class="col-span-2 text-[11px] leading-relaxed text-ink/40">
                Runs once when the device returns. Duration uses completed minutes, so exactly 10 means 10:00–10:59 offline.
              </p>
            </div>
          {:else if draft.trigger.type === 'presence'}
            <div class="grid grid-cols-2 gap-2 rounded-xl bg-ink/[0.03] p-3">
              <input
                bind:value={draft.trigger.presence.mac}
                placeholder="a4:83:e7:11:22:33"
                autocapitalize="none"
                autocorrect="off"
                spellcheck="false"
                aria-label="MAC address to watch"
                class="col-span-2 rounded-lg border border-ink/10 bg-ink/5 px-3 py-2 font-mono text-sm outline-none ring-indigo-400/50 focus:ring-2"
              />
              <select bind:value={draft.trigger.presence.present} class="rounded-lg border border-ink/10 bg-ink/5 px-2 py-2 text-sm"><option value={true}>Joins the network</option><option value={false}>Leaves the network</option></select>
              <label class="flex items-center gap-2 text-[11px] text-ink/45">
                <input type="number" min="0" max="900" bind:value={draft.trigger.presence.stable_seconds} class="w-20 rounded-lg border border-ink/10 bg-ink/5 px-2 py-2 text-sm" />
                seconds
              </label>
              <p class="col-span-2 text-[11px] leading-relaxed text-ink/40">
                The MAC of a phone or laptop — no device has to be added for it. Presence is approximate: an entry can linger after a device leaves and can vanish while a phone sleeps, so keep several minutes of settle time, especially for “leaves”.
              </p>
              {#if draft.trigger.presence.mac && !isMAC(draft.trigger.presence.mac)}
                <p class="col-span-2 text-[11px] text-rose-600 dark:text-rose-300">That is not a MAC address.</p>
              {/if}
            </div>
          {:else}
            <div class="rounded-xl bg-indigo-500/10 p-3 text-xs leading-relaxed text-ink/60">
              Saving creates a separate high-entropy token. External calls can trigger only this automation’s predefined actions.
            </div>
          {/if}

          <div>
            <div class="flex items-center justify-between"><span class="text-xs text-ink/55">{draft.trigger.type === 'schedule' ? 'Step conditions (checked each time)' : 'Conditions (all must match)'}</span><button type="button" onclick={addCondition} disabled={(draft.conditions?.length ?? 0) >= 4 || !switchDevices.length} class="text-xs font-medium text-indigo-500 disabled:opacity-40">+ Add</button></div>
            {#each draft.conditions ?? [] as condition, index (index)}
              <div class="mt-1 flex gap-1">
                <select bind:value={condition.device_id} class="min-w-0 flex-1 rounded-lg border border-ink/10 bg-ink/5 px-2 py-1.5 text-xs">{#each switchDevices as device (device.id)}<option value={device.id}>{device.name || device.id}</option>{/each}</select>
                <select bind:value={condition.on} class="rounded-lg border border-ink/10 bg-ink/5 px-2 py-1.5 text-xs"><option value={true}>is on</option><option value={false}>is off</option></select>
                <button type="button" onclick={() => draft && (draft.conditions = draft.conditions?.filter((_, item) => item !== index))} class="h-8 w-8 rounded-lg text-rose-500">×</button>
              </div>
            {/each}
          </div>

          <div>
            <div class="flex items-center justify-between"><span class="text-xs text-ink/55">{draft.trigger.type === 'schedule' ? 'Timed steps' : 'Actions (in order)'}</span><button type="button" onclick={addAction} disabled={draft.actions.length >= 16 || !canAddAction} class="text-xs font-medium text-indigo-500 disabled:opacity-40">+ Add</button></div>
            {#if draft.trigger.type === 'schedule'}<p class="mt-1 text-[11px] text-ink/40">Offset 0 is the start step. Later offsets use the shared clock and do not occupy a worker while waiting.</p>{/if}
            <div class="mt-1 space-y-2">
              {#each draft.actions as action, index (index)}
                <div class="rounded-xl bg-ink/[0.03] p-2">
                  <div class="flex gap-1">
                    <select value={actionTarget(action)} onchange={(event) => setActionTarget(action, event.currentTarget.value)} aria-label="Action target" class="min-w-0 flex-1 rounded-lg border border-ink/10 bg-ink/5 px-2 py-1.5 text-xs">
                      {#each actionDevices as device (device.id)}<option value={device.id}>{device.name || device.id}</option>{/each}
                      <option value={AUTOMATION_TARGET} disabled={!callableRules.length && action.action !== 'run_automation'}>Automation</option>
                    </select>
                    {#if action.action === 'run_automation'}
                      <select bind:value={action.automation_id} aria-label="Automation to run" class="min-w-0 flex-1 rounded-lg border border-ink/10 bg-ink/5 px-2 py-1.5 text-xs">
                        {#if action.automation_id && !callableRules.some((rule) => rule.id === action.automation_id)}<option value={action.automation_id}>Missing: {action.automation_id}</option>{/if}
                        {#each callableRules as rule (rule.id)}<option value={rule.id}>{rule.name}{rule.enabled ? '' : ' (disabled)'}</option>{/each}
                      </select>
                    {:else}
                      <select value={action.action} onchange={(event) => setAction(action, event.currentTarget.value)} aria-label="Device action" class="min-w-0 flex-1 rounded-lg border border-ink/10 bg-ink/5 px-2 py-1.5 text-xs">{#each actionOptions(deviceFor(action.device_id)) as option (option.value)}<option value={option.value}>{option.label}</option>{/each}</select>
                    {/if}
                    <button type="button" onclick={() => draft && (draft.actions = draft.actions.filter((_, item) => item !== index))} disabled={draft.actions.length === 1} class="h-8 w-8 rounded-lg text-rose-500 disabled:opacity-30">×</button>
                  </div>
                  {#if action.action === 'set_color'}
                    <input type="color" value={colorHex(action.value)} onchange={(event) => setColor(action, event.currentTarget.value)} class="mt-2 h-8 w-full rounded-lg bg-transparent" aria-label="Automation color" />
                  {:else if action.action === 'set_scene'}
                    <select bind:value={action.value} class="mt-2 w-full rounded-lg border border-ink/10 bg-ink/5 px-2 py-1.5 text-xs">{#each deviceFor(action.device_id)?.scenes ?? [] as scene (scene.id)}<option value={scene.id}>{scene.name}</option>{/each}</select>
                  {:else if action.action === 'launch_app'}
                    <select bind:value={action.value} class="mt-2 w-full rounded-lg border border-ink/10 bg-ink/5 px-2 py-1.5 text-xs">{#each deviceFor(action.device_id)?.apps ?? [] as app (app.id)}<option value={app.id}>{app.name}</option>{/each}</select>
                  {:else if action.action === 'set_timer'}
                    <select value={Number(action.value ?? 0)} onchange={(event) => setNumber(action, event.currentTarget.value)} aria-label="Timer duration" class="mt-2 w-full rounded-lg border border-ink/10 bg-ink/5 px-2 py-1.5 text-xs">{#each deviceFor(action.device_id)?.timer_options ?? [0] as hours (hours)}<option value={hours}>{hours === 0 ? 'Off' : `${hours} hour${hours > 1 ? 's' : ''}`}</option>{/each}</select>
                  {:else if action.action === 'set_sleep' || action.action === 'set_light'}
                    <select value={String(action.value ?? true)} onchange={(event) => setBool(action, event.currentTarget.value)} aria-label={action.action === 'set_light' ? 'Light state' : 'Sleep mode state'} class="mt-2 w-full rounded-lg border border-ink/10 bg-ink/5 px-2 py-1.5 text-xs"><option value="true">On</option><option value="false">Off</option></select>
                  {:else if NUMERIC_ACTIONS.includes(action.action)}
                    <input type="number" value={Number(action.value ?? 0)} oninput={(event) => setNumber(action, event.currentTarget.value)} min={actionMin(action)} max={actionMax(action)} class="mt-2 w-full rounded-lg border border-ink/10 bg-ink/5 px-2 py-1.5 text-xs" />
                  {/if}
                  {#if draft.trigger.type === 'schedule'}
                    <label class="mt-2 flex items-center gap-2 text-[11px] text-ink/40">
                      <span class="min-w-0 flex-1">Run after start</span>
                      <input type="number" min="0" max="1439" value={action.offset_minutes ?? 0} oninput={(event) => setActionOffset(action, event.currentTarget.value)} class="w-20 shrink-0 rounded-lg border border-ink/10 bg-ink/5 px-2 py-1" />
                      <span class="shrink-0">min</span>
                    </label>
                  {/if}
                  <div class="mt-2 rounded-lg border border-ink/5 bg-ink/[0.02] p-2">
                    <div class="flex items-center justify-between">
                      <span class="text-[11px] text-ink/40">Only if (checked before action)</span>
                      <button type="button" onclick={() => addActionCondition(action)} disabled={(action.when?.length ?? 0) >= 4 || !switchDevices.length} class="text-[11px] font-medium text-indigo-500 disabled:opacity-40">+ Add</button>
                    </div>
                    {#each action.when ?? [] as condition, conditionIndex (conditionIndex)}
                      <div class="mt-1 flex gap-1">
                        <select bind:value={condition.device_id} aria-label="Action condition device" class="min-w-0 flex-1 rounded-lg border border-ink/10 bg-ink/5 px-2 py-1.5 text-xs">{#each switchDevices as device (device.id)}<option value={device.id}>{device.name || device.id}</option>{/each}</select>
                        <select bind:value={condition.on} aria-label="Action condition state" class="rounded-lg border border-ink/10 bg-ink/5 px-2 py-1.5 text-xs"><option value={true}>is on</option><option value={false}>is off</option></select>
                        <button type="button" onclick={() => removeActionCondition(action, conditionIndex)} class="h-8 w-8 rounded-lg text-rose-500" aria-label="Remove action condition">×</button>
                      </div>
                    {/each}
                  </div>
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
          <button type="button" onclick={saveDraft} disabled={saving || !draftIsComplete(draft)} class="flex-1 rounded-xl bg-indigo-500 py-2.5 text-sm font-semibold text-white disabled:opacity-40">{saving ? 'Saving…' : 'Save'}</button>
        </div>
      {:else}
        <div class="mt-4 flex items-center gap-2">
          {#if isAdmin}
            <button type="button" onclick={togglePause} disabled={saving || !snapshot} class="rounded-xl px-3 py-2 text-xs font-medium {snapshot?.paused ? 'bg-amber-500 text-white' : 'bg-ink/5 text-ink/65'}">{snapshot?.paused ? 'Resume all' : 'Pause all'}</button>
          {:else if snapshot?.paused}
            <p class="text-xs text-amber-600 dark:text-amber-300">Paused by the administrator.</p>
          {/if}
          {#if canModify}
            <button type="button" onclick={newRule} disabled={!snapshot || !canAddAction} class="ml-auto rounded-xl bg-indigo-500 px-3 py-2 text-xs font-semibold text-white disabled:opacity-40">+ New automation</button>
          {:else if !snapshot?.paused}
            <p class="text-xs text-ink/45">You can run these by hand; only a manager can change them.</p>
          {/if}
        </div>

        <div class="mt-3 min-h-0 min-w-0 flex-1 space-y-1.5 overflow-x-hidden overflow-y-auto overscroll-contain">
          {#if !snapshot?.items.length}
            <div class="rounded-2xl bg-ink/[0.03] p-6 text-center text-sm text-ink/45">No automations yet.</div>
          {:else}
            {#each snapshot.items as rule (rule.id)}
              {@const missing = missingDevices(rule)}
              {@const unavailableAutomation = unavailableAutomations(rule)}
              <div class="min-w-0 rounded-2xl border border-ink/10 bg-ink/[0.025] p-3" in:fly={{ y: 5, duration: 120 }}>
                <div class="flex min-w-0 items-center gap-2">
                  <button type="button" onclick={() => toggleRule(rule)} disabled={!canModify || saving || missing.length > 0 || unavailableAutomation.length > 0} aria-label={rule.enabled ? `Disable ${rule.name}` : `Enable ${rule.name}`} class="h-5 w-9 shrink-0 rounded-full p-0.5 transition {rule.enabled ? 'bg-emerald-500' : 'bg-ink/15'} disabled:opacity-40"><span class="block h-4 w-4 rounded-full bg-white shadow transition {rule.enabled ? 'translate-x-4' : ''}"></span></button>
                  <button type="button" onclick={() => canModify && (draft = cloneRule(rule))} disabled={!canModify} class="min-w-0 flex-1 text-left disabled:cursor-default"><span class="block truncate text-sm font-medium">{rule.name}</span><span class="block text-[11px] text-ink/40">{triggerLabel(rule)} · {actionSummary(rule)}</span></button>
                  <button type="button" onclick={() => run(rule)} disabled={saving || snapshot?.paused || !rule.enabled || missing.length > 0 || unavailableAutomation.length > 0} class="shrink-0 rounded-lg bg-indigo-500/10 px-2 py-1.5 text-[11px] font-medium text-indigo-600 disabled:opacity-30 dark:text-indigo-300">{isTimedRule(rule) ? 'Run first step' : 'Run'}</button>
                  {#if canModify}
                    <button type="button" onclick={() => removeRule(rule.id)} disabled={saving} class="grid h-8 w-8 shrink-0 place-items-center rounded-lg text-rose-500">×</button>
                  {/if}
                </div>
                {#if missing.length}<p class="mt-1 text-[11px] text-amber-600 dark:text-amber-300">Missing device: {missing.join(', ')}. Kept disabled after restore.</p>{/if}
                {#if unavailableAutomation.length}<p class="mt-1 text-[11px] text-amber-600 dark:text-amber-300">Unavailable automation: {unavailableAutomation.join(', ')}. Enable its target first.</p>{/if}
                {#if rule.trigger.type === 'webhook' && canModify}
                  <div class="mt-2 flex min-w-0 items-center gap-2 overflow-hidden rounded-lg bg-ink/[0.03] px-2 py-1.5"><code class="min-w-0 flex-1 truncate text-[10px] text-ink/45">{webhookURL(rule.id)}</code><button type="button" onclick={() => rotate(rule)} disabled={saving} class="shrink-0 whitespace-nowrap text-[10px] font-medium text-indigo-500">New token</button></div>
                {/if}
              </div>
            {/each}
          {/if}

          {#if snapshot?.runs.length}
            <h3 class="px-1 pt-3 text-xs font-medium text-ink/50">Recent runs (memory only)</h3>
            {#each snapshot.runs.slice(0, 5) as run (run.id)}
              <div class="flex min-w-0 items-center gap-2 rounded-xl px-2 py-1.5 text-xs"><span class="h-2 w-2 shrink-0 rounded-full {run.ok ? 'bg-emerald-500' : 'bg-rose-500'}"></span><span class="min-w-0 flex-1 truncate">{run.rule_name}</span><span class="max-w-[50%] shrink-0 truncate text-[10px] text-ink/35">{runMeta(run)}</span></div>
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
