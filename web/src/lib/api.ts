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
  scenes?: Scene[]
  apps?: App[]
  state: DeviceState
}

export type CommandAction =
  | 'on'
  | 'off'
  | 'set_brightness'
  | 'set_color'
  | 'set_color_temp'
  | 'set_scene'
  | 'set_scene_speed'
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

const TOKEN_KEY = 'setu.token'
const DEVICE_LIST_TIMEOUT_MS = 8000
let activeDeviceListController: AbortController | undefined

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
    volume: asNumber(value.volume),
    muted: value.muted === true,
    text_active: value.text_active === true,
    text_value: asString(value.text_value),
  }
}

function asStringArray(value: unknown): string[] {
  return Array.isArray(value) ? value.filter((item): item is string => typeof item === 'string') : []
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
  if (!res.ok) {
    let msg = res.statusText
    try {
      const body = (await res.json()) as { error?: string }
      if (body?.error) msg = body.error
    } catch {
      // non-JSON error body — keep the status text
    }
    throw new ApiError(res.status, msg)
  }
  return (await res.json()) as T
}

export async function listDevices(): Promise<Device[]> {
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
    return normalizeDevices(await request<unknown>('/api/devices', { signal: controller.signal }))
  } catch (err) {
    if (timedOut) throw new Error('Setu did not respond within 8 seconds.')
    throw err
  } finally {
    clearTimeout(timeout)
    if (activeDeviceListController === controller) activeDeviceListController = undefined
  }
}

export function sendCommand(
  id: string,
  action: CommandAction,
  value?: number | Color | string,
): Promise<Device> {
  return request<Device>(`/api/devices/${encodeURIComponent(id)}/command`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ action, value }),
  })
}

// wsURL builds the WebSocket URL (same origin). The token rides as a query
// parameter because browsers cannot set an Authorization header on a WebSocket.
export function wsURL(): string {
  const proto = location.protocol === 'https:' ? 'wss:' : 'ws:'
  return `${proto}//${location.host}/ws?token=${encodeURIComponent(getToken())}`
}
