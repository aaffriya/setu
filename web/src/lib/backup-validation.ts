import type { Color, CommandAction } from './api'
import type { Favorite, Scene, SceneCommand } from './store'

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function isInteger(value: unknown, min: number, max: number): value is number {
  return typeof value === 'number' && Number.isInteger(value) && value >= min && value <= max
}

function isShortString(value: unknown, max = 128): value is string {
  return typeof value === 'string' && value.length > 0 && value.length <= max
}

function isColor(value: unknown): value is Color {
  return (
    isRecord(value) &&
    isInteger(value.r, 0, 255) &&
    isInteger(value.g, 0, 255) &&
    isInteger(value.b, 0, 255)
  )
}

function isFavorite(value: unknown): value is Favorite {
  if (
    !isRecord(value) ||
    !isShortString(value.id) ||
    !isShortString(value.label) ||
    (value.brightness !== undefined && !isInteger(value.brightness, 0, 100))
  ) {
    return false
  }
  switch (value.kind) {
    case 'color':
      return isColor(value.value)
    case 'color_temp':
      return isInteger(value.value, 1, 10000)
    case 'scene':
      return isInteger(value.value, 1, Number.MAX_SAFE_INTEGER)
    default:
      return false
  }
}

export function isFavoritesSection(value: unknown): value is Record<string, Favorite[]> {
  return (
    isRecord(value) &&
    Object.values(value).every(
      (list) => Array.isArray(list) && list.every((favorite) => isFavorite(favorite)),
    )
  )
}

const noValueActions = new Set<CommandAction>([
  'on',
  'off',
  'volume_up',
  'volume_down',
  'mute',
  'wake',
])
const stringActions = new Set<CommandAction>([
  'key',
  'key_down',
  'key_up',
  'send_text',
  'launch_app',
])

function isSceneCommand(value: unknown): value is SceneCommand {
  if (!isRecord(value) || !isShortString(value.deviceId) || typeof value.action !== 'string') {
    return false
  }
  const action = value.action as CommandAction
  if (noValueActions.has(action)) return value.value === undefined
  if (stringActions.has(action)) return isShortString(value.value, 1024)
  switch (action) {
    case 'set_brightness':
    case 'set_volume':
      return isInteger(value.value, 0, 100)
    case 'set_color':
      return isColor(value.value)
    case 'set_color_temp':
      return isInteger(value.value, 1, 10000)
    case 'set_scene':
      return isInteger(value.value, 1, Number.MAX_SAFE_INTEGER)
    case 'set_scene_speed':
      return isInteger(value.value, 10, 200)
    default:
      return false
  }
}

function isScene(value: unknown): value is Scene {
  return (
    isRecord(value) &&
    isShortString(value.id) &&
    isShortString(value.name, 64) &&
    Array.isArray(value.commands) &&
    value.commands.length > 0 &&
    value.commands.every((command) => isSceneCommand(command))
  )
}

export function isScenesSection(value: unknown): value is Scene[] {
  return Array.isArray(value) && value.every((scene) => isScene(scene))
}
