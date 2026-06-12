<script lang="ts">
  import { haptics } from '../haptics'

  // A TV remote: D-pad, media transport, channels, navigation, and input/source.
  // A tap sends a Samsung-style key via onKey ("key" command). On hold-capable
  // devices (`key_hold`) every button also supports press-and-hold: keeping it
  // down past HOLD_MS sends onKeyDown ("key_down" → Press) and lifting sends
  // onKeyUp ("key_up" → Release) — the TV then auto-repeats the key itself
  // (fast scroll). Safety: a missed lift (backgrounded tab, dropped network)
  // can't stick the key — the backend watchdog always releases — but we still
  // release eagerly on pointercancel / hidden tab / pagehide so it never waits
  // that long. Driven purely by capabilities — no per-device markup.
  let {
    disabled = false,
    holdable = false,
    onKey,
    onKeyDown,
    onKeyUp,
  }: {
    disabled?: boolean
    holdable?: boolean
    onKey?: (key: string) => void
    onKeyDown?: (key: string) => void
    onKeyUp?: (key: string) => void
  } = $props()

  // Press longer than this becomes a hold; shorter stays a normal tap.
  const HOLD_MS = 350

  let held: string | null = null // key currently held down on the TV
  let wasHold = false // swallow the synthetic click that follows a hold
  let timer: ReturnType<typeof setTimeout> | undefined

  function endHold() {
    clearTimeout(timer)
    if (held) {
      const k = held
      held = null
      wasHold = true
      onKeyUp?.(k)
    }
  }

  // Per-button handlers, spread onto each <button>. Click stays the tap path
  // (works for keyboard/AT too); pointer events only detect and run the hold.
  function press(key: string) {
    return {
      onclick: () => {
        if (wasHold) {
          wasHold = false
          return
        }
        haptics.tap()
        onKey?.(key)
      },
      onpointerdown: (e: PointerEvent) => {
        if (disabled || !holdable) return
        wasHold = false
        ;(e.currentTarget as HTMLElement).setPointerCapture?.(e.pointerId)
        clearTimeout(timer)
        timer = setTimeout(() => {
          held = key
          haptics.toggle(true) // firmer tick: the hold has engaged
          onKeyDown?.(key)
        }, HOLD_MS)
      },
      onpointerup: endHold,
      onpointercancel: endHold,
      oncontextmenu: (e: Event) => {
        if (holdable) e.preventDefault() // mobile long-press menu would break the hold
      },
    }
  }

  // If the tab is backgrounded mid-hold the pointerup never arrives — release
  // immediately rather than leaning on the backend watchdog.
  $effect(() => {
    const onHide = () => {
      if (document.hidden) endHold()
    }
    document.addEventListener('visibilitychange', onHide)
    window.addEventListener('pagehide', endHold)
    return () => {
      document.removeEventListener('visibilitychange', onHide)
      window.removeEventListener('pagehide', endHold)
      endHold()
    }
  })
</script>

<div class="space-y-3">
  <!-- D-pad: a roomier gap than the other rows so neighbouring arrows aren't
       mis-tapped (button size unchanged — only the spacing grew). -->
  <div class="mx-auto grid w-48 grid-cols-3 gap-3">
    <span></span>
    <button class="setu-key h-11" {disabled} {...press('KEY_UP')} aria-label="Up">▲</button>
    <span></span>

    <button class="setu-key h-11" {disabled} {...press('KEY_LEFT')} aria-label="Left">◀</button>
    <button class="setu-key h-11 font-semibold" {disabled} {...press('KEY_ENTER')} aria-label="OK">OK</button>
    <button class="setu-key h-11" {disabled} {...press('KEY_RIGHT')} aria-label="Right">▶</button>

    <span></span>
    <button class="setu-key h-11" {disabled} {...press('KEY_DOWN')} aria-label="Down">▼</button>
    <span></span>
  </div>

  <!-- Media transport -->
  <div class="grid grid-cols-4 gap-1.5">
    <button class="setu-key h-9" {disabled} {...press('KEY_REWIND')} aria-label="Rewind">
      <svg class="h-4 w-4" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
        <path d="M11 6 4 12l7 6V6z" /><path d="M18 6l-7 6 7 6V6z" />
      </svg>
    </button>
    <button class="setu-key h-9" {disabled} {...press('KEY_PLAY')} aria-label="Play">
      <svg class="h-4 w-4" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
        <path d="M8 6 18 12 8 18z" />
      </svg>
    </button>
    <button class="setu-key h-9" {disabled} {...press('KEY_PAUSE')} aria-label="Pause">
      <svg class="h-4 w-4" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
        <rect x="7" y="6" width="3.4" height="12" rx="1" /><rect x="13.6" y="6" width="3.4" height="12" rx="1" />
      </svg>
    </button>
    <button class="setu-key h-9" {disabled} {...press('KEY_FF')} aria-label="Fast forward">
      <svg class="h-4 w-4" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
        <path d="M13 6l7 6-7 6V6z" /><path d="M6 6l7 6-7 6V6z" />
      </svg>
    </button>
  </div>

  <!-- Back / Home / Menu / Exit -->
  <div class="grid grid-cols-4 gap-1.5">
    <button class="setu-key h-9" {disabled} {...press('KEY_RETURN')} aria-label="Back">
      <svg class="h-5 w-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        <path d="M9 14 4 9l5-5" /><path d="M4 9h11a5 5 0 0 1 0 10h-4" />
      </svg>
    </button>
    <button class="setu-key h-9" {disabled} {...press('KEY_HOME')} aria-label="Home">
      <svg class="h-5 w-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        <path d="M4 11.5 12 5l8 6.5" /><path d="M6 10.5V20h12v-9.5" />
      </svg>
    </button>
    <button class="setu-key h-9" {disabled} {...press('KEY_MENU')} aria-label="Menu">
      <svg class="h-5 w-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" aria-hidden="true">
        <path d="M4 7h16M4 12h16M4 17h16" />
      </svg>
    </button>
    <button class="setu-key h-9" {disabled} {...press('KEY_EXIT')} aria-label="Exit">
      <svg class="h-5 w-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        <path d="M13 5H7a2 2 0 0 0-2 2v10a2 2 0 0 0 2 2h6" /><path d="M10 12h10" /><path d="M17 9l3 3-3 3" />
      </svg>
    </button>
  </div>

  <!-- Channel down / Source / Channel up (HDMI lives in the app/shortcut grid above) -->
  <div class="grid grid-cols-3 gap-1.5">
    <button class="setu-key h-9" {disabled} {...press('KEY_CHDOWN')} aria-label="Channel down">
      <svg class="h-5 w-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        <path d="M7 7l5 5 5-5" /><path d="M7 13l5 5 5-5" />
      </svg>
    </button>
    <button class="setu-key h-9" {disabled} {...press('KEY_SOURCE')} aria-label="Source">
      <svg class="h-5 w-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        <path d="M4 9h13" /><path d="M14 6l3 3-3 3" /><path d="M20 15H7" /><path d="M10 12l-3 3 3 3" />
      </svg>
    </button>
    <button class="setu-key h-9" {disabled} {...press('KEY_CHUP')} aria-label="Channel up">
      <svg class="h-5 w-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        <path d="M7 17l5-5 5 5" /><path d="M7 11l5-5 5 5" />
      </svg>
    </button>
  </div>
</div>
