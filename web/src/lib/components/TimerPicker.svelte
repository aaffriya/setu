<script lang="ts">
  import { haptics } from '../haptics'

  // Auto-off timer. The durations come from the device (an Atomberg fan accepts
  // 1, 2, 3 or 6 hours), so this component stays device-agnostic. 0 is always
  // offered as "Off" — cancelling a running timer has to be reachable.
  let {
    options = [],
    value = 0,
    elapsedMins = 0,
    disabled = false,
    onPick,
  }: {
    options?: number[]
    value?: number
    elapsedMins?: number
    disabled?: boolean
    onPick?: (hours: number) => void
  } = $props()

  const durations = $derived(options.length ? options : [0])

  // Show how much of a running timer is left, so the card answers "when does
  // this turn off?" rather than only "what did I set?".
  const remaining = $derived.by(() => {
    if (value <= 0) return ''
    const left = value * 60 - elapsedMins
    if (left <= 0) return 'ending'
    if (left < 60) return `${left}m left`
    const hours = Math.floor(left / 60)
    const mins = left % 60
    return mins ? `${hours}h ${mins}m left` : `${hours}h left`
  })

  function handle(event: Event) {
    haptics.tap()
    onPick?.(Number((event.target as HTMLSelectElement).value))
  }

  const label = (hours: number) => (hours === 0 ? 'Off' : `${hours} hour${hours > 1 ? 's' : ''}`)
</script>

<div class="flex items-center gap-3">
  <svg
    class="h-4 w-4 shrink-0 text-ink/50"
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    stroke-width="1.6"
    aria-hidden="true"
  >
    <circle cx="12" cy="13" r="8" />
    <path d="M12 9v4l2.5 2M9 2h6" stroke-linecap="round" />
  </svg>
  <select
    class="w-full appearance-none rounded-xl border border-ink/10 bg-ink/5 px-3 py-2 text-sm text-ink/90 outline-none transition focus:border-indigo-400/50 disabled:cursor-not-allowed disabled:opacity-40"
    {value}
    {disabled}
    onchange={handle}
    aria-label="Auto-off timer"
  >
    {#each durations as hours (hours)}
      <option value={hours}>{label(hours)}</option>
    {/each}
  </select>
  {#if remaining}
    <span class="shrink-0 text-xs tabular-nums text-ink/45">{remaining}</span>
  {/if}
</div>
