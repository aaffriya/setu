<script lang="ts">
  import { haptics } from '../haptics'

  // Animation speed for dynamic scenes (slow → fast). WiZ range is 10–200.
  const MIN = 10
  const MAX = 200

  let {
    value = 0,
    disabled = false,
    onChange,
  }: {
    value?: number
    disabled?: boolean
    onChange?: (speed: number) => void
  } = $props()

  let dragging = $state<number | null>(null)
  const display = $derived(dragging ?? (value || 100))

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
  <!-- turtle (slow) -->
  <svg class="h-4 w-4 shrink-0 text-ink/55" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
    <path d="M5 15a6 4 0 0 1 12 0" />
    <path d="M11 11v4M8.4 12l-.6 3M13.6 12l.6 3" />
    <path d="M7 15v1.8M15 15v1.8" />
    <path d="M17 14c1.4 0 2.3-.6 2.3-1.5S18.4 11 18 11.4" />
  </svg>

  <input
    class="setu-range w-full"
    type="range"
    min={MIN}
    max={MAX}
    step="5"
    value={display}
    {disabled}
    oninput={handle}
    aria-label="Scene speed"
  />

  <!-- rabbit (fast) -->
  <svg class="h-4 w-4 shrink-0 text-ink/55" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
    <path d="M9 12c-1-2-1.4-6 .1-6.4S11 8 11 11" />
    <path d="M14 11c0-3 .8-5.4 2-4.9S16 10 15 12" />
    <circle cx="12" cy="15.5" r="3.5" />
    <circle cx="13.3" cy="14.8" r=".5" fill="currentColor" stroke="none" />
  </svg>
</div>
