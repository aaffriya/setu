<script lang="ts">
  import type { App } from '../api'
  import AppIcon from './AppIcon.svelte'
  import { haptics } from '../haptics'

  // One-tap shortcuts for a device: app launchers (from the device's `apps` list)
  // plus optional key shortcuts (e.g. HDMI input), rendered together in one grid.
  // Stays device-agnostic — driven by the `app` / `key` capabilities, no per-device
  // markup. Buttons are icon-only (brand logo); the name rides as the aria-label.
  let {
    apps = [],
    keys = [],
    disabled = false,
    onLaunch,
    onKey,
  }: {
    apps?: App[]
    keys?: { key: string; name: string }[]
    disabled?: boolean
    onLaunch?: (id: string) => void
    onKey?: (key: string) => void
  } = $props()
</script>

{#if apps.length || keys.length}
  <div class="grid grid-cols-3 gap-1.5">
    {#each apps as app (app.id)}
      <button
        class="setu-key h-11"
        {disabled}
        aria-label={app.name}
        title={app.name}
        onclick={() => {
          haptics.tap()
          onLaunch?.(app.id)
        }}
      >
        <AppIcon name={app.name} />
      </button>
    {/each}
    {#each keys as k (k.key)}
      <button
        class="setu-key h-11"
        {disabled}
        aria-label={k.name}
        title={k.name}
        onclick={() => {
          haptics.tap()
          onKey?.(k.key)
        }}
      >
        <AppIcon name={k.name} />
      </button>
    {/each}
  </div>
{/if}
