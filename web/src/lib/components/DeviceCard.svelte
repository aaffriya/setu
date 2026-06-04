<script lang="ts">
  import type { Device, Color, DeviceState } from '../api'
  import { command } from '../store'
  import Toggle from './Toggle.svelte'
  import BrightnessSlider from './BrightnessSlider.svelte'
  import ColorPicker from './ColorPicker.svelte'
  import ColorTempSlider from './ColorTempSlider.svelte'
  import ScenePicker from './ScenePicker.svelte'
  import SceneSpeedSlider from './SceneSpeedSlider.svelte'
  import Favorites from './Favorites.svelte'
  import VolumeControl from './VolumeControl.svelte'
  import RemotePad from './RemotePad.svelte'
  import AppShortcuts from './AppShortcuts.svelte'

  // Renders one device entirely from its data + capabilities — no per-device
  // markup. Adding a device type to the backend lights up the right controls
  // here automatically.
  let { device }: { device: Device } = $props()

  let caps = $derived(new Set(device.capabilities))
  let offline = $derived(!device.state.online)
  let on = $derived(device.state.on)
  let color = $derived(device.state.color)

  // The colour glowing behind the card reflects the bulb's *current* look. A
  // tunable-white bulb keeps its last RGB value while in white mode, so the glow
  // can't just read `color` — a colour → temperature switch would leave the
  // stale colour glowing. Derive the tint from whichever mode is active instead.
  const TEMP_MIN = 2200
  const TEMP_MAX = 6500
  // Warm → cool stops matching the .setu-temp slider track, so the glow tracks
  // exactly what the temperature slider shows.
  const TEMP_STOPS: Color[] = [
    { r: 255, g: 157, b: 60 }, // 2200K warm  (#ff9d3c)
    { r: 255, g: 217, b: 160 }, //             (#ffd9a0)
    { r: 255, g: 255, b: 255 }, // neutral      (#ffffff)
    { r: 207, g: 227, b: 255 }, //             (#cfe3ff)
    { r: 156, g: 196, b: 255 }, // 6500K cool  (#9cc4ff)
  ]

  function kelvinToColor(kelvin: number): Color {
    const t = Math.min(1, Math.max(0, (kelvin - TEMP_MIN) / (TEMP_MAX - TEMP_MIN)))
    const pos = t * (TEMP_STOPS.length - 1)
    const i = Math.min(TEMP_STOPS.length - 2, Math.floor(pos))
    const f = pos - i
    const lo = TEMP_STOPS[i]
    const hi = TEMP_STOPS[i + 1]
    const lerp = (a: number, b: number) => Math.round(a + (b - a) * f)
    return { r: lerp(lo.r, hi.r), g: lerp(lo.g, hi.g), b: lerp(lo.b, hi.b) }
  }

  // White-temperature mode (color_temp > 0) wins, then RGB colour; a scene falls
  // back to the last colour. null → no tint, use the neutral card shadow.
  function tintFor(state: DeviceState, caps: Set<string>): Color | null {
    if (caps.has('color_temp') && state.color_temp > 0) return kelvinToColor(state.color_temp)
    if (caps.has('color')) return state.color
    return null
  }

  let tint = $derived(tintFor(device.state, caps))
  let glow = $derived(
    on && tint
      ? `0 14px 40px -12px rgba(${tint.r}, ${tint.g}, ${tint.b}, 0.55)`
      : 'var(--card-shadow)',
  )

  const toggle = (v: boolean) => command(device.id, v ? 'on' : 'off')
  const setBrightness = (v: number) => command(device.id, 'set_brightness', v)
  const setColor = (c: Color) => command(device.id, 'set_color', c)
  const setColorTemp = (k: number) => command(device.id, 'set_color_temp', k)
  const setScene = (id: number) => command(device.id, 'set_scene', id)
  const setSceneSpeed = (v: number) => command(device.id, 'set_scene_speed', v)
  const sendKey = (key: string) => command(device.id, 'key', key)
  const launchApp = (id: string) => command(device.id, 'launch_app', id)

  let hasLight = $derived(
    caps.has('brightness') || caps.has('color') || caps.has('color_temp') || caps.has('scene'),
  )
  // Speed only applies to dynamic (animated) scenes; show the slider only then.
  let activeScene = $derived(device.scenes?.find((s) => s.id === device.state.scene))
  let hasMedia = $derived(caps.has('volume') || caps.has('key') || caps.has('app'))
  // Media controls (volume / remote keys / app shortcuts) need the TV powered on
  // and reachable, so they're gated on online + power. The power toggle itself
  // stays usable while off: a TV reports online even when off (it can be woken by
  // Wake-on-LAN), so off ≠ offline and you can always turn it back on.
  let mediaDisabled = $derived(offline || !on)
</script>

<article
  class="rounded-3xl border border-ink/10 bg-ink/[0.06] p-5 backdrop-blur-xl transition-shadow duration-500"
  style={`box-shadow: ${glow}`}
  class:opacity-60={offline}
>
  <header class="flex items-start justify-between gap-3">
    <div class="min-w-0">
      <h2 class="truncate text-lg font-semibold leading-tight">{device.name || device.id}</h2>
      <p class="mt-0.5 truncate text-xs text-ink/45">
        {device.brand} · {device.model}
        {#if offline}<span class="text-rose-500 dark:text-rose-300/80"> · offline</span>{/if}
      </p>
    </div>
    {#if caps.has('switch')}
      <Toggle checked={on} disabled={offline} label={device.name || device.id} onToggle={toggle} />
    {/if}
  </header>

  {#if hasLight}
    <div class="mt-5 space-y-4">
      {#if caps.has('brightness')}
        <BrightnessSlider value={device.state.brightness} disabled={offline || !on} onChange={setBrightness} />
      {/if}
      {#if caps.has('color_temp')}
        <ColorTempSlider value={device.state.color_temp} disabled={offline || !on} onChange={setColorTemp} />
      {/if}
      {#if caps.has('color')}
        <ColorPicker {color} disabled={offline || !on} onPick={setColor} />
      {/if}
      {#if caps.has('scene')}
        <ScenePicker scenes={device.scenes ?? []} value={device.state.scene} disabled={offline || !on} onPick={setScene} />
        {#if activeScene?.dynamic}
          <SceneSpeedSlider value={device.state.scene_speed} disabled={offline || !on} onChange={setSceneSpeed} />
        {/if}
      {/if}
      <Favorites {device} disabled={offline} />
    </div>
  {/if}

  {#if hasMedia}
    <div class="mt-5 space-y-4">
      {#if caps.has('app')}
        <AppShortcuts apps={device.apps ?? []} disabled={mediaDisabled} onLaunch={launchApp} />
      {/if}
      {#if caps.has('volume')}
        <VolumeControl
          disabled={mediaDisabled}
          onVolumeDown={() => command(device.id, 'volume_down')}
          onMute={() => command(device.id, 'mute')}
          onVolumeUp={() => command(device.id, 'volume_up')}
        />
      {/if}
      {#if caps.has('key')}
        <RemotePad disabled={mediaDisabled} onKey={sendKey} />
      {/if}
    </div>
  {/if}
</article>
