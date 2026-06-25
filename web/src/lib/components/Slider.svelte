<script lang="ts">
  // Pointer-driven slider — replaces the native <input type="range">, whose track
  // is dead to taps on touch (iOS only moves on a thumb drag, never on a track
  // tap). Here a pointerdown ANYWHERE on the track jumps the value to that spot
  // and starts a drag, and the thumb then follows the finger no matter where the
  // gesture began — you never have to grab the knob. Emits `oninput(v)`
  // continuously, exactly like the native element did, so callers keep their own
  // drag-override + debounce (see BrightnessSlider et al.).
  let {
    value = 0,
    min = 0,
    max = 100,
    step = 1,
    disabled = false,
    label,
    trackClass = '',
    oninput,
  }: {
    value?: number
    min?: number
    max?: number
    step?: number
    disabled?: boolean
    label?: string
    trackClass?: string
    oninput?: (value: number) => void
  } = $props()

  let el = $state<HTMLDivElement>()
  let active = $state(false)

  const clamp = (v: number) => Math.min(max, Math.max(min, v))
  const snap = (v: number) => clamp(Math.round((v - min) / step) * step + min)
  const pct = $derived(((clamp(value) - min) / (max - min)) * 100)

  // Map a pointer's X to a snapped value across the track's width.
  function valueAt(clientX: number): number {
    if (!el) return value
    const r = el.getBoundingClientRect()
    const frac = r.width > 0 ? (clientX - r.left) / r.width : 0
    return snap(min + frac * (max - min))
  }

  // Only fire when the snapped value actually changes — the parent updates
  // `value` from each emit, so this also dedupes sub-step pointer jitter.
  function emit(v: number) {
    if (v !== value) oninput?.(v)
  }

  function down(e: PointerEvent) {
    // Ignore a second finger landing mid-drag — one pointer owns the slider.
    if (disabled || active) return
    e.preventDefault()
    el?.focus()
    // Capture so the drag keeps tracking even if the finger leaves the track;
    // guarded because a synthetic/stale pointer id can throw, and that must not
    // abort the value update below.
    try {
      el?.setPointerCapture(e.pointerId)
    } catch {
      /* non-fatal */
    }
    active = true
    emit(valueAt(e.clientX))
  }
  function move(e: PointerEvent) {
    if (!active) return
    emit(valueAt(e.clientX))
  }
  function up(e: PointerEvent) {
    if (!active) return
    active = false
    try {
      el?.releasePointerCapture(e.pointerId)
    } catch {
      /* non-fatal */
    }
  }

  function key(e: KeyboardEvent) {
    if (disabled) return
    let v = value
    if (e.key === 'ArrowRight' || e.key === 'ArrowUp') v = snap(value + step)
    else if (e.key === 'ArrowLeft' || e.key === 'ArrowDown') v = snap(value - step)
    else if (e.key === 'Home') v = min
    else if (e.key === 'End') v = max
    else return
    e.preventDefault()
    emit(v)
  }
</script>

<div
  bind:this={el}
  class="setu-slider"
  class:is-dragging={active}
  class:is-disabled={disabled}
  role="slider"
  tabindex={disabled ? -1 : 0}
  aria-label={label}
  aria-orientation="horizontal"
  aria-valuemin={min}
  aria-valuemax={max}
  aria-valuenow={Math.round(value)}
  aria-disabled={disabled}
  onpointerdown={down}
  onpointermove={move}
  onpointerup={up}
  onpointercancel={up}
  onkeydown={key}
>
  <div class="setu-slider-track {trackClass}">
    <div class="setu-slider-thumb" style="left: {pct}%"></div>
  </div>
</div>
