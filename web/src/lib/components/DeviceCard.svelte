<script lang="ts">
  import type { Device, Color } from '../api'
  import { command } from '../store'
  import Toggle from './Toggle.svelte'
  import BrightnessSlider from './BrightnessSlider.svelte'
  import ColorPicker from './ColorPicker.svelte'

  // Renders one device entirely from its data + capabilities — no per-device
  // markup. Adding a device type to the backend lights up the right controls
  // here automatically.
  let { device }: { device: Device } = $props()

  let caps = $derived(new Set(device.capabilities))
  let offline = $derived(!device.state.online)
  let on = $derived(device.state.on)
  let color = $derived(device.state.color)

  // Tint the card with the bulb's color when it's on, for a lit-up feel.
  let glow = $derived(
    on && caps.has('color')
      ? `0 14px 40px -12px rgba(${color.r}, ${color.g}, ${color.b}, 0.55)`
      : '0 12px 30px -16px rgba(0,0,0,0.6)',
  )

  const toggle = (v: boolean) => command(device.id, v ? 'on' : 'off')
  const setBrightness = (v: number) => command(device.id, 'set_brightness', v)
  const setColor = (c: Color) => command(device.id, 'set_color', c)
</script>

<article
  class="rounded-3xl border border-white/10 bg-white/[0.06] p-5 backdrop-blur-xl transition-shadow duration-500"
  style={`box-shadow: ${glow}`}
  class:opacity-60={offline}
>
  <header class="flex items-start justify-between gap-3">
    <div class="min-w-0">
      <h2 class="truncate text-lg font-semibold leading-tight">{device.name || device.id}</h2>
      <p class="mt-0.5 truncate text-xs text-white/45">
        {device.brand} · {device.model}
        {#if offline}<span class="text-rose-300/80"> · offline</span>{/if}
      </p>
    </div>
    {#if caps.has('switch')}
      <Toggle checked={on} disabled={offline} label={device.name || device.id} onToggle={toggle} />
    {/if}
  </header>

  {#if caps.has('brightness') || caps.has('color')}
    <div class="mt-5 space-y-4">
      {#if caps.has('brightness')}
        <BrightnessSlider value={device.state.brightness} disabled={offline || !on} onChange={setBrightness} />
      {/if}
      {#if caps.has('color')}
        <ColorPicker {color} disabled={offline || !on} onPick={setColor} />
      {/if}
    </div>
  {/if}
</article>
