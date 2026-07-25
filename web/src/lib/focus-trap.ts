const focusable = [
  'button:not([disabled])',
  'input:not([disabled])',
  'select:not([disabled])',
  'textarea:not([disabled])',
  'a[href]',
  '[tabindex]:not([tabindex="-1"])',
].join(',')

// Keep Tab inside the active dialog and return focus to its opener on close.
// `enabled` lets the Settings dialog yield while one child dialog is open.
export function trapFocus(node: HTMLElement, enabled = true) {
  let active = enabled
  const previous = document.activeElement instanceof HTMLElement ? document.activeElement : null

  function items(): HTMLElement[] {
    return [...node.querySelectorAll<HTMLElement>(focusable)].filter(
      (item) => item.getClientRects().length > 0,
    )
  }

  function focusDialog() {
    queueMicrotask(() => {
      // Start on the dialog only when one of its children has not deliberately
      // focused itself (for example, the name field in the New Scene editor).
      if (active && !node.contains(document.activeElement)) {
        node.focus({ preventScroll: true })
      }
    })
  }

  function onKeydown(event: KeyboardEvent) {
    if (!active || event.key !== 'Tab') return
    const list = items()
    if (list.length === 0) {
      event.preventDefault()
      node.focus()
      return
    }
    const first = list[0]
    const last = list[list.length - 1]
    if (document.activeElement === node) {
      event.preventDefault()
      const target = event.shiftKey ? last : first
      target.focus()
    } else if (event.shiftKey && document.activeElement === first) {
      event.preventDefault()
      last.focus()
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault()
      first.focus()
    }
  }

  node.addEventListener('keydown', onKeydown)
  if (active) focusDialog()

  return {
    update(next: boolean) {
      const wasActive = active
      active = next
      if (!wasActive && active) focusDialog()
    },
    destroy() {
      node.removeEventListener('keydown', onKeydown)
      // An outgoing dialog can overlap its successor during a transition.
      // Restore the opener only while this dialog still owns focus; otherwise
      // leave deliberate focus in the newly opened dialog untouched.
      const stillOwnsFocus =
        node.contains(document.activeElement) || document.activeElement === document.body
      if (stillOwnsFocus && previous?.isConnected) previous.focus()
    },
  }
}
