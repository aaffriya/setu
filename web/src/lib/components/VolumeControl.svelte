<script lang="ts">
  import { haptics } from '../haptics'

  // Absolute volume slider (0–100), like the brightness slider. The TV has no
  // absolute volume on its remote channel, so the backend tracks the level and
  // steps to the target with paced key presses; sliding fully to 0 or 100
  // re-calibrates. While dragging we show a local override so the % label tracks
  // the thumb instantly, debouncing the command so we send one target per drag.
  // Tap the speaker to mute.
  let {
    value = 0,
    disabled = false,
    onChange,
    onMute,
  }: {
    value?: number
    disabled?: boolean
    onChange?: (pct: number) => void
    onMute?: () => void
  } = $props()

  let dragging = $state<number | null>(null)
  const display = $derived(dragging ?? value)

  let debounce: ReturnType<typeof setTimeout> | undefined
  function handle(event: Event) {
    const v = Number((event.target as HTMLInputElement).value)
    dragging = v
    haptics.slide()
    clearTimeout(debounce)
    debounce = setTimeout(() => {
      onChange?.(v)
      dragging = null
    }, 160)
  }
</script>

<div class="flex items-center gap-3">
  <button
    class="setu-key h-9 w-10 shrink-0"
    {disabled}
    onclick={() => {
      haptics.tap()
      onMute?.()
    }}
    aria-label="Mute"
  >
    <svg class="h-5 w-5" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
      <path d="M11 5 6 9H3v6h3l5 4V5z" />
      <path d="M15 8.5a4 4 0 0 1 0 7M17.5 6a7 7 0 0 1 0 12" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" fill="none" />
    </svg>
  </button>
  <input
    class="setu-range flex-1"
    type="range"
    min="0"
    max="100"
    value={display}
    {disabled}
    oninput={handle}
    aria-label="Volume"
  />
  <span class="w-9 text-right text-sm tabular-nums text-ink/60">{display}%</span>
</div>
