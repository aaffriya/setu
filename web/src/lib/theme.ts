// Theme override. By default Setu follows the OS (pure CSS via
// prefers-color-scheme — no class, no flash). A user can force light or dark;
// that just toggles a class on <html> which app.css's :root.theme-* rules read.
// Stored in localStorage; applied in main.ts before mount so there's no flash.

export type Theme = 'system' | 'light' | 'dark'

const KEY = 'setu.theme'

export function getTheme(): Theme {
  try {
    const t = localStorage.getItem(KEY)
    if (t === 'light' || t === 'dark') return t
  } catch {
    // storage disabled — fall through to system
  }
  return 'system'
}

export function applyTheme(t: Theme): void {
  if (typeof document === 'undefined') return
  const el = document.documentElement
  el.classList.toggle('theme-dark', t === 'dark')
  el.classList.toggle('theme-light', t === 'light')
}

export function setTheme(t: Theme): void {
  try {
    if (t === 'system') localStorage.removeItem(KEY)
    else localStorage.setItem(KEY, t)
  } catch {
    // storage disabled — the choice just won't persist
  }
  applyTheme(t)
}
