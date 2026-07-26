// The commit policy every value slider shares: while the finger is moving, show
// the dragged value locally and hold the command back; once the drag settles for
// `delayMs`, send it once and fall back to the server value again (the optimistic
// update has already set it to the same number).
//
// This was four identical copies — brightness, colour temperature, scene speed
// and volume. The behaviour is deliberate and unchanged, including:
//   - trailing-only: nothing is sent while the finger keeps moving, so a drag
//     costs one command instead of a burst at the device;
//   - the per-control delay, which is longer for volume because that command is
//     a UPnP round trip rather than a single UDP packet;
//   - no cancellation on unmount: a value the user already dragged to is still
//     worth sending even if the card is collapsed right after.

import { haptics } from './haptics'

export type SliderCommit = {
  /** The value currently being dragged, or null once the drag has settled. */
  readonly dragging: number | null
  /** Feed one `oninput` from the Slider. */
  input: (value: number) => void
}

export function sliderCommit(delayMs: number, commit: (value: number) => void): SliderCommit {
  let dragging = $state<number | null>(null)
  let timer: ReturnType<typeof setTimeout> | undefined

  return {
    get dragging() {
      return dragging
    },
    input(value: number) {
      dragging = value
      haptics.slide()
      clearTimeout(timer)
      timer = setTimeout(() => {
        commit(value)
        dragging = null
      }, delayMs)
    },
  }
}
