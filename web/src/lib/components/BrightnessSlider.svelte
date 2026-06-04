<script lang="ts">
  // Brightness control (0–100). While dragging we show a local override so the
  // label tracks the thumb instantly; the command is debounced so we don't flood
  // the device. Once sent, we drop the override and follow the server value again
  // (which the optimistic update has already set to the same number).
  let {
    value = 0,
    disabled = false,
    onChange,
  }: {
    value?: number
    disabled?: boolean
    onChange?: (value: number) => void
  } = $props()

  let dragging = $state<number | null>(null)
  const display = $derived(dragging ?? value)

  let debounce: ReturnType<typeof setTimeout> | undefined
  function handle(event: Event) {
    const v = Number((event.target as HTMLInputElement).value)
    dragging = v
    clearTimeout(debounce)
    debounce = setTimeout(() => {
      onChange?.(v)
      dragging = null
    }, 120)
  }
</script>

<div class="flex items-center gap-3">
  <svg class="h-4 w-4 shrink-0 text-ink/50" viewBox="0 0 24 24" fill="none" aria-hidden="true">
    <circle cx="12" cy="12" r="4" fill="currentColor" />
    <path d="M12 2v2.5M12 19.5V22M2 12h2.5M19.5 12H22M4.9 4.9l1.8 1.8M17.3 17.3l1.8 1.8M4.9 19.1l1.8-1.8M17.3 6.7l1.8-1.8" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" />
  </svg>
  <input
    class="setu-range w-full"
    type="range"
    min="0"
    max="100"
    value={display}
    {disabled}
    oninput={handle}
    aria-label="Brightness"
  />
  <span class="w-9 text-right text-sm tabular-nums text-ink/60">{display}%</span>
</div>
