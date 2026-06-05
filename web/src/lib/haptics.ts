// Haptic feedback via the Web Vibration API. It's supported on Android
// (Chrome/Firefox) and ignored on iOS Safari, so every call is a guarded
// progressive enhancement — silent where unsupported, never throwing. Patterns
// are deliberately tiny so a press feels like a tick, not a buzz.

function vibrate(pattern: number | number[]): void {
  try {
    if (typeof navigator !== 'undefined' && typeof navigator.vibrate === 'function') {
      navigator.vibrate(pattern)
    }
  } catch {
    // unsupported, blocked, or bad pattern — feedback is optional, so ignore
  }
}

// Sliders fire input events continuously while dragging; throttle so a drag
// ratchets pleasantly instead of vibrating on every integer step.
let lastSlide = 0
const SLIDE_GAP_MS = 35

export const haptics = {
  // A short tick for any button / remote key press.
  tap(): void {
    vibrate(8)
  },
  // A lighter, throttled tick while dragging a slider — distinct from a tap.
  slide(): void {
    const now = Date.now()
    if (now - lastSlide < SLIDE_GAP_MS) return
    lastSlide = now
    vibrate(4)
  },
  // The power switch: a single firm tick when turning on, a double tick when
  // turning off, so a device switching off feels different without looking.
  toggle(on: boolean): void {
    vibrate(on ? 14 : [9, 40, 9])
  },
}
