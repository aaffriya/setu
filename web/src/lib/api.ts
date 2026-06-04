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
}

export type Device = {
  id: string
  name: string
  brand: string
  model: string
  mac: string
  capabilities: string[]
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
  | 'mute'
  | 'key'
  | 'launch_app'

const TOKEN_KEY = 'setu.token'

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

export function listDevices(): Promise<Device[]> {
  return request<Device[]>('/api/devices')
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
