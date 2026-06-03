// Global app state: a small set of Svelte stores plus the side-effects that keep
// them current (REST refresh, optimistic commands, and a resilient WebSocket).
//
// Design notes for the low-memory / backgrounding requirement:
//   - State is mirrored to localStorage so a tab the mobile OS killed repaints
//     instantly on resume, before any network round-trip.
//   - The WebSocket auto-reconnects with backoff and is re-primed on resume()
//     (called from visibilitychange / online), so it "just works" after a tab
//     comes back to the foreground.

import { writable, get } from 'svelte/store'
import {
  listDevices,
  sendCommand,
  wsURL,
  getToken,
  ApiError,
  type Device,
  type DeviceState,
  type Color,
  type CommandAction,
} from './api'

const CACHE_KEY = 'setu.devices'

export type ConnectionStatus = 'connecting' | 'online' | 'offline' | 'unauthorized'

export const devices = writable<Device[]>(loadCache())
export const connection = writable<ConnectionStatus>('connecting')
export const lastError = writable<string>('')

// Persist the device list so a cold resume paints immediately.
devices.subscribe((list) => {
  try {
    localStorage.setItem(CACHE_KEY, JSON.stringify(list))
  } catch {
    // storage full/disabled — non-fatal
  }
})

function loadCache(): Device[] {
  try {
    const raw = localStorage.getItem(CACHE_KEY)
    return raw ? (JSON.parse(raw) as Device[]) : []
  } catch {
    return []
  }
}

let errorTimer: ReturnType<typeof setTimeout> | undefined
function setError(msg: string): void {
  lastError.set(msg)
  clearTimeout(errorTimer)
  if (msg) errorTimer = setTimeout(() => lastError.set(''), 4000)
}

// --- data loading -----------------------------------------------------------

export async function refresh(): Promise<void> {
  try {
    devices.set(await listDevices())
    connection.set('online')
    setError('')
  } catch (err) {
    if (err instanceof ApiError && err.status === 401) {
      connection.set('unauthorized')
    } else {
      connection.set('offline')
    }
    setError(err instanceof Error ? err.message : 'failed to load devices')
  }
}

// --- commands (optimistic, reconciled by the response + WS) ------------------

export async function command(
  id: string,
  action: CommandAction,
  value?: number | Color,
): Promise<void> {
  const prev = get(devices)
  devices.update((list) =>
    list.map((d) => (d.id === id ? { ...d, state: applyOptimistic(d.state, action, value) } : d)),
  )
  try {
    const updated = await sendCommand(id, action, value)
    devices.update((list) => list.map((d) => (d.id === id ? updated : d)))
  } catch (err) {
    devices.set(prev) // revert the optimistic change
    setError(err instanceof Error ? err.message : 'command failed')
  }
}

function applyOptimistic(state: DeviceState, action: CommandAction, value?: number | Color): DeviceState {
  const next = { ...state }
  switch (action) {
    case 'on':
      next.on = true
      break
    case 'off':
      next.on = false
      break
    case 'set_brightness':
      next.brightness = value as number
      if ((value as number) > 0) next.on = true
      break
    case 'set_color':
      next.color = value as Color
      break
  }
  return next
}

// --- WebSocket: live updates with auto-reconnect -----------------------------

type WsMessage = { type: string; device_id: string; state: DeviceState }

let ws: WebSocket | null = null
let reconnectTimer: ReturnType<typeof setTimeout> | null = null
let backoff = 1000
let stopped = false

export function connect(): void {
  stopped = false
  openSocket()
}

function openSocket(): void {
  if (!getToken()) {
    connection.set('unauthorized')
    return
  }
  clearReconnect()
  try {
    ws = new WebSocket(wsURL())
  } catch {
    scheduleReconnect()
    return
  }
  connection.set('connecting')

  ws.onopen = () => {
    backoff = 1000
    connection.set('online')
  }
  ws.onmessage = (ev) => {
    try {
      const msg = JSON.parse(ev.data as string) as WsMessage
      devices.update((list) =>
        list.map((d) => (d.id === msg.device_id ? { ...d, state: msg.state } : d)),
      )
    } catch {
      // ignore malformed frames
    }
  }
  ws.onclose = () => {
    ws = null
    if (!stopped) {
      connection.set('offline')
      scheduleReconnect()
    }
  }
  ws.onerror = () => ws?.close()
}

function scheduleReconnect(): void {
  if (stopped || reconnectTimer) return
  reconnectTimer = setTimeout(() => {
    reconnectTimer = null
    backoff = Math.min(backoff * 2, 15000)
    openSocket()
  }, backoff)
}

function clearReconnect(): void {
  if (reconnectTimer) {
    clearTimeout(reconnectTimer)
    reconnectTimer = null
  }
}

export function disconnect(): void {
  stopped = true
  clearReconnect()
  ws?.close()
  ws = null
}

// resume re-fetches state and re-primes the socket after the tab returns to the
// foreground (mobile OSes often suspend or kill backgrounded tabs).
export function resume(): void {
  void refresh()
  if (!ws || ws.readyState === WebSocket.CLOSED || ws.readyState === WebSocket.CLOSING) {
    backoff = 1000
    connect()
  }
}
