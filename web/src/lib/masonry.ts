// Masonry packing for the device grid. CSS grid has no native masonry yet, so a
// tall card (an expanded TV remote) would otherwise inflate its whole row and
// strand the short cards beside it — leaving big vertical gaps. This action makes
// every card span as many fine row-tracks as its height needs, so cards pack
// tightly column-by-column while the grid keeps its fixed-width, centered columns
// (and svelte's flip + drag-to-reorder keep working — they only touch transforms).
//
// Fails safe: the action OWNS `grid-auto-rows` (the fine track), so if it never
// runs the grid is just the normal one-row-per-card layout — never a crushed,
// 8px-tall grid. The initial pass is synchronous (no requestAnimationFrame, which
// is throttled on backgrounded/headless pages), then a ResizeObserver re-packs on
// any size change and a MutationObserver tracks added/removed cards. No library.

// 1px tracks with no row gap give an exact result: a card spans (its height + the
// gap) tracks, so the empty space below every card is precisely GAP — no rounding
// to a coarse track, which would leave uneven space under some cards. The visual
// gap is created by the span, not row-gap, so the action sets row-gap to 0.
const ROW = 1 // grid-auto-rows track unit (px)
const GAP = 16 // vertical gap between cards (matches the grid's gap-4 = 16px)

export function masonry(node: HTMLElement) {
  function layout() {
    node.style.gridAutoRows = `${ROW}px`
    node.style.rowGap = '0px'
    for (const child of node.children) {
      const el = child as HTMLElement
      // Top-align so the item measures its true content height (not stretched to
      // its spanned tracks) — keeps offsetHeight stable so writing the span can't
      // feed back into another resize.
      if (el.style.alignSelf !== 'start') el.style.alignSelf = 'start'
      // offsetHeight ignores the drag-lift transform, so dragging never resizes.
      // span = content height + the gap (in 1px tracks) → exactly GAP of space
      // below each card, with the next card tucked right under it.
      const span = Math.max(1, el.offsetHeight + GAP)
      const value = `span ${span}`
      // Only write when it actually changes: avoids a ResizeObserver feedback loop
      // when the container's own size shifts as a result of re-spanning.
      if (el.dataset.mspan !== value) {
        el.dataset.mspan = value
        el.style.gridRowEnd = value
      }
    }
  }

  // Re-pack when the container resizes (column count changes) or any card grows
  // or shrinks (expand/collapse).
  const ro = new ResizeObserver(layout)
  ro.observe(node)
  const observeChildren = () => {
    for (const child of node.children) ro.observe(child)
  }
  observeChildren()

  // Re-pack and observe when cards are added/removed (filter, search, new device).
  const mo = new MutationObserver(() => {
    observeChildren()
    layout()
  })
  mo.observe(node, { childList: true })

  layout()

  return {
    destroy() {
      ro.disconnect()
      mo.disconnect()
      node.style.gridAutoRows = ''
      node.style.rowGap = ''
      for (const child of node.children) {
        const el = child as HTMLElement
        el.style.alignSelf = ''
        el.style.gridRowEnd = ''
        delete el.dataset.mspan
      }
    },
  }
}
