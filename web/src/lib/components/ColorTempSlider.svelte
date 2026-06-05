<script lang="ts">
  import { haptics } from '../haptics'

  // White color-temperature control (Kelvin). Warm (left) → cool (right). Same
  // drag-override + debounce pattern as BrightnessSlider.
  const MIN = 2200
  const MAX = 6500

  let {
    value = 0,
    disabled = false,
    onChange,
  }: {
    value?: number
    disabled?: boolean
    onChange?: (kelvin: number) => void
  } = $props()

  let dragging = $state<number | null>(null)
  // Fall back to a neutral 2700 K for display when the bulb isn't in white mode.
  const display = $derived(dragging ?? (value || 2700))

  let debounce: ReturnType<typeof setTimeout> | undefined
  function handle(event: Event) {
    const v = Number((event.target as HTMLInputElement).value)
    dragging = v
    haptics.slide()
    clearTimeout(debounce)
    debounce = setTimeout(() => {
      onChange?.(v)
      dragging = null
    }, 120)
  }
</script>

<div class="flex items-center gap-3">
  <!-- Thermometer: the conventional "temperature" glyph (distinct from the
       brightness sun). Filled bulb + mercury column reads at 16px. -->
  <svg class="h-4 w-4 shrink-0 text-ink/50" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
    <path d="M10 13.6V5a2 2 0 1 1 4 0v8.6a3.5 3.5 0 1 1-4 0z" />
    <path d="M12 9v5.4" />
    <circle cx="12" cy="16.6" r="1.7" fill="currentColor" stroke="none" />
  </svg>
  <input
    class="setu-range setu-temp w-full"
    type="range"
    min={MIN}
    max={MAX}
    step="100"
    value={display}
    {disabled}
    oninput={handle}
    aria-label="Color temperature"
  />
  <span class="w-12 text-right text-sm tabular-nums text-ink/60">{display}K</span>
</div>
