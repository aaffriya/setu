import { get } from 'svelte/store'
import {
  expanded,
  favorites,
  order,
  rooms,
  scenes,
  type Favorite,
  type Scene,
} from './store'
import { getTheme, type Theme } from './theme'
import { isFavoritesSection, isScenesSection } from './backup-validation'
import {
  exportAutomations,
  exportDevices,
  getAutomations,
  listDevices,
  replaceDevices,
  saveAutomations,
  type AutomationAction,
  type AutomationRule,
  type AutomationState,
  type Device,
  type DeviceSpec,
} from './api'

export const BACKUP_LIMIT = 256 * 1024

export type BackupSelection = {
  devices: boolean
  favorites: boolean
  rooms: boolean
  scenes: boolean
  appearance: boolean
  automations: boolean
}

type AppearanceBackup = {
  order: string[]
  expanded: Record<string, boolean>
  theme: Theme
}

type BackupSections = {
  // The devices themselves live on the server now, so they belong in the
  // backup: without them a restored install has automations and room names for
  // devices that do not exist.
  devices?: DeviceSpec[]
  favorites?: Record<string, Favorite[]>
  rooms?: Record<string, string>
  scenes?: Scene[]
  appearance?: AppearanceBackup
  automations?: AutomationState
}

export type SetuBackup = {
  format: 'setu-backup'
  version: 1
  created_at: string
  sections: BackupSections
}

const storageKeys = {
  favorites: 'setu.favorites',
  rooms: 'setu.rooms',
  scenes: 'setu.scenes',
  order: 'setu.order',
  expanded: 'setu.expanded',
  theme: 'setu.theme',
} as const

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

// A device entry is only worth restoring if it carries the identity Setu needs
// to build it: a brand, the driver to run it with, and the MAC that is the
// device. The server validates again; this keeps an obviously wrong file from
// reaching it.
//
// A backup written before the driver key was called `driver` said `model`
// instead, so that shape is accepted too and upgraded on restore — a backup
// exists to survive the version that follows it.
function isDeviceSection(value: unknown): boolean {
  return (
    Array.isArray(value) &&
    value.every(
      (item) =>
        isRecord(item) &&
        typeof item.brand === 'string' &&
        (typeof item.driver === 'string' || typeof item.model === 'string') &&
        typeof item.name === 'string' &&
        typeof item.mac === 'string',
    )
  )
}

// upgradeDevices rewrites a pre-`driver` device section: what it called the
// model was the driver key, and what it called the series was the model.
function upgradeDevices(items: DeviceSpec[]): DeviceSpec[] {
  return items.map((item) => {
    if (item.driver) return item
    const legacy = item as DeviceSpec & { series?: string }
    return { ...legacy, driver: legacy.model ?? '', model: legacy.series, series: undefined }
  })
}

function isStringRecord(value: unknown): boolean {
  return isRecord(value) && Object.values(value).every((item) => typeof item === 'string')
}

function isBooleanRecord(value: unknown): boolean {
  return isRecord(value) && Object.values(value).every((item) => typeof item === 'boolean')
}

export async function createBackup(selection: BackupSelection): Promise<SetuBackup> {
  const sections: BackupSections = {}
  if (selection.devices) sections.devices = (await exportDevices()).items
  if (selection.favorites) sections.favorites = get(favorites)
  if (selection.rooms) sections.rooms = get(rooms)
  if (selection.scenes) sections.scenes = get(scenes)
  if (selection.appearance) {
    sections.appearance = { order: get(order), expanded: get(expanded), theme: getTheme() }
  }
  if (selection.automations) sections.automations = await exportAutomations()
  if (Object.keys(sections).length === 0) throw new Error('Select at least one backup type.')
  return { format: 'setu-backup', version: 1, created_at: new Date().toISOString(), sections }
}

export function downloadBackup(backup: SetuBackup): void {
  const text = JSON.stringify(backup, null, 2)
  if (new Blob([text]).size > BACKUP_LIMIT) throw new Error('Backup is larger than 256 KB.')
  const date = backup.created_at.slice(0, 10)
  const url = URL.createObjectURL(new Blob([text], { type: 'application/json' }))
  const link = document.createElement('a')
  link.href = url
  link.download = `setu-backup-${date}.json`
  link.click()
  setTimeout(() => URL.revokeObjectURL(url), 0)
}

export async function readBackup(file: File): Promise<SetuBackup> {
  if (file.size > BACKUP_LIMIT) throw new Error('Backup is larger than 256 KB.')
  let value: unknown
  try {
    value = JSON.parse(await file.text())
  } catch {
    throw new Error('Backup is not valid JSON.')
  }
  return validateBackup(value)
}

export function validateBackup(value: unknown): SetuBackup {
  if (
    !isRecord(value) ||
    value.format !== 'setu-backup' ||
    value.version !== 1 ||
    typeof value.created_at !== 'string'
  ) {
    throw new Error('This is not a supported Setu backup.')
  }
  if (!isRecord(value.sections)) throw new Error('Backup has no sections.')
  const keys = Object.keys(value.sections)
  const allowed = new Set(['devices', 'favorites', 'rooms', 'scenes', 'appearance', 'automations'])
  if (keys.length === 0 || keys.some((key) => !allowed.has(key))) {
    throw new Error('Backup contains unsupported sections.')
  }
  const sections = value.sections
  if ('devices' in sections && !isDeviceSection(sections.devices)) {
    throw new Error('Devices section is invalid.')
  }
  if ('favorites' in sections && !isFavoritesSection(sections.favorites)) {
    throw new Error('Favorites section is invalid.')
  }
  if ('rooms' in sections && !isStringRecord(sections.rooms)) {
    throw new Error('Rooms section is invalid.')
  }
  if ('scenes' in sections && !isScenesSection(sections.scenes)) {
    throw new Error('Scenes section is invalid.')
  }
  if ('appearance' in sections) {
    const appearance = sections.appearance
    if (
      !isRecord(appearance) ||
      !Array.isArray(appearance.order) ||
      !appearance.order.every((id) => typeof id === 'string') ||
      !isBooleanRecord(appearance.expanded) ||
      !['system', 'light', 'dark'].includes(String(appearance.theme))
    ) {
      throw new Error('Appearance section is invalid.')
    }
  }
  if ('automations' in sections) {
    const automation = sections.automations
    if (
      !isRecord(automation) ||
      automation.version !== 1 ||
      !Array.isArray(automation.items) ||
      typeof automation.paused !== 'boolean'
    ) {
      throw new Error('Automations section is invalid.')
    }
    for (const item of automation.items) {
      if (!isRecord(item) || !isRecord(item.trigger) || item.trigger.type !== 'webhook') continue
      const webhook = item.trigger.webhook
      if (
        !isRecord(webhook) ||
        typeof webhook.secret_hash !== 'string' ||
        !/^[0-9a-f]{64}$/.test(webhook.secret_hash)
      ) {
        throw new Error('Webhook backup is missing its restorable secret.')
      }
    }
  }
  return value as SetuBackup
}

export function backupSectionNames(backup: SetuBackup): string[] {
  const names: Record<keyof BackupSections, string> = {
    devices: 'Devices',
    favorites: 'Favorites',
    rooms: 'Rooms',
    scenes: 'Manual scenes',
    appearance: 'Layout & theme',
    automations: 'Automations',
  }
  return (Object.keys(backup.sections) as Array<keyof BackupSections>).map((key) => names[key])
}

function supportsAction(device: Device, action: AutomationAction): boolean {
  const capabilities = new Set(device.capabilities)
  switch (action.action) {
    case 'on':
    case 'off':
      return capabilities.has('switch')
    case 'set_brightness':
      return capabilities.has('brightness')
    case 'set_color':
      return capabilities.has('color')
    case 'set_color_temp':
      return (
        capabilities.has('color_temp') &&
        (typeof action.value !== 'number' ||
          (action.value >= (device.color_temp_min ?? action.value) &&
            action.value <= (device.color_temp_max ?? action.value)))
      )
    case 'set_scene':
      return (
        capabilities.has('scene') &&
        (typeof action.value !== 'number' ||
          (device.scenes ?? []).some((scene) => scene.id === action.value))
      )
    case 'set_scene_speed':
      return capabilities.has('scene')
    case 'set_speed':
      return (
        capabilities.has('speed') &&
        (typeof action.value !== 'number' ||
          (action.value >= (device.speed_min ?? action.value) &&
            action.value <= (device.speed_max ?? action.value)))
      )
    case 'set_sleep':
      return capabilities.has('sleep')
    case 'set_light':
      return capabilities.has('light')
    case 'set_timer':
      return (
        capabilities.has('timer') &&
        (typeof action.value !== 'number' || (device.timer_options ?? []).includes(action.value))
      )
    case 'set_volume':
      return capabilities.has('volume')
    case 'launch_app':
      return (
        capabilities.has('app') &&
        (typeof action.value !== 'string' ||
          (device.apps ?? []).some((app) => app.id === action.value))
      )
    case 'wake':
      return capabilities.has('wol')
  }
  return false
}

// The capability a device must report for a watched value to mean anything.
// Mirrors metricCapability in internal/automation.
const METRIC_CAPABILITY: Record<string, string> = {
  power: 'switch',
  brightness: 'brightness',
  speed: 'speed',
  volume: 'volume',
  color_temp: 'color_temp',
  timer_hours: 'timer',
}

function ruleMatchesInstall(
  rule: AutomationRule,
  devices: Map<string, Device>,
  enabledAutomationIDs: Set<string>,
  presence: boolean,
): boolean {
  if (rule.trigger.type === 'device_state') {
    const source = devices.get(rule.trigger.device.device_id)
    const capability = METRIC_CAPABILITY[rule.trigger.device.metric ?? 'power']
    if (!capability || !source?.capabilities.includes(capability)) return false
  }
  if (rule.trigger.type === 'device_offline') {
    // Reachability requires an explicit live-contact signal; being pollable is
    // not enough when Online means control availability.
    const source = devices.get(rule.trigger.offline.device_id)
    if (!source?.reports_reachability) return false
  }
  if (rule.trigger.type === 'device_online') {
    const source = devices.get(rule.trigger.online.device_id)
    if (!source?.reports_reachability) return false
  }
  // A presence rule restored onto a host that cannot read its neighbour table
  // would be refused, taking the whole restore with it.
  if (rule.trigger.type === 'presence' && !presence) return false
  for (const condition of rule.conditions ?? []) {
    if (!devices.get(condition.device_id)?.capabilities.includes('switch')) return false
  }
  return rule.actions.every((action) => {
    for (const condition of action.when ?? []) {
      if (!devices.get(condition.device_id)?.capabilities.includes('switch')) return false
    }
    if (action.action === 'run_automation') {
      return (
        typeof action.automation_id === 'string' &&
        enabledAutomationIDs.has(action.automation_id)
      )
    }
    const device = devices.get(action.device_id)
    return device !== undefined && supportsAction(device, action)
  })
}

function portableAutomations(
  state: AutomationState,
  devices: Device[],
  presence: boolean,
): AutomationState {
  const available = new Map(devices.map((device) => [device.id, device]))
  const copy = JSON.parse(JSON.stringify(state)) as AutomationState
  let changed = true
  while (changed) {
    changed = false
    const enabledAutomationIDs = new Set(
      copy.items.filter((rule) => rule.enabled).map((rule) => rule.id),
    )
    for (const rule of copy.items) {
      if (rule.enabled && !ruleMatchesInstall(rule, available, enabledAutomationIDs, presence)) {
        rule.enabled = false
        changed = true
      }
    }
  }
  return copy
}

type RawSnapshot = Record<string, string | null>

function localValues(backup: SetuBackup): Record<string, string | null> {
  const values: Record<string, string | null> = {}
  const sections = backup.sections
  if (sections.favorites !== undefined)
    values[storageKeys.favorites] = JSON.stringify(sections.favorites)
  if (sections.rooms !== undefined) values[storageKeys.rooms] = JSON.stringify(sections.rooms)
  if (sections.scenes !== undefined) values[storageKeys.scenes] = JSON.stringify(sections.scenes)
  if (sections.appearance !== undefined) {
    values[storageKeys.order] = JSON.stringify(sections.appearance.order)
    values[storageKeys.expanded] = JSON.stringify(sections.appearance.expanded)
    values[storageKeys.theme] =
      sections.appearance.theme === 'system' ? null : sections.appearance.theme
  }
  return values
}

function applyLocal(values: Record<string, string | null>): RawSnapshot {
  const previous: RawSnapshot = {}
  try {
    for (const [key, value] of Object.entries(values)) {
      previous[key] = localStorage.getItem(key)
      if (value === null) localStorage.removeItem(key)
      else localStorage.setItem(key, value)
    }
  } catch {
    rollbackLocal(previous)
    throw new Error('Browser storage is unavailable or full.')
  }
  return previous
}

function rollbackLocal(previous: RawSnapshot): void {
  for (const [key, value] of Object.entries(previous)) {
    try {
      if (value === null) localStorage.removeItem(key)
      else localStorage.setItem(key, value)
    } catch {
      // Best effort: the original write already reported storage failure.
    }
  }
}

// Restore is one user action. Included local sections replace their current
// keys; omitted sections are untouched. Backend validation happens before its
// atomic file replacement, and local keys roll back if that call fails.
//
// Devices go first and are not rolled back: every other section refers to them
// by id, so which automations survive depends on the devices that exist *after*
// the restore. The server validates and builds the whole list before replacing
// anything, so the failure this ordering exposes is narrow — devices restored,
// a later section refused — and the message says which.
export async function restoreBackup(backup: SetuBackup, devices: Device[]): Promise<void> {
  let installed = devices
  if (backup.sections.devices) {
    await replaceDevices(upgradeDevices(backup.sections.devices))
    installed = await listDevices()
  }

  let automation: AutomationState | undefined
  if (backup.sections.automations) {
    const current = await getAutomations()
    automation = portableAutomations(
      backup.sections.automations,
      installed,
      current.presence !== false,
    )
    automation.version = 1
    automation.revision = current.revision
  }

  const previous = applyLocal(localValues(backup))
  try {
    if (automation) await saveAutomations(automation)
  } catch (error) {
    rollbackLocal(previous)
    throw error
  }
}
