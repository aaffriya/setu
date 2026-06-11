<script lang="ts">
  import { haptics } from '../haptics'

  // Sends typed text to a text field focused on the device (`text` capability →
  // "send_text"). The device reports its field over state: `active` means a
  // field is focused right now (TV keyboard open) and `value` mirrors what's
  // been typed into it so far — including with the physical remote — streamed
  // live over the WebSocket. The row stays usable even when `active` is false
  // (focus events can be missed); the TV simply ignores text with no field.
  let {
    value = '',
    active = false,
    disabled = false,
    onSend,
  }: {
    value?: string
    active?: boolean
    disabled?: boolean
    onSend?: (text: string) => void
  } = $props()

  let draft = $state('')

  function submit(e: SubmitEvent) {
    e.preventDefault()
    const text = draft.trim()
    if (!text) return
    haptics.tap()
    onSend?.(text)
    draft = ''
  }
</script>

<form class="space-y-1" onsubmit={submit}>
  <div class="flex items-center gap-1.5">
    <input
      class="h-9 min-w-0 flex-1 rounded-xl border bg-ink/5 px-3 text-sm text-ink/80 outline-none transition placeholder:text-ink/35 focus:bg-ink/10 disabled:cursor-not-allowed disabled:opacity-40
        {active ? 'border-sky-400/60' : 'border-transparent'}"
      type="text"
      maxlength="255"
      enterkeyhint="send"
      placeholder={active ? 'TV input focused — type here…' : 'Type to TV…'}
      bind:value={draft}
      {disabled}
      aria-label="Text to send to the device"
    />
    <button class="setu-key h-9 w-10 shrink-0" type="submit" disabled={disabled || !draft.trim()} aria-label="Send text">
      <svg class="h-5 w-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        <path d="M5 12h13" /><path d="m13 7 5 5-5 5" />
      </svg>
    </button>
  </div>
  {#if active && value}
    <p class="truncate px-1 text-xs text-ink/45">On TV: <span class="text-ink/70">{value}</span></p>
  {/if}
</form>
