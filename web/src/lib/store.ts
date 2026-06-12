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
  value?: number | Color | string,
): Promise<void> {
  // Snapshot only the TARGET device, not the whole list: while this command is
  // on the wire, WS events and other in-flight commands keep updating other
  // devices, and a whole-list revert on failure would wind those back too
  // (stale UI until the next event/poll corrects it, seconds later).
  const prev = get(devices).find((d) => d.id === id)
  devices.update((list) =>
    list.map((d) => (d.id === id ? { ...d, state: applyOptimistic(d.state, action, value) } : d)),
  )
  try {
    const updated = await sendCommand(id, action, value)
    devices.update((list) => list.map((d) => (d.id === id ? updated : d)))
  } catch (err) {
    if (prev) {
      // Revert just this device's optimistic change. If a newer WS state for
      // it raced in, the next event/poll re-corrects — same as before, but the
      // blast radius is one device instead of all of them.
      devices.update((list) => list.map((d) => (d.id === id ? prev : d)))
    }
    setError(err instanceof Error ? err.message : 'command failed')
  }
}

function applyOptimistic(
  state: DeviceState,
  action: CommandAction,
  value?: number | Color | string,
): DeviceState {
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
      next.color_temp = 0
      next.scene = 0
      break
    case 'set_color_temp':
      next.color_temp = value as number
      next.scene = 0
      next.on = true
      break
    case 'set_scene':
      next.scene = value as number
      next.on = true
      break
    case 'set_scene_speed':
      next.scene_speed = value as number
      break
    case 'set_volume':
      next.volume = value as number
      break
    case 'mute':
      next.muted = !next.muted
      break
    // volume_up / volume_down / key / key_down / key_up / send_text have no
    // locally-predictable state — they're sent through as-is.
  }
  return next
}

// --- favourites (client-side presets, saved per device) ----------------------
// Favourites are a UI convenience, so they live in localStorage — no backend
// state files, keeping the server lightweight. They're per-browser.

export type Favorite = {
  id: string
  kind: 'color' | 'color_temp' | 'scene'
  value: Color | number
  label: string
  // Captured brightness (0–100) so a favourite restores the whole look — the
  // colour/temp/scene *and* how bright it was. Optional: older saved favourites
  // (and devices without a brightness control) simply omit it.
  brightness?: number
}

const FAV_KEY = 'setu.favorites'

export const favorites = writable<Record<string, Favorite[]>>(loadFavorites())

favorites.subscribe((all) => {
  try {
    localStorage.setItem(FAV_KEY, JSON.stringify(all))
  } catch {
    // storage disabled — non-fatal
  }
})

function loadFavorites(): Record<string, Favorite[]> {
  try {
    return JSON.parse(localStorage.getItem(FAV_KEY) ?? '{}') as Record<string, Favorite[]>
  } catch {
    return {}
  }
}

function favId(): string {
  try {
    return crypto.randomUUID()
  } catch {
    return `f${Date.now()}-${Math.random().toString(16).slice(2)}`
  }
}

export function addFavorite(deviceId: string, fav: Omit<Favorite, 'id'>): void {
  favorites.update((all) => {
    const list = all[deviceId] ?? []
    const dup = list.some(
      (f) =>
        f.kind === fav.kind &&
        JSON.stringify(f.value) === JSON.stringify(fav.value) &&
        f.brightness === fav.brightness,
    )
    if (dup) return all
    return { ...all, [deviceId]: [...list, { ...fav, id: favId() }] }
  })
}

export function removeFavorite(deviceId: string, id: string): void {
  favorites.update((all) => ({
    ...all,
    [deviceId]: (all[deviceId] ?? []).filter((f) => f.id !== id),
  }))
}

// applyFavorite re-sends a saved preset as the appropriate command, then restores
// the captured brightness so the whole look (mode + level) comes back.
export function applyFavorite(deviceId: string, fav: Favorite): void {
  switch (fav.kind) {
    case 'color':
      void command(deviceId, 'set_color', fav.value as Color)
      break
    case 'color_temp':
      void command(deviceId, 'set_color_temp', fav.value as number)
      break
    case 'scene':
      void command(deviceId, 'set_scene', fav.value as number)
      break
  }
  if (typeof fav.brightness === 'number' && fav.brightness > 0) {
    void command(deviceId, 'set_brightness', fav.brightness)
  }
}

// --- card expand/collapse (UI-only, persisted) -------------------------------
// Cards start collapsed (name + power + an expand button) and open on demand.
// Whether a card is expanded is a UI preference, so — like favourites — it lives
// in localStorage rather than the backend, and survives a mobile tab reload.

const EXPAND_KEY = 'setu.expanded'

export const expanded = writable<Record<string, boolean>>(loadExpanded())

expanded.subscribe((map) => {
  try {
    localStorage.setItem(EXPAND_KEY, JSON.stringify(map))
  } catch {
    // storage disabled — non-fatal
  }
})

function loadExpanded(): Record<string, boolean> {
  try {
    return JSON.parse(localStorage.getItem(EXPAND_KEY) ?? '{}') as Record<string, boolean>
  } catch {
    return {}
  }
}

export function toggleExpanded(id: string): void {
  expanded.update((map) => ({ ...map, [id]: !map[id] }))
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
  // ONE socket at a time: if the current one is live (or still connecting),
  // keep it. A second socket would duplicate every event in the UI, leak a
  // server-side subscription, and leave two sets of handlers fighting over the
  // shared `ws` variable (the old onclose nulling it / scheduling a competing
  // reconnect, the old onerror closing the new socket).
  if (ws && ws.readyState !== WebSocket.CLOSING && ws.readyState !== WebSocket.CLOSED) return
  clearReconnect()
  let sock: WebSocket
  try {
    sock = new WebSocket(wsURL())
  } catch {
    scheduleReconnect()
    return
  }
  ws = sock
  connection.set('connecting')

  // Handlers close over `sock` and bail unless it still owns `ws`, so a
  // replaced/disconnected socket's late events can never clobber the live one.
  sock.onopen = () => {
    if (ws !== sock) return
    backoff = 1000
    connection.set('online')
  }
  sock.onmessage = (ev) => {
    if (ws !== sock) return
    try {
      const msg = JSON.parse(ev.data as string) as WsMessage
      devices.update((list) =>
        list.map((d) => (d.id === msg.device_id ? { ...d, state: msg.state } : d)),
      )
    } catch {
      // ignore malformed frames
    }
  }
  sock.onclose = () => {
    if (ws !== sock) return
    ws = null
    if (!stopped) {
      connection.set('offline')
      scheduleReconnect()
    }
  }
  // Close *this* socket, never whatever `ws` points at by now.
  sock.onerror = () => sock.close()
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
  const sock = ws
  ws = null // neutralizes sock's handlers first (they identity-check `ws`)
  sock?.close()
}

// resume re-fetches state and re-primes the socket after the tab returns to the
// foreground (mobile OSes often suspend or kill backgrounded tabs). The command
// path never waits on this: actions fire against the cached list immediately.
// openSocket itself refuses to double-connect, so calling eagerly is safe.
export function resume(): void {
  void refresh()
  backoff = 1000 // foreground again — retry eagerly, not at the backed-off pace
  connect()
}
