// Thin fetch wrapper around the Setu JSON API, plus the shared data model. All
// calls are same-origin and carry the bearer token (kept in localStorage).

export type Color = { r: number; g: number; b: number }

export type Scene = { id: number; name: string; dynamic: boolean }

export type App = { id: string; name: string }

export type DeviceState = {
  online: boolean
  on: boolean
  brightness: number
  color: Color
  color_temp: number
  scene: number
  scene_speed: number
  // Fan controls: the current discrete step, sleep/night mode, and the running
  // auto-off timer (0 hours = none).
  speed: number
  sleep: boolean
  timer_hours: number
  timer_elapsed_mins: number
  // A secondary light that only switches (a fan's lamp). Distinct from `on`,
  // which is the device's own power.
  light: boolean
  volume: number
  muted: boolean
  // Mirrors a focused text field on the device (e.g. a TV search box): whether
  // one is focused, and its live contents as typed on the device.
  text_active: boolean
  text_value: string
}

export type Device = {
  id: string
  name: string
  brand: string
  model: string
  series?: string // optional friendly product/series name (falls back to model)
  mac: string
  capabilities: string[]
  color_temp_min?: number
  color_temp_max?: number
  speed_min?: number // hardware step range, for devices with a speed capability
  speed_max?: number
  timer_options?: number[] // hour values the device accepts (0 cancels)
  scenes?: Scene[]
  apps?: App[]
  state: DeviceState
}

export type DeviceDiagnostics = {
  id: string
  pollable: boolean
  last_poll_at: number
  last_poll_error: string
  last_command_at: number
  last_command_action: string
  last_command_error: string
}

// One device found by a network scan. `model` is empty when the brand answered
// but Setu has no driver for that hardware; `configured` means this brand
// already has a device with that MAC (then `device_id` names it).
export type DiscoveredDevice = {
  brand: string
  model: string
  series?: string
  name?: string
  mac: string
  ip?: string
  configured: boolean
  device_id?: string
}

// A (brand, model) pair this build can drive. The catalog comes from the
// server, so the manual add form can never offer something Setu cannot build.
export type DeviceType = { brand: string; model: string }

// What Setu stores for one device — the form used to add one and to back the
// list up. Identity (brand, model, mac) is fixed once added; only the labels
// can be edited.
export type DeviceSpec = {
  id?: string
  brand: string
  model: string
  series?: string
  name: string
  mac: string
}

export type DeviceList = { version: number; items: DeviceSpec[] }

export type DiscoveryResult = {
  candidates: DiscoveredDevice[]
  // One brand's scanner failing (no multicast route, a firewall) must not hide
  // what the others found, so failures arrive beside the results.
  errors: string[]
}

export type CommandAction =
  | 'on'
  | 'off'
  | 'set_brightness'
  | 'set_color'
  | 'set_color_temp'
  | 'set_scene'
  | 'set_scene_speed'
  | 'set_speed'
  | 'set_sleep'
  | 'set_timer'
  | 'set_light'
  | 'volume_up'
  | 'volume_down'
  | 'set_volume'
  | 'mute'
  | 'key'
  | 'key_down'
  | 'key_up'
  | 'send_text'
  | 'launch_app'
  | 'wake'

export type AutomationActionName =
  | 'on'
  | 'off'
  | 'set_brightness'
  | 'set_color'
  | 'set_color_temp'
  | 'set_scene'
  | 'set_scene_speed'
  | 'set_speed'
  | 'set_sleep'
  | 'set_timer'
  | 'set_light'
  | 'set_volume'
  | 'launch_app'
  | 'wake'
  | 'run_automation'

export type AutomationAction = {
  device_id: string
  automation_id?: string
  action: AutomationActionName
  value?: number | string | boolean | Color
  delay_seconds?: number
}

export type AutomationCondition = { device_id: string; on: boolean }
export type AutomationTrigger =
  | {
      type: 'schedule'
      schedule: { time: string; weekdays: number[]; utc_offset_minutes: number }
    }
  | {
      type: 'device_state'
      device: { device_id: string; on: boolean; stable_seconds?: number }
    }
  | { type: 'webhook'; webhook: { has_secret?: boolean; secret_hash?: string } }

export type AutomationRule = {
  id: string
  name: string
  enabled: boolean
  trigger: AutomationTrigger
  conditions?: AutomationCondition[]
  actions: AutomationAction[]
  cooldown_seconds?: number
}

export type AutomationState = {
  version: number
  revision: number
  paused: boolean
  items: AutomationRule[]
}

export type AutomationRun = {
  id: string
  rule_id: string
  rule_name: string
  source: string
  started_at: string
  duration_ms: number
  ok: boolean
  results: Array<{
    device_id?: string
    automation_id?: string
    action: string
    ok: boolean
    error?: string
  }>
}

export type AutomationSnapshot = AutomationState & { runs: AutomationRun[] }
export type AutomationUpdate = {
  state: AutomationState
  generated_tokens?: Record<string, string>
}

const TOKEN_KEY = 'setu.token'
const DEVICE_LIST_TIMEOUT_MS = 8000
const ACTIVITY_SIGNAL_INTERVAL_MS = 30_000
let activeDeviceListController: AbortController | undefined
let lastActivitySignal = 0

export function getToken(): string {
  try {
    return localStorage.getItem(TOKEN_KEY) ?? ''
  } catch {
    return ''
  }
}

export function setToken(token: string): void {
  try {
    localStorage.setItem(TOKEN_KEY, token)
  } catch {
    // storage disabled — token simply won't persist across reloads
  }
}

export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
    public device?: Device,
  ) {
    super(message)
    this.name = 'ApiError'
  }
}

const emptyState: DeviceState = {
  online: false,
  on: false,
  brightness: 0,
  color: { r: 255, g: 255, b: 255 },
  color_temp: 0,
  scene: 0,
  scene_speed: 0,
  speed: 0,
  sleep: false,
  timer_hours: 0,
  timer_elapsed_mins: 0,
  light: false,
  volume: 0,
  muted: false,
  text_active: false,
  text_value: '',
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function asString(value: unknown): string {
  return typeof value === 'string' ? value : ''
}

function asNumber(value: unknown): number {
  return typeof value === 'number' && Number.isFinite(value) ? value : 0
}

function asColor(value: unknown): Color {
  if (!isRecord(value)) return emptyState.color
  return {
    r: asNumber(value.r),
    g: asNumber(value.g),
    b: asNumber(value.b),
  }
}

function asState(value: unknown): DeviceState {
  if (!isRecord(value)) return emptyState
  return {
    online: value.online === true,
    on: value.on === true,
    brightness: asNumber(value.brightness),
    color: asColor(value.color),
    color_temp: asNumber(value.color_temp),
    scene: asNumber(value.scene),
    scene_speed: asNumber(value.scene_speed),
    speed: asNumber(value.speed),
    sleep: value.sleep === true,
    timer_hours: asNumber(value.timer_hours),
    timer_elapsed_mins: asNumber(value.timer_elapsed_mins),
    light: value.light === true,
    volume: asNumber(value.volume),
    muted: value.muted === true,
    text_active: value.text_active === true,
    text_value: asString(value.text_value),
  }
}

function asStringArray(value: unknown): string[] {
  return Array.isArray(value) ? value.filter((item): item is string => typeof item === 'string') : []
}

function asNumberArray(value: unknown): number[] | undefined {
  if (!Array.isArray(value)) return undefined
  const out = value.filter((item): item is number => typeof item === 'number')
  return out.length ? out : undefined
}

function asSpeedRange(item: Record<string, unknown>): { min?: number; max?: number } {
  const min = asNumber(item.speed_min)
  const max = asNumber(item.speed_max)
  return min > 0 && max > min ? { min, max } : {}
}

function asColorTempRange(item: Record<string, unknown>): { min?: number; max?: number } {
  const min = asNumber(item.color_temp_min)
  const max = asNumber(item.color_temp_max)
  return min > 0 && max > min ? { min, max } : {}
}

export function normalizeDevices(value: unknown): Device[] {
  if (!Array.isArray(value)) return []
  const out: Device[] = []
  for (const item of value) {
    if (!isRecord(item)) continue
    const id = asString(item.id)
    if (!id) continue
    const colorTempRange = asColorTempRange(item)
    const speedRange = asSpeedRange(item)
    out.push({
      id,
      name: asString(item.name),
      brand: asString(item.brand),
      model: asString(item.model),
      series: typeof item.series === 'string' ? item.series : undefined,
      mac: asString(item.mac),
      capabilities: asStringArray(item.capabilities),
      color_temp_min: colorTempRange.min,
      color_temp_max: colorTempRange.max,
      speed_min: speedRange.min,
      speed_max: speedRange.max,
      timer_options: asNumberArray(item.timer_options),
      scenes: Array.isArray(item.scenes) ? (item.scenes as Scene[]) : undefined,
      apps: Array.isArray(item.apps) ? (item.apps as App[]) : undefined,
      state: asState(item.state),
    })
  }
  return out
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, {
    ...init,
    headers: {
      ...(init?.headers ?? {}),
      Authorization: `Bearer ${getToken()}`,
    },
  })
  if (!res.ok) throw await failure(res)
  return (await res.json()) as T
}

// failure turns a non-OK response into the error the UI shows. Setu answers
// with {"error": "..."} and sometimes a reconciled device, so the message the
// user sees is the server's own, not a bare status code.
async function failure(res: Response): Promise<ApiError> {
  let msg = res.statusText
  let device: Device | undefined
  try {
    const body = (await res.json()) as unknown
    if (isRecord(body)) {
      if (typeof body.error === 'string') msg = body.error
      device = normalizeDevices([body.device])[0]
    }
  } catch {
    // non-JSON error body — keep the status text
  }
  return new ApiError(res.status, msg, device)
}

export async function listDevices(hardwareRefresh = false): Promise<Device[]> {
  // Only the newest snapshot request is useful. Abort an older one immediately
  // so a slow pre-resume/pre-token-change response cannot finish after it and
  // overwrite newer state in the store.
  activeDeviceListController?.abort()
  const controller = new AbortController()
  activeDeviceListController = controller
  let timedOut = false
  const timeout = setTimeout(() => {
    timedOut = true
    controller.abort()
  }, DEVICE_LIST_TIMEOUT_MS)

  try {
    const path = hardwareRefresh ? '/api/devices?refresh=true' : '/api/devices'
    return normalizeDevices(await request<unknown>(path, { signal: controller.signal }))
  } catch (err) {
    if (timedOut) throw new Error('Setu did not respond within 8 seconds.')
    throw err
  } finally {
    clearTimeout(timeout)
    if (activeDeviceListController === controller) activeDeviceListController = undefined
  }
}

// signalActivity is intentionally much cheaper than polling devices. Pointer
// and keyboard bursts are throttled to one small authenticated hint every 30s;
// the server uses it only to keep the active polling cadence warm.
export function signalActivity(): void {
  const token = getToken()
  const now = Date.now()
  if (!token || now - lastActivitySignal < ACTIVITY_SIGNAL_INTERVAL_MS) return
  lastActivitySignal = now
  void fetch('/api/activity', {
    method: 'POST',
    headers: { Authorization: `Bearer ${token}` },
  }).catch(() => {
    // Activity is only a cadence hint; normal refresh/WS paths own errors.
  })
}

export function sendCommand(
  id: string,
  action: CommandAction,
  value?: number | Color | string | boolean,
): Promise<Device> {
  return request<Device>(`/api/devices/${encodeURIComponent(id)}/command`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ action, value }),
  })
}

function normalizeDiagnostics(value: unknown): DeviceDiagnostics[] {
  if (!Array.isArray(value)) return []
  const out: DeviceDiagnostics[] = []
  for (const item of value) {
    if (!isRecord(item)) continue
    const id = asString(item.id)
    if (!id) continue
    out.push({
      id,
      pollable: item.pollable === true,
      last_poll_at: asNumber(item.last_poll_at),
      last_poll_error: asString(item.last_poll_error),
      last_command_at: asNumber(item.last_command_at),
      last_command_action: asString(item.last_command_action),
      last_command_error: asString(item.last_command_error),
    })
  }
  return out
}

export async function getDiagnostics(): Promise<DeviceDiagnostics[]> {
  return normalizeDiagnostics(await request<unknown>('/api/diagnostics'))
}

export async function refreshDevice(id: string): Promise<Device> {
  const device = normalizeDevices([
    await request<unknown>(`/api/devices/${encodeURIComponent(id)}/refresh`, {
      method: 'POST',
    }),
  ])[0]
  if (!device) throw new Error('Setu returned an invalid device status.')
  return device
}

function normalizeDiscovery(value: unknown): DiscoveryResult {
  if (!isRecord(value)) return { candidates: [], errors: [] }
  const candidates: DiscoveredDevice[] = []
  if (Array.isArray(value.candidates)) {
    for (const item of value.candidates) {
      if (!isRecord(item)) continue
      const mac = asString(item.mac)
      if (!mac) continue // without the identity there is nothing to configure
      candidates.push({
        brand: asString(item.brand),
        model: asString(item.model),
        series: asString(item.series) || undefined,
        name: asString(item.name) || undefined,
        ip: asString(item.ip) || undefined,
        mac,
        configured: item.configured === true,
        device_id: asString(item.device_id) || undefined,
      })
    }
  }
  return { candidates, errors: asStringArray(value.errors) }
}

// scanNetwork asks each brand to enumerate its devices on the LAN. It actively
// broadcasts, so it is a POST and the server runs one scan at a time (409 when
// another is already in flight).
export async function scanNetwork(): Promise<DiscoveryResult> {
  return normalizeDiscovery(await request<unknown>('/api/discovery/scan', { method: 'POST' }))
}

export function listDeviceTypes(): Promise<DeviceType[]> {
  return request<DeviceType[]>('/api/device-types')
}

// addDevice stores a device and brings it online; the server derives the id and
// answers with the live device, so the card can appear immediately.
export async function addDevice(spec: DeviceSpec): Promise<Device> {
  const device = normalizeDevices([
    await request<unknown>('/api/devices', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(spec),
    }),
  ])[0]
  if (!device) throw new Error('Setu returned an invalid device.')
  return device
}

// updateDevice edits the labels only. Brand, model and MAC are the device's
// identity: changing those is a remove and an add.
//
// It sends just the fields being changed. Name and series are two separate
// inputs, and a save from one must not carry the other's value — which may be
// a render behind after the previous save.
export async function updateDevice(
  id: string,
  labels: { name?: string; series?: string },
): Promise<Device> {
  const device = normalizeDevices([
    await request<unknown>(`/api/devices/${encodeURIComponent(id)}`, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(labels),
    }),
  ])[0]
  if (!device) throw new Error('Setu returned an invalid device.')
  return device
}

// deleteDevice is the one call with no response body (204), so it does not go
// through request().
export async function deleteDevice(id: string): Promise<void> {
  const res = await fetch(`/api/devices/${encodeURIComponent(id)}`, {
    method: 'DELETE',
    headers: { Authorization: `Bearer ${getToken()}` },
  })
  if (!res.ok) throw await failure(res)
}

export function exportDevices(): Promise<DeviceList> {
  return request<DeviceList>('/api/devices/export')
}

// replaceDevices swaps the whole list (restore). The server validates and
// builds everything first, so a rejected list changes nothing.
export function replaceDevices(items: DeviceSpec[]): Promise<DeviceList> {
  return request<DeviceList>('/api/devices', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ version: 1, items }),
  })
}

export function getAutomations(): Promise<AutomationSnapshot> {
  return request<AutomationSnapshot>('/api/automations')
}

export function exportAutomations(): Promise<AutomationState> {
  return request<AutomationState>('/api/automations/export')
}

export function saveAutomations(state: AutomationState): Promise<AutomationUpdate> {
  return request<AutomationUpdate>('/api/automations', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(state),
  })
}

export function runAutomation(id: string): Promise<{ run_id?: string; status: string }> {
  return request(`/api/automations/${encodeURIComponent(id)}/run`, { method: 'POST' })
}

export function rotateAutomationToken(
  id: string,
): Promise<{ token: string; state: AutomationState }> {
  return request(`/api/automations/${encodeURIComponent(id)}/token`, { method: 'POST' })
}

// wsURL builds the WebSocket URL (same origin). The token rides as a query
// parameter because browsers cannot set an Authorization header on a WebSocket.
export function wsURL(): string {
  const proto = location.protocol === 'https:' ? 'wss:' : 'ws:'
  return `${proto}//${location.host}/ws?token=${encodeURIComponent(getToken())}`
}
