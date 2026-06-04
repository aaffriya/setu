<script lang="ts">
  import type { App } from '../api'

  // One-tap launchers for a device's apps (e.g. a TV's streaming apps). Buttons
  // come from the device's `apps` list, so this stays device-agnostic: a device
  // that reports the `app` capability lights these up with no per-device markup.
  let {
    apps = [],
    disabled = false,
    onLaunch,
  }: {
    apps?: App[]
    disabled?: boolean
    onLaunch?: (id: string) => void
  } = $props()
</script>

{#if apps.length}
  <div class="grid grid-cols-2 gap-1.5">
    {#each apps as app (app.id)}
      <button
        class="setu-key h-9 truncate px-2 text-xs font-medium"
        {disabled}
        onclick={() => onLaunch?.(app.id)}
      >
        {app.name}
      </button>
    {/each}
  </div>
{/if}
