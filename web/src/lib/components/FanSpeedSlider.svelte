<script lang="ts">
  import Slider from './Slider.svelte'
  import { sliderCommit } from '../slider-commit.svelte'

  // Fan speed — discrete steps, not a percentage, so the readout is "4/6"
  // rather than a made-up %. The range comes from the device (Atomberg fans
  // report 1–6, where the top step is boost). Same drag-override + debounced
  // commit as the other value sliders — see slider-commit.svelte.ts.
  let {
    value = 0,
    min = 1,
    max = 6,
    disabled = false,
    onChange,
  }: {
    value?: number
    min?: number
    max?: number
    disabled?: boolean
    onChange?: (value: number) => void
  } = $props()

  const drag = sliderCommit(120, (v) => onChange?.(v))
  // A fan that has never reported a speed would otherwise park the thumb below
  // the track's minimum.
  const display = $derived(drag.dragging ?? Math.max(value, min))
</script>

<div class="flex items-center gap-3">
  <svg class="h-4 w-4 shrink-0 text-ink/50" viewBox="0 0 24 24" fill="none" aria-hidden="true">
    <circle cx="12" cy="12" r="1.8" fill="currentColor" />
    <path
      d="M12 10.2c0-2.6-.7-5.2-2.6-5.2-1.6 0-2.6 1.3-2.6 2.9 0 2.4 2.8 3.4 5.2 3.4M13.8 12c2.6 0 5.2-.7 5.2-2.6 0-1.6-1.3-2.6-2.9-2.6-2.4 0-3.4 2.8-3.4 5.2M12 13.8c0 2.6.7 5.2 2.6 5.2 1.6 0 2.6-1.3 2.6-2.9 0-2.4-2.8-3.4-5.2-3.4M10.2 12c-2.6 0-5.2.7-5.2 2.6 0 1.6 1.3 2.6 2.9 2.6 2.4 0 3.4-2.8 3.4-5.2"
      stroke="currentColor"
      stroke-width="1.4"
      stroke-linejoin="round"
    />
  </svg>
  <Slider {min} {max} step={1} value={display} {disabled} label="Fan speed" oninput={drag.input} />
  <span class="w-9 text-right text-sm tabular-nums text-ink/60">{display}/{max}</span>
</div>
