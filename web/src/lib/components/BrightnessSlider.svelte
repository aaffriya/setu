<script lang="ts">
  import Slider from './Slider.svelte'
  import { sliderCommit } from '../slider-commit.svelte'

  // Brightness control (0–100). The drag-override + debounced commit is shared
  // with the other value sliders — see slider-commit.svelte.ts.
  let {
    value = 0,
    disabled = false,
    onChange,
  }: {
    value?: number
    disabled?: boolean
    onChange?: (value: number) => void
  } = $props()

  const drag = sliderCommit(120, (v) => onChange?.(v))
  const display = $derived(drag.dragging ?? value)
</script>

<div class="flex items-center gap-3">
  <svg class="h-4 w-4 shrink-0 text-ink/50" viewBox="0 0 24 24" fill="none" aria-hidden="true">
    <circle cx="12" cy="12" r="4" fill="currentColor" />
    <path d="M12 2v2.5M12 19.5V22M2 12h2.5M19.5 12H22M4.9 4.9l1.8 1.8M17.3 17.3l1.8 1.8M4.9 19.1l1.8-1.8M17.3 6.7l1.8-1.8" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" />
  </svg>
  <Slider min={0} max={100} value={display} {disabled} label="Brightness" oninput={drag.input} />
  <span class="w-9 text-right text-sm tabular-nums text-ink/60">{display}%</span>
</div>
