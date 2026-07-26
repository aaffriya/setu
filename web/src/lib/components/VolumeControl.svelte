<script lang="ts">
  import { haptics } from '../haptics'
  import Slider from './Slider.svelte'
  import { sliderCommit } from '../slider-commit.svelte'

  // Absolute volume slider (0–100). The backend sets and reads the level over
  // UPnP, so `value` is the TV's real volume (re-synced every poll) — no
  // tracked estimate. The drag-override + debounced commit is shared with the
  // other value sliders, with a longer delay here because each command is a
  // UPnP round trip. The speaker button toggles mute; `muted` is the real state
  // read back from the TV, so the icon always tells the truth.
  let {
    value = 0,
    muted = false,
    disabled = false,
    onChange,
    onMute,
  }: {
    value?: number
    muted?: boolean
    disabled?: boolean
    onChange?: (pct: number) => void
    onMute?: () => void
  } = $props()

  const drag = sliderCommit(160, (v) => onChange?.(v))
  const display = $derived(drag.dragging ?? value)
</script>

<div class="flex items-center gap-3">
  <button
    class="setu-key h-9 w-10 shrink-0"
    class:text-rose-500={muted}
    {disabled}
    onclick={() => {
      haptics.tap()
      onMute?.()
    }}
    aria-label={muted ? 'Unmute' : 'Mute'}
    aria-pressed={muted}
  >
    {#if muted}
      <svg class="h-5 w-5" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
        <path d="M11 5 6 9H3v6h3l5 4V5z" />
        <path d="m15.5 9.5 5 5M20.5 9.5l-5 5" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" fill="none" />
      </svg>
    {:else}
      <svg class="h-5 w-5" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
        <path d="M11 5 6 9H3v6h3l5 4V5z" />
        <path d="M15 8.5a4 4 0 0 1 0 7M17.5 6a7 7 0 0 1 0 12" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" fill="none" />
      </svg>
    {/if}
  </button>
  <Slider min={0} max={100} value={display} {disabled} label="Volume" oninput={drag.input} />
  <span class="w-9 text-right text-sm tabular-nums text-ink/60">{display}%</span>
</div>
