<script lang="ts">
  // A compact remote: a D-pad (arrows + OK) plus Back / Home / Menu. Each button
  // sends a Samsung-style key via onKey; the parent maps that to a "key" command.
  let {
    disabled = false,
    onKey,
  }: {
    disabled?: boolean
    onKey?: (key: string) => void
  } = $props()

  const press = (key: string) => () => onKey?.(key)
</script>

<div class="space-y-3">
  <!-- D-pad -->
  <div class="mx-auto grid w-44 grid-cols-3 gap-1.5">
    <span></span>
    <button class="setu-key h-11" {disabled} onclick={press('KEY_UP')} aria-label="Up">▲</button>
    <span></span>

    <button class="setu-key h-11" {disabled} onclick={press('KEY_LEFT')} aria-label="Left">◀</button>
    <button class="setu-key h-11 font-semibold" {disabled} onclick={press('KEY_ENTER')} aria-label="OK">OK</button>
    <button class="setu-key h-11" {disabled} onclick={press('KEY_RIGHT')} aria-label="Right">▶</button>

    <span></span>
    <button class="setu-key h-11" {disabled} onclick={press('KEY_DOWN')} aria-label="Down">▼</button>
    <span></span>
  </div>

  <!-- Back / Home / Menu -->
  <div class="grid grid-cols-3 gap-1.5 text-xs">
    <button class="setu-key h-9" {disabled} onclick={press('KEY_RETURN')}>Back</button>
    <button class="setu-key h-9" {disabled} onclick={press('KEY_HOME')}>Home</button>
    <button class="setu-key h-9" {disabled} onclick={press('KEY_MENU')}>Menu</button>
  </div>

  <!-- Source / inputs -->
  <div class="grid grid-cols-2 gap-1.5 text-xs">
    <button class="setu-key h-9" {disabled} onclick={press('KEY_SOURCE')}>Source</button>
    <button class="setu-key h-9" {disabled} onclick={press('KEY_HDMI')}>HDMI</button>
  </div>
</div>
