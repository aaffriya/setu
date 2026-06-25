/// <reference types="svelte" />
/// <reference types="vite/client" />

// Set by the inline splash watchdog in index.html; cleared once the app mounts.
interface Window {
  __setuSplashWatchdog?: ReturnType<typeof setTimeout>
}
