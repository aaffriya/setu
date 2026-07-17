<script lang="ts">
  import type { Device, Color, DeviceState } from '../api'
  import { slide } from 'svelte/transition'
  import { command, expanded, toggleExpanded } from '../store'
  import { haptics } from '../haptics'
  import { wakeLock } from '../wakelock'
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
  import TextEntry from './TextEntry.svelte'
  import WakeButton from './WakeButton.svelte'

  // Renders one device entirely from its data + capabilities — no per-device
  // markup. Adding a device type to the backend lights up the right controls
  // here automatically.
  let { device }: { device: Device } = $props()

  let caps = $derived(new Set(device.capabilities))
  // A Wake-on-LAN device is just a MAC + a Wake button — no light, no media, and
  // nothing to expand.
  let isWol = $derived(caps.has('wol'))
  let offline = $derived(!device.state.online)
  let on = $derived(device.state.on)
  let color = $derived(device.state.color)

  // Collapsed by default: a card shows only its name, power, and the expand
  // chevron until opened. The open/closed state is persisted per device.
  let isOpen = $derived($expanded[device.id] ?? false)
  // Prefer a configured friendly series (e.g. "AU7700"); otherwise prettify the
  // driver model key ("color_bulb" → "Color Bulb") so the subtitle reads well.
  let modelLabel = $derived(
    device.series?.trim() ||
      device.model.replace(/[_-]+/g, ' ').replace(/\b\w/g, (c) => c.toUpperCase()),
  )

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

  // The glow behind the card mirrors what the device is doing:
  //  • a lit bulb glows in its own colour (warm/cool white in temperature mode);
  //  • a powered-on TV casts a cool, wide screen-light halo — two soft layers
  //    (blue spill + indigo ambient) so it reads as a panel spilling light, not
  //    a point source;
  //  • off / idle rests on the neutral card shadow.
  const TV_GLOW =
    '0 18px 52px -14px rgba(96, 165, 250, 0.5), 0 6px 26px -10px rgba(99, 102, 241, 0.34)'
  let glow = $derived.by(() => {
    if (!on) return 'var(--card-shadow)'
    if (tint) return `0 14px 40px -12px rgba(${tint.r}, ${tint.g}, ${tint.b}, 0.55)`
    if (caps.has('volume') || caps.has('key') || caps.has('app')) return TV_GLOW
    return 'var(--card-shadow)'
  })

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
  let hasMedia = $derived(
    caps.has('volume') || caps.has('key') || caps.has('app') || caps.has('text'),
  )
  // Media controls (volume / remote keys / app shortcuts) need the TV powered on
  // and reachable, so they're gated on online + power. The power toggle itself
  // stays usable while off: a TV reports online even when off (it can be woken by
  // Wake-on-LAN), so off ≠ offline and you can always turn it back on.
  let mediaDisabled = $derived(offline || !on)
  // HDMI rides in the shortcut grid (alongside apps) as a key shortcut, since the
  // protocol exposes it as KEY_HDMI rather than an app launch.
  let keyShortcuts = $derived(caps.has('key') ? [{ key: 'KEY_HDMI', name: 'HDMI' }] : [])

  // Keep the screen awake while this card is open and usable as a remote (TV keys
  // or text entry) — the phone/laptop is acting as a controller, so it shouldn't
  // dim. Reference-counted in wakelock.ts so several open remotes share one lock;
  // the effect's cleanup releases on collapse / unmount. Silent where unsupported.
  let usesRemote = $derived(isOpen && (caps.has('key') || caps.has('text')))
  $effect(() => {
    if (!usesRemote) return
    wakeLock.request()
    return () => wakeLock.release()
  })
</script>

<!-- Keep the glow static between state updates. A shadow transition restarts on
     store reconciliation and makes unrelated card edges appear to blink. -->
<article
  class="rounded-3xl border border-ink/10 bg-ink/[0.06] p-5 backdrop-blur-xl"
  style={`box-shadow: ${glow}`}
  class:opacity-60={offline}
>
  <header class="flex items-start justify-between gap-3">
    <div class="min-w-0">
      <h2 class="truncate text-lg font-semibold leading-tight">{device.name || device.id}</h2>
      {#if isWol}
        <p class="mt-0.5 truncate font-mono text-xs text-ink/45">{device.mac}</p>
      {:else if isOpen}
        <p class="mt-0.5 truncate text-xs text-ink/45">
          {device.brand} · {modelLabel}
          {#if offline}<span class="text-rose-500 dark:text-rose-300/80"> · offline</span>{/if}
        </p>
      {/if}
    </div>
    <div class="flex shrink-0 items-center gap-2">
      {#if caps.has('switch')}
        <Toggle checked={on} disabled={offline} label={device.name || device.id} onToggle={toggle} />
      {/if}
      {#if caps.has('wol')}
        <WakeButton onWake={() => command(device.id, 'wake')} />
      {/if}
      {#if hasLight || hasMedia}
        <button
          type="button"
          onclick={() => {
            haptics.tap()
            toggleExpanded(device.id)
          }}
          aria-expanded={isOpen}
          aria-label={isOpen ? `Collapse ${device.name || device.id}` : `Expand ${device.name || device.id}`}
          class="grid h-8 w-8 place-items-center rounded-full bg-ink/5 text-ink/60 transition hover:bg-ink/10 hover:text-ink"
        >
          <svg
            class="h-5 w-5 transition-transform duration-300 {isOpen ? 'rotate-180' : ''}"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            stroke-width="2"
            stroke-linecap="round"
            stroke-linejoin="round"
            aria-hidden="true"
          >
            <path d="m6 9 6 6 6-6" />
          </svg>
        </button>
      {/if}
    </div>
  </header>

  {#if isOpen && hasLight}
    <div class="mt-5 space-y-4" transition:slide={{ duration: 250 }}>
      {#if caps.has('brightness')}
        <BrightnessSlider value={device.state.brightness} disabled={offline || !on} onChange={setBrightness} />
      {/if}
      {#if caps.has('color_temp')}
        <ColorTempSlider
          value={device.state.color_temp}
          min={device.color_temp_min ?? 2200}
          max={device.color_temp_max ?? 6500}
          disabled={offline || !on}
          onChange={setColorTemp}
        />
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

  {#if isOpen && hasMedia}
    <div class="mt-5 space-y-4" transition:slide={{ duration: 250 }}>
      {#if caps.has('app') || keyShortcuts.length}
        <AppShortcuts
          apps={caps.has('app') ? (device.apps ?? []) : []}
          keys={keyShortcuts}
          disabled={mediaDisabled}
          onLaunch={launchApp}
          onKey={sendKey}
        />
      {/if}
      {#if caps.has('volume')}
        <VolumeControl
          value={device.state.volume}
          muted={device.state.muted}
          disabled={mediaDisabled}
          onChange={(v) => command(device.id, 'set_volume', v)}
          onMute={() => command(device.id, 'mute')}
        />
      {/if}
      {#if caps.has('key')}
        <RemotePad
          disabled={mediaDisabled}
          holdable={caps.has('key_hold')}
          onKey={sendKey}
          onKeyDown={(k) => command(device.id, 'key_down', k)}
          onKeyUp={(k) => command(device.id, 'key_up', k)}
        />
      {/if}
      {#if caps.has('text')}
        <TextEntry
          value={device.state.text_value}
          active={device.state.text_active}
          disabled={mediaDisabled}
          onSend={(t) => command(device.id, 'send_text', t)}
        />
      {/if}
    </div>
  {/if}
</article>
