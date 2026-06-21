// Global app state: a small set of Svelte stores plus the side-effects that keep
// them current (REST refresh, optimistic commands, and a resilient WebSocket).
//
// Design notes for the low-memory / backgrounding requirement:
//   - State is mirrored to localStorage so a tab the mobile OS killed repaints
//     instantly on resume, before any network round-trip.
//   - The WebSocket auto-reconnects with backoff and is re-primed on resume()
//     (called from visibilitychange / online), so it "just works" after a tab
//     comes back to the foreground.

import { writable, get, type Writable } from 'svelte/store'
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
// When we last got fresh state (REST refresh or a WS event), as epoch ms. Drives
// the header's "updated Xs ago" hint while offline. 0 = never yet.
export const lastUpdated = writable<number>(0)

// persisted is a writable mirrored to localStorage — the single pattern behind
// every UI-only pref (favourites, expanded, scenes, rooms, order). Client-side
// only: no server state (keeps the binary free of user prefs). Per-browser, and
// resilient to a mobile tab reload.
function persisted<T>(key: string, fallback: T): Writable<T> {
  let initial = fallback
  try {
    const raw = localStorage.getItem(key)
    if (raw) initial = JSON.parse(raw) as T
  } catch {
    // unreadable / disabled — fall back
  }
  const store = writable<T>(initial)
  store.subscribe((v) => {
    try {
      localStorage.setItem(key, JSON.stringify(v))
    } catch {
      // storage full/disabled — non-fatal
    }
  })
  return store
}

// uid mints an opaque id for client-side records (favourites, scenes). Prefers
// the platform UUID; the fallback only matters where crypto.randomUUID is absent
// or throws (older / insecure contexts) and is never parsed — just unique.
function uid(): string {
  try {
    return crypto.randomUUID()
  } catch {
    return `u${Date.now()}-${Math.random().toString(16).slice(2)}`
  }
}

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
    lastUpdated.set(Date.now())
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

// Returns true when the command reached the device, false on failure — callers
// can use this for one-shot feedback (e.g. the WoL "Sent" pulse). Existing
// callers that ignore the result are unaffected.
export async function command(
  id: string,
  action: CommandAction,
  value?: number | Color | string,
): Promise<boolean> {
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
    return true
  } catch (err) {
    if (prev) {
      // Revert just this device's optimistic change. If a newer WS state for
      // it raced in, the next event/poll re-corrects — same as before, but the
      // blast radius is one device instead of all of them.
      devices.update((list) => list.map((d) => (d.id === id ? prev : d)))
    }
    setError(err instanceof Error ? err.message : 'command failed')
    return false
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

export const favorites = persisted<Record<string, Favorite[]>>('setu.favorites', {})

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
    return { ...all, [deviceId]: [...list, { ...fav, id: uid() }] }
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

export const expanded = persisted<Record<string, boolean>>('setu.expanded', {})

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
  // Don't blink the status chip to "connecting" when we're just recycling an
  // already-live connection (e.g. a healthy resume — see resume()): keep the
  // current status and let onopen/onclose settle it. A genuine reconnect comes
  // in via onclose, which has already set 'offline', so this still shows
  // "connecting" there.
  if (get(connection) !== 'online') connection.set('connecting')

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
      lastUpdated.set(Date.now())
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
export function resume(): void {
  void refresh()
  backoff = 1000 // foreground again — retry eagerly, not at the backed-off pace
  // A backgrounded socket can go *half-open*: the mobile OS / NAT drops the TCP
  // link without the socket ever leaving readyState OPEN. Since the client never
  // writes to the WS, nothing detects this — and openSocket() would treat the
  // zombie as live and refuse to reconnect, so live events stall silently (the
  // header even keeps reading "Live"). Drop any existing socket first — its
  // handlers identity-check `ws`, so nulling `ws` neutralizes them — then
  // reconnect fresh. refresh() above already re-synced, so the brief reconnect
  // is effectively free and guarantees a working live channel after resume.
  const stale = ws
  ws = null
  stale?.close()
  connect()
}

// --- scenes: manual multi-device presets (snapshot + replay) -----------------
// A scene is a *manual* tile that fires several existing commands at once ("Movie
// mode" = TV on + lamp warm-dim). It's user-triggered only — nothing time- or
// event-driven (that would be the out-of-scope automation engine). The editor
// picks which devices to include and snapshots their current look; stored in
// localStorage like every other UI pref.

export type SceneCommand = {
  deviceId: string
  action: CommandAction
  value?: number | Color | string
}
export type Scene = { id: string; name: string; commands: SceneCommand[] }

export const scenes = persisted<Scene[]>('setu.scenes', [])

// snapshotCommands turns a device's current state into the commands that would
// reproduce its look — power first, then (if on) brightness and whichever colour
// mode is active, plus volume. Mirrors the optimistic mapping in reverse. Note:
// a TV's input *source* isn't in device state, so it can't be snapshotted — the
// editor lets the user attach a source/app launch explicitly (see ScenePick).
function snapshotCommands(d: Device): SceneCommand[] {
  const caps = new Set(d.capabilities)
  const s = d.state
  const out: SceneCommand[] = []
  if (caps.has('switch') && !s.on) {
    out.push({ deviceId: d.id, action: 'off' })
    return out // off → nothing else to restore
  }
  if (caps.has('switch')) out.push({ deviceId: d.id, action: 'on' })
  if (caps.has('brightness') && s.brightness > 0)
    out.push({ deviceId: d.id, action: 'set_brightness', value: s.brightness })
  if (caps.has('color_temp') && s.color_temp > 0)
    out.push({ deviceId: d.id, action: 'set_color_temp', value: s.color_temp })
  else if (caps.has('scene') && s.scene > 0)
    out.push({ deviceId: d.id, action: 'set_scene', value: s.scene })
  else if (caps.has('color'))
    out.push({ deviceId: d.id, action: 'set_color', value: s.color })
  if (caps.has('volume')) out.push({ deviceId: d.id, action: 'set_volume', value: s.volume })
  return out
}

// ScenePick is one included device. `launch`, when set, appends an explicit
// action the snapshot can't express — "app:<id>" launches a TV app, "key:<KEY>"
// sends a source key (e.g. key:KEY_HDMI) — so a scene can switch the TV's input.
export type ScenePick = { deviceId: string; launch?: string }

// createScene snapshots the picked devices (plus any chosen source/app) into a
// named scene. Empty picks → no-op.
export function createScene(name: string, picks: ScenePick[]): void {
  const live = get(devices)
  const commands: SceneCommand[] = []
  for (const p of picks) {
    const d = live.find((x) => x.id === p.deviceId)
    if (!d) continue
    commands.push(...snapshotCommands(d))
    if (p.launch?.startsWith('app:'))
      commands.push({ deviceId: d.id, action: 'launch_app', value: p.launch.slice(4) })
    else if (p.launch?.startsWith('key:'))
      commands.push({ deviceId: d.id, action: 'key', value: p.launch.slice(4) })
  }
  if (commands.length === 0) return
  scenes.update((list) => [...list, { id: uid(), name, commands }])
}

export function removeScene(id: string): void {
  scenes.update((list) => list.filter((s) => s.id !== id))
}

// runScene replays a scene's commands through the normal (optimistic) command
// path. Devices it referenced that no longer exist are skipped harmlessly.
export function runScene(scene: Scene): void {
  const live = new Set(get(devices).map((d) => d.id))
  for (const c of scene.commands) {
    if (live.has(c.deviceId)) void command(c.deviceId, c.action, c.value)
  }
}

// --- rooms & manual order (UI-only organisation) -----------------------------
// Both are localStorage-only and only matter once there are many devices. `rooms`
// maps deviceId → room name (absent = unassigned). `order` is the manual card
// order by id; ids missing from it fall back to server order, appended.

export const rooms = persisted<Record<string, string>>('setu.rooms', {})

export function setRoom(deviceId: string, room: string): void {
  rooms.update((m) => {
    const next = { ...m }
    if (room) next[deviceId] = room
    else delete next[deviceId]
    return next
  })
}

export const order = persisted<string[]>('setu.order', [])

// orderDevices sorts a device list by the saved manual order; unknown ids keep
// their incoming (server) order, appended after the explicitly-ordered ones.
export function orderDevices(list: Device[], ids: string[]): Device[] {
  if (ids.length === 0) return list
  const rank = new Map(ids.map((id, i) => [id, i]))
  return [...list].sort((a, b) => {
    const ra = rank.get(a.id) ?? Infinity
    const rb = rank.get(b.id) ?? Infinity
    return ra === rb ? 0 : ra - rb
  })
}

// moveDevice rewrites `order` so dragId sits immediately before overId, using the
// supplied display order as the base (so a first drag captures today's order).
export function moveDevice(displayIds: string[], dragId: string, overId: string): void {
  if (dragId === overId) return
  const ids = displayIds.filter((id) => id !== dragId)
  const at = ids.indexOf(overId)
  if (at < 0) return
  ids.splice(at, 0, dragId)
  // No-op if nothing actually moved — `order.set` would otherwise re-persist an
  // identical list (a localStorage write) on every such call.
  if (ids.length === displayIds.length && ids.every((id, i) => id === displayIds[i])) return
  order.set(ids)
}
