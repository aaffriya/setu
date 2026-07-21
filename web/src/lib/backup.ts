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
  getAutomations,
  saveAutomations,
  type AutomationAction,
  type AutomationRule,
  type AutomationState,
  type Device,
} from './api'

export const BACKUP_LIMIT = 256 * 1024

export type BackupSelection = {
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

function isStringRecord(value: unknown): boolean {
  return isRecord(value) && Object.values(value).every((item) => typeof item === 'string')
}

function isBooleanRecord(value: unknown): boolean {
  return isRecord(value) && Object.values(value).every((item) => typeof item === 'boolean')
}

export async function createBackup(selection: BackupSelection): Promise<SetuBackup> {
  const sections: BackupSections = {}
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
  const allowed = new Set(['favorites', 'rooms', 'scenes', 'appearance', 'automations'])
  if (keys.length === 0 || keys.some((key) => !allowed.has(key))) {
    throw new Error('Backup contains unsupported sections.')
  }
  const sections = value.sections
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

function ruleMatchesDevices(rule: AutomationRule, devices: Map<string, Device>): boolean {
  if (rule.trigger.type === 'device_state') {
    const source = devices.get(rule.trigger.device.device_id)
    if (!source?.capabilities.includes('switch')) return false
  }
  for (const condition of rule.conditions ?? []) {
    if (!devices.get(condition.device_id)?.capabilities.includes('switch')) return false
  }
  return rule.actions.every((action) => {
    const device = devices.get(action.device_id)
    return device !== undefined && supportsAction(device, action)
  })
}

function portableAutomations(state: AutomationState, devices: Device[]): AutomationState {
  const available = new Map(devices.map((device) => [device.id, device]))
  const copy = JSON.parse(JSON.stringify(state)) as AutomationState
  for (const rule of copy.items) {
    if (!ruleMatchesDevices(rule, available)) rule.enabled = false
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
export async function restoreBackup(backup: SetuBackup, devices: Device[]): Promise<void> {
  let automation: AutomationState | undefined
  if (backup.sections.automations) {
    const current = await getAutomations()
    automation = portableAutomations(backup.sections.automations, devices)
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
