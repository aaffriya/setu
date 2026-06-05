<script lang="ts">
  import type { Color } from '../api'
  import { haptics } from '../haptics'

  // A row of preset swatches plus a native color input for anything custom.
  let {
    color = { r: 255, g: 255, b: 255 },
    disabled = false,
    onPick,
  }: {
    color?: Color
    disabled?: boolean
    onPick?: (color: Color) => void
  } = $props()

  const presets: Color[] = [
    { r: 255, g: 87, b: 87 },
    { r: 255, g: 168, b: 76 },
    { r: 255, g: 233, b: 122 },
    { r: 122, g: 224, b: 145 },
    { r: 96, g: 178, b: 246 },
    { r: 167, g: 139, b: 250 },
    { r: 255, g: 255, b: 255 },
  ]

  const hex2 = (n: number) => n.toString(16).padStart(2, '0')
  const toHex = (c: Color) => `#${hex2(c.r)}${hex2(c.g)}${hex2(c.b)}`
  const fromHex = (s: string): Color => ({
    r: parseInt(s.slice(1, 3), 16),
    g: parseInt(s.slice(3, 5), 16),
    b: parseInt(s.slice(5, 7), 16),
  })

  const sameColor = (a: Color, b: Color) => a.r === b.r && a.g === b.g && a.b === b.b
</script>

<div class="flex flex-wrap items-center gap-2">
  {#each presets as preset (toHex(preset))}
    <button
      type="button"
      {disabled}
      onclick={() => {
        haptics.tap()
        onPick?.(preset)
      }}
      aria-label={`Set color ${toHex(preset)}`}
      class="h-7 w-7 rounded-full ring-2 transition hover:scale-110 disabled:opacity-40
             {sameColor(preset, color) ? 'ring-ink' : 'ring-ink/15'}"
      style={`background: rgb(${preset.r} ${preset.g} ${preset.b})`}
    ></button>
  {/each}

  <label
    class="relative h-7 w-7 cursor-pointer overflow-hidden rounded-full ring-2 ring-ink/15"
    style="background: conic-gradient(from 0deg, #f87171, #fbbf24, #34d399, #60a5fa, #a78bfa, #f472b6, #f87171)"
    aria-label="Custom color"
  >
    <input
      type="color"
      value={toHex(color)}
      {disabled}
      oninput={(e) => {
        haptics.slide()
        onPick?.(fromHex((e.target as HTMLInputElement).value))
      }}
      class="absolute -inset-2 h-12 w-12 cursor-pointer appearance-none border-0 bg-transparent p-0 opacity-0"
    />
  </label>
</div>
