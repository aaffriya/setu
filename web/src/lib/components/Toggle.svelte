<script lang="ts">
  // An iOS-style on/off switch. Stateless: it renders `checked` and reports
  // intent via `onToggle`; the parent owns the state (optimistic update).
  let {
    checked = false,
    disabled = false,
    label = 'Toggle',
    onToggle,
  }: {
    checked?: boolean
    disabled?: boolean
    label?: string
    onToggle?: (value: boolean) => void
  } = $props()
</script>

<button
  type="button"
  role="switch"
  aria-checked={checked}
  aria-label={label}
  {disabled}
  onclick={() => onToggle?.(!checked)}
  class="relative inline-flex h-8 w-14 shrink-0 items-center rounded-full transition-colors duration-300 disabled:cursor-not-allowed disabled:opacity-40
         {checked ? 'bg-emerald-400/90' : 'bg-ink/15'}"
>
  <span
    class="inline-block h-6 w-6 transform rounded-full bg-white shadow-md transition-transform duration-300
           {checked ? 'translate-x-7' : 'translate-x-1'}"
  ></span>
</button>
