<script lang="ts">
  // Relative volume control for devices that expose step up/down + mute (e.g. a
  // TV). There's no absolute level to display — these are momentary actions.
  let {
    disabled = false,
    onVolumeDown,
    onMute,
    onVolumeUp,
  }: {
    disabled?: boolean
    onVolumeDown?: () => void
    onMute?: () => void
    onVolumeUp?: () => void
  } = $props()
</script>

<div class="flex items-center gap-2">
  <span class="w-9 shrink-0 text-xs font-medium uppercase tracking-wider text-ink/40">Vol</span>
  <button class="setu-key h-10 flex-1 text-2xl leading-none" {disabled} onclick={onVolumeDown} aria-label="Volume down">−</button>
  <button class="setu-key h-10 w-12 shrink-0" {disabled} onclick={onMute} aria-label="Mute">
    <svg class="h-5 w-5" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
      <path d="M11 5 6 9H3v6h3l5 4V5z" />
      <path d="M16 9l5 6M21 9l-5 6" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" fill="none" />
    </svg>
  </button>
  <button class="setu-key h-10 flex-1 text-2xl leading-none" {disabled} onclick={onVolumeUp} aria-label="Volume up">+</button>
</div>
