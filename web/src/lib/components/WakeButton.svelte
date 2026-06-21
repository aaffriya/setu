<script lang="ts">
  import { haptics } from '../haptics'

  // A single "Wake" button for a Wake-on-LAN device. Waking is fire-and-forget,
  // so the only feedback we can honestly give is whether the magic packet was
  // dispatched: onWake resolves true on success, and only then do we show the
  // brief "Sent" pulse (a failure surfaces the app's global error instead).
  let { onWake }: { onWake: () => Promise<boolean> } = $props()

  let busy = $state(false)
  let sent = $state(false)
  let timer: ReturnType<typeof setTimeout> | undefined

  async function wake() {
    if (busy) return
    haptics.tap()
    busy = true
    const ok = await onWake()
    busy = false
    if (!ok) return
    sent = true
    clearTimeout(timer)
    timer = setTimeout(() => (sent = false), 1400)
  }
</script>

<button
  type="button"
  class="inline-flex items-center gap-1.5 rounded-full px-3.5 py-1.5 text-sm font-medium transition active:scale-95 disabled:cursor-not-allowed disabled:opacity-50 {sent
    ? 'bg-emerald-500/15 text-emerald-600 dark:text-emerald-300'
    : 'bg-indigo-500/15 text-indigo-600 hover:bg-indigo-500/25 dark:text-indigo-300'}"
  disabled={busy}
  onclick={wake}
  aria-label="Wake"
>
  {#if sent}
    <svg class="h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      <path d="M20 6 9 17l-5-5" />
    </svg>
    Sent
  {:else}
    <svg class="h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      <path d="M12 2v10" />
      <path d="M18.4 6.6a9 9 0 1 1-12.8 0" />
    </svg>
    Wake
  {/if}
</button>
