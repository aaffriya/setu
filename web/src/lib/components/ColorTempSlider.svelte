<script lang="ts">
  import Slider from './Slider.svelte'
  import { sliderCommit } from '../slider-commit.svelte'

  // White color-temperature control (Kelvin). Warm (left) → cool (right). Same
  // drag-override + debounced commit as the other value sliders.
  let {
    value = 0,
    min = 2200,
    max = 6500,
    disabled = false,
    onChange,
  }: {
    value?: number
    min?: number
    max?: number
    disabled?: boolean
    onChange?: (kelvin: number) => void
  } = $props()

  const drag = sliderCommit(120, (v) => onChange?.(v))
  // Fall back to a neutral 2700 K when the bulb isn't in white mode, then keep
  // both that fallback and any stale cached value inside this device's range.
  const display = $derived(Math.min(max, Math.max(min, drag.dragging ?? (value || 2700))))
</script>

<div class="flex items-center gap-3">
  <!-- Thermometer: the conventional "temperature" glyph (distinct from the
       brightness sun). Filled bulb + mercury column reads at 16px. -->
  <svg class="h-4 w-4 shrink-0 text-ink/50" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
    <path d="M10 13.6V5a2 2 0 1 1 4 0v8.6a3.5 3.5 0 1 1-4 0z" />
    <path d="M12 9v5.4" />
    <circle cx="12" cy="16.6" r="1.7" fill="currentColor" stroke="none" />
  </svg>
  <Slider
    {min}
    {max}
    step={100}
    value={display}
    {disabled}
    label="Color temperature"
    trackClass="setu-temp"
    oninput={drag.input}
  />
  <span class="w-12 text-right text-sm tabular-nums text-ink/60">{display}K</span>
</div>
