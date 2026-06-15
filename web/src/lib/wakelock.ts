// Screen Wake Lock — keeps the display awake while the app is driven as a remote
// (a TV card open, RemotePad / TextEntry in use). Feature-detected and fail-soft,
// exactly like haptics.ts: silent where unsupported (older iOS, non-secure http).
//
// Two details the platform forces on us:
//   - The lock is auto-released whenever the page is hidden, so we re-acquire on
//     visibilitychange → visible (and store.ts calls through resume() on return).
//   - Several open remotes can want it at once, so it's reference-counted: the
//     lock is held while at least one caller has request()ed it.

// Minimal local types so we don't depend on lib.dom shipping WakeLock yet.
interface WakeLockSentinelLike {
  release(): Promise<void>
  addEventListener(type: 'release', cb: () => void): void
}
interface WakeLockLike {
  request(type: 'screen'): Promise<WakeLockSentinelLike>
}

function wakeLockApi(): WakeLockLike | undefined {
  if (typeof navigator === 'undefined') return undefined
  return (navigator as Navigator & { wakeLock?: WakeLockLike }).wakeLock
}

let sentinel: WakeLockSentinelLike | null = null
let holders = 0

async function acquire(): Promise<void> {
  const api = wakeLockApi()
  // Nothing to hold, already held, unsupported, or the page is hidden (the
  // request would reject) — bail quietly.
  if (!api || holders === 0 || sentinel || document.visibilityState !== 'visible') return
  try {
    const s = await api.request('screen')
    // A request that resolved after we no longer want it (or after the page
    // went hidden) is stale — drop it immediately.
    if (holders === 0) {
      void s.release().catch(() => {})
      return
    }
    sentinel = s
    // The platform releases the lock on its own when the tab hides; clear our
    // handle so a later foreground re-acquires instead of holding a dead one.
    s.addEventListener('release', () => {
      if (sentinel === s) sentinel = null
    })
  } catch {
    // denied / unsupported / hidden — the screen just won't be kept awake
    sentinel = null
  }
}

function drop(): void {
  const s = sentinel
  sentinel = null
  void s?.release().catch(() => {})
}

export const wakeLock = {
  // Ask to keep the screen awake. Pair every request() with exactly one release().
  request(): void {
    holders++
    void acquire()
  },
  // Give up one request; the lock drops when the last holder releases.
  release(): void {
    holders = Math.max(0, holders - 1)
    if (holders === 0) drop()
  },
}

// Re-acquire when the tab returns to the foreground (the platform dropped the
// lock while hidden). resume() in store.ts covers the same path on mobile.
if (typeof document !== 'undefined') {
  document.addEventListener('visibilitychange', () => {
    if (document.visibilityState === 'visible') void acquire()
  })
}
