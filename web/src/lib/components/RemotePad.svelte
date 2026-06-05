<script lang="ts">
  import { haptics } from '../haptics'

  // A TV remote: D-pad, media transport, channels, navigation, and input/source.
  // Every button sends a Samsung-style key via onKey; the parent maps that to a
  // "key" command. Driven purely by the `key` capability — any key-capable device
  // shows the same remote, no per-device markup.
  let {
    disabled = false,
    onKey,
  }: {
    disabled?: boolean
    onKey?: (key: string) => void
  } = $props()

  const press = (key: string) => () => {
    haptics.tap()
    onKey?.(key)
  }
</script>

<div class="space-y-3">
  <!-- D-pad -->
  <div class="mx-auto grid w-44 grid-cols-3 gap-1.5">
    <span></span>
    <button class="setu-key h-11" {disabled} onclick={press('KEY_UP')} aria-label="Up">▲</button>
    <span></span>

    <button class="setu-key h-11" {disabled} onclick={press('KEY_LEFT')} aria-label="Left">◀</button>
    <button class="setu-key h-11 font-semibold" {disabled} onclick={press('KEY_ENTER')} aria-label="OK">OK</button>
    <button class="setu-key h-11" {disabled} onclick={press('KEY_RIGHT')} aria-label="Right">▶</button>

    <span></span>
    <button class="setu-key h-11" {disabled} onclick={press('KEY_DOWN')} aria-label="Down">▼</button>
    <span></span>
  </div>

  <!-- Media transport -->
  <div class="grid grid-cols-4 gap-1.5 text-sm">
    <button class="setu-key h-9" {disabled} onclick={press('KEY_REWIND')} aria-label="Rewind">⏪</button>
    <button class="setu-key h-9" {disabled} onclick={press('KEY_PLAY')} aria-label="Play">▶</button>
    <button class="setu-key h-9" {disabled} onclick={press('KEY_PAUSE')} aria-label="Pause">⏸</button>
    <button class="setu-key h-9" {disabled} onclick={press('KEY_FF')} aria-label="Fast forward">⏩</button>
  </div>

  <!-- Channels -->
  <div class="grid grid-cols-3 gap-1.5 text-xs">
    <button class="setu-key h-9" {disabled} onclick={press('KEY_CHDOWN')} aria-label="Channel down">CH −</button>
    <button class="setu-key h-9" {disabled} onclick={press('KEY_CH_LIST')}>List</button>
    <button class="setu-key h-9" {disabled} onclick={press('KEY_CHUP')} aria-label="Channel up">CH +</button>
  </div>

  <!-- Back / Home / Menu / Exit -->
  <div class="grid grid-cols-4 gap-1.5 text-xs">
    <button class="setu-key h-9" {disabled} onclick={press('KEY_RETURN')} aria-label="Back">
      <svg class="h-5 w-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        <path d="M9 14 4 9l5-5" /><path d="M4 9h11a5 5 0 0 1 0 10h-4" />
      </svg>
    </button>
    <button class="setu-key h-9" {disabled} onclick={press('KEY_HOME')} aria-label="Home">
      <svg class="h-5 w-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        <path d="M4 11.5 12 5l8 6.5" /><path d="M6 10.5V20h12v-9.5" />
      </svg>
    </button>
    <button class="setu-key h-9" {disabled} onclick={press('KEY_MENU')} aria-label="Menu">
      <svg class="h-5 w-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" aria-hidden="true">
        <path d="M4 7h16M4 12h16M4 17h16" />
      </svg>
    </button>
    <button class="setu-key h-9" {disabled} onclick={press('KEY_EXIT')}>Exit</button>
  </div>

  <!-- Source / inputs (HDMI lives in the app/shortcut grid above) -->
  <div class="grid grid-cols-2 gap-1.5 text-xs">
    <button class="setu-key h-9" {disabled} onclick={press('KEY_SOURCE')}>Source</button>
    <button class="setu-key h-9" {disabled} onclick={press('KEY_TV')}>TV</button>
  </div>
</div>
