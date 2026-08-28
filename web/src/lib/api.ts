// Thin fetch wrapper around the Setu JSON API, plus the shared data model. All
// calls are same-origin and carry the bearer token (kept in localStorage).

export type Color = { r: number; g: number; b: number }

export type Scene = {
  id: number
  name: string
  dynamic: boolean
  // Some devices treat the scene's level as part of the preset; on others the
  // scene is only a colour mode and brightness must be restored separately.
  brightness_locked?: boolean
}

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
  // brand is the vendor; model is the hardware, when anything has said what it
  // is; driver is which driver runs it — identity, never rendered.
  brand: string
  driver: string
  model?: string
  mac: string
  capabilities: string[]
  pollable: boolean
  reports_reachability: boolean
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

// One device found by a network scan. `driver` (and with it `label`) is empty
// when the brand answered but Setu has no driver for that hardware; `model` is
// what the device called itself, which may be a marketing name, a bare code, or
// nothing at all. `configured` means this brand already has a device with that
// MAC (then `device_id` names it).
export type DiscoveredDevice = {
  brand: string
  driver: string
  model?: string
  label?: string
  name?: string
  mac: string
  ip?: string
  configured: boolean
  device_id?: string
}

// One driver this build can run, under the category and name to show for it.
// The catalog comes from the server, so the manual add form can never offer
// something Setu cannot build — and the UI never has to turn a driver key into
// English.
export type DeviceType = {
  category: string
  brand: string
  driver: string
  label: string
}

// What Setu stores for one device — the form used to add one and to back the
// list up. Identity (brand, driver, mac) is fixed once added; only the name and
// the model can be edited.
export type DeviceSpec = {
  id?: string
  brand: string
  driver: string
  model?: string
  name: string
  mac: string
}

export type DeviceList = { version: number; items: DeviceSpec[] }

// A stored device the server could not start: the spec exactly as it was kept,
// plus why. It is in no device list and on no card — the manager never built
// it — so this is the only way the app can show that it exists at all.
//
// `repairable` says whether editing the name or model could bring it online.
// Only the labels are editable, so an entry whose driver or MAC is the problem
// cannot be fixed here at all, and the screen offers removal instead of a
// rename the server would refuse every time.
export type UnusableDevice = DeviceSpec & {
  id: string
  reason: string
  repairable: boolean
}

// DEVICE_LIST_VERSION is the schema of the device list this build exports and
// sends. It tracks the server's own (internal/api deviceFormatVersion).
export const DEVICE_LIST_VERSION = 2

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
  offset_minutes?: number
  when?: AutomationCondition[]
}

export type AutomationCondition = { device_id: string; on: boolean }

// A device trigger watches either the on/off edge (the default, and what an
// absent metric means) or one reported number crossing a threshold.
export type AutomationMetric =
  | 'power'
  | 'brightness'
  | 'speed'
  | 'volume'
  | 'color_temp'
  | 'timer_hours'
export type AutomationComparison = 'above' | 'below' | 'equals'

export type AutomationDeviceTrigger = {
  device_id: string
  on: boolean
  stable_seconds?: number
  metric?: AutomationMetric
  operator?: AutomationComparison
  value?: number
}

export type AutomationTrigger =
  | {
      type: 'schedule'
      schedule: { time: string; weekdays: number[]; utc_offset_minutes: number }
    }
  | { type: 'device_state'; device: AutomationDeviceTrigger }
  // Fires once when a device has been unreachable for this long, and arms again
  // only after it has been seen back on the network.
  | { type: 'device_offline'; offline: { device_id: string; minutes: number } }
  // Fires on the recovery edge when the completed minutes in the just-finished
  // offline episode match this comparison.
  | {
      type: 'device_online'
      online: { device_id: string; operator: AutomationComparison; minutes: number }
    }
  // Watches a MAC on the LAN — a phone arriving or leaving — with no device
  // added for it.
  | { type: 'presence'; presence: { mac: string; present: boolean; stable_seconds?: number } }
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
  offset_minutes?: number
  started_at: string
  duration_ms: number
  ok: boolean
  results: Array<{
    device_id?: string
    automation_id?: string
    action: string
    ok: boolean
    skipped?: boolean
    error?: string
  }>
}

// presence reports whether this host can read its neighbour table. Without it
// presence rules cannot be armed, so the editor does not offer them.
export type AutomationSnapshot = AutomationState & {
  runs: AutomationRun[]
  presence?: boolean
}
export type AutomationUpdate = {
  state: AutomationState
  generated_tokens?: Record<string, string>
}

// Accounts and permissions.
//
// The administrator is whoever presents SETU_TOKEN. Everyone else is created in
// the app and signs in with a token Setu generated — there is no password, and
// the token carries the name and permissions with it.
export type Role = 'read' | 'modify'

// Session is what the signed-in account may do. It is advisory: the server
// enforces all of it again on every request, so this only decides what the app
// bothers to show.
export type Session = {
  name: string
  admin: boolean
  role: Role
  // all_devices means this account is not limited to a device list.
  all_devices: boolean
  devices: string[]
  // users reports whether this build can manage accounts at all.
  users: boolean
}

export type User = {
  id: string
  name: string
  role: Role
  devices: string[]
  created_at?: number
}

// UserResponse carries a freshly issued token beside its account. The plaintext
// exists only in this one response, so the screen must show it before moving on.
export type UserResponse = { user: User; token?: string }

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
      driver: asString(item.driver),
      model: asString(item.model) || undefined,
      mac: asString(item.mac),
      capabilities: asStringArray(item.capabilities),
      pollable: item.pollable === true,
      reports_reachability: item.reports_reachability === true,
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
        driver: asString(item.driver),
        model: asString(item.model) || undefined,
        label: asString(item.label) || undefined,
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

// listUnusableDevices reports the stored entries that are not running. Kept out
// of the device store on purpose: these have no state to show and nothing to
// control, so only the device screen — where they are repaired or removed — asks
// for them.
export function listUnusableDevices(): Promise<UnusableDevice[]> {
  return request<UnusableDevice[]>('/api/devices/unusable')
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

// updateDevice edits the labels only. Brand, driver and MAC are the device's
// identity: changing those is a remove and an add.
//
// It sends just the fields being changed. The name and the model are two
// separate inputs, and a save from one must not carry the other's value — which
// may be a render behind after the previous save.
export async function updateDevice(
  id: string,
  labels: { name?: string; model?: string },
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
// builds everything first, so a rejected list changes nothing. The items are
// always in the current shape — a backup from an older Setu is upgraded before
// it gets here (see backup.ts).
export function replaceDevices(items: DeviceSpec[]): Promise<DeviceList> {
  return request<DeviceList>('/api/devices', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ version: DEVICE_LIST_VERSION, items }),
  })
}

// --- accounts ---------------------------------------------------------------

function asRole(value: unknown): Role {
  return value === 'modify' ? 'modify' : 'read'
}

function asUser(value: unknown): User | undefined {
  if (!isRecord(value)) return undefined
  const id = asString(value.id)
  if (!id) return undefined
  return {
    id,
    name: asString(value.name),
    role: asRole(value.role),
    devices: asStringArray(value.devices),
    created_at: asNumber(value.created_at) || undefined,
  }
}

// getSession answers "who am I, and what may I do?". A failure is not fatal: the
// app falls back to showing everything and lets the server refuse what it must.
export async function getSession(): Promise<Session> {
  const value = await request<unknown>('/api/session')
  if (!isRecord(value)) throw new Error('Setu returned an invalid session.')
  return {
    name: asString(value.name),
    admin: value.admin === true,
    role: asRole(value.role),
    all_devices: value.all_devices === true,
    devices: asStringArray(value.devices),
    users: value.users === true,
  }
}

export async function listUsers(): Promise<User[]> {
  const value = await request<unknown>('/api/users')
  if (!Array.isArray(value)) return []
  const out: User[] = []
  for (const item of value) {
    const user = asUser(item)
    if (user) out.push(user)
  }
  return out
}

async function userRequest(path: string, init: RequestInit): Promise<UserResponse> {
  const value = await request<unknown>(path, init)
  const user = isRecord(value) ? asUser(value.user) : undefined
  if (!user) throw new Error('Setu returned an invalid account.')
  return { user, token: isRecord(value) ? asString(value.token) || undefined : undefined }
}

// createUser takes only a name: signing in is the generated token and nothing
// else. The token comes back once, in this response.
export function createUser(name: string, role: Role, devices: string[]): Promise<UserResponse> {
  return userRequest('/api/users', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name, role, devices }),
  })
}

// updateUser sends only the fields being changed, so editing a name cannot
// silently resend — and revert — a stale copy of the device grants.
export function updateUser(
  id: string,
  patch: { name?: string; role?: Role; devices?: string[] },
): Promise<UserResponse> {
  return userRequest(`/api/users/${encodeURIComponent(id)}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(patch),
  })
}

// rotateUserToken issues a replacement and invalidates the previous token — the
// recovery path when one is lost or was shared with the wrong person.
export function rotateUserToken(id: string): Promise<UserResponse> {
  return userRequest(`/api/users/${encodeURIComponent(id)}/token`, { method: 'POST' })
}

export async function deleteUser(id: string): Promise<void> {
  const res = await fetch(`/api/users/${encodeURIComponent(id)}`, {
    method: 'DELETE',
    headers: { Authorization: `Bearer ${getToken()}` },
  })
  if (!res.ok) throw await failure(res)
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
