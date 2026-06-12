<script lang="ts">
  import { onMount, onDestroy } from 'svelte'
  import { fade, fly } from 'svelte/transition'
  import { flip } from 'svelte/animate'
  import {
    devices,
    connection,
    lastError,
    refresh,
    connect,
    disconnect,
    resume,
    type ConnectionStatus,
  } from './lib/store'
  import { getToken, setToken } from './lib/api'
  import DeviceCard from './lib/components/DeviceCard.svelte'

  let token = $state(getToken())
  let tokenDraft = $state(getToken())
  let showSettings = $state(false)

  // Show the connect card when we have no token, or the server rejected it.
  let needsToken = $derived(!token || $connection === 'unauthorized')

  function onVisibility() {
    if (document.visibilityState === 'visible') resume()
  }

  onMount(() => {
    refresh()
    connect()
    document.addEventListener('visibilitychange', onVisibility)
    window.addEventListener('online', resume)
  })

  // Clean up listeners and the socket so nothing leaks (matters on mobile).
  onDestroy(() => {
    document.removeEventListener('visibilitychange', onVisibility)
    window.removeEventListener('online', resume)
    disconnect()
  })

  function saveToken() {
    token = tokenDraft.trim()
    setToken(token)
    showSettings = false
    refresh()
    // Drop any existing socket first: it authenticated with the previous
    // token, and connect() alone deliberately refuses to replace a live one
    // (the one-socket rule in store.ts).
    disconnect()
    connect()
  }

  const statusLabel: Record<ConnectionStatus, string> = {
    connecting: 'Connecting…',
    online: 'Live',
    offline: 'Offline',
    unauthorized: 'Locked',
  }
  const statusDot: Record<ConnectionStatus, string> = {
    connecting: 'bg-amber-400',
    online: 'bg-emerald-400',
    offline: 'bg-rose-400',
    unauthorized: 'bg-rose-400',
  }
</script>

<svelte:window onkeydown={(e) => showSettings && e.key === 'Escape' && (showSettings = false)} />

<div
  class="mx-auto flex min-h-[100dvh] max-w-3xl flex-col px-4 pb-16"
  style="padding-top: max(1.25rem, env(safe-area-inset-top))"
>
  <header class="flex items-center justify-between gap-3 py-4">
    <div class="flex items-center gap-3">
      <div class="grid h-11 w-11 place-items-center rounded-2xl bg-gradient-to-br from-indigo-500 to-fuchsia-500 shadow-lg shadow-indigo-500/30">
        <svg class="h-6 w-6 text-white" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true">
          <path d="M3 16h18" />
          <path d="M5 16a7 7 0 0114 0" />
          <path d="M12 9v7M8 12.5V16M16 12.5V16" />
        </svg>
      </div>
      <div>
        <h1 class="text-2xl font-semibold tracking-tight">Setu</h1>
        <p class="text-xs text-ink/45">सेतु</p>
      </div>
    </div>

    <div class="flex items-center gap-2">
      <div class="flex items-center gap-2 rounded-full bg-ink/5 px-3 py-1.5 text-xs text-ink/70">
        <span class="relative flex h-2 w-2">
          {#if $connection === 'connecting' || $connection === 'online'}
            <span class="absolute inline-flex h-full w-full animate-ping rounded-full opacity-60 {statusDot[$connection]}"></span>
          {/if}
          <span class="relative inline-flex h-2 w-2 rounded-full {statusDot[$connection]}"></span>
        </span>
        {statusLabel[$connection]}
      </div>
      <button
        onclick={() => {
          tokenDraft = token
          showSettings = true
        }}
        class="grid h-9 w-9 place-items-center rounded-full bg-ink/5 text-ink/70 transition hover:bg-ink/10 hover:text-ink"
        aria-label="Settings"
      >
        <svg class="h-5 w-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true">
          <circle cx="12" cy="12" r="3" />
          <path d="M19.4 15a1.65 1.65 0 00.33 1.82l.06.06a2 2 0 11-2.83 2.83l-.06-.06a1.65 1.65 0 00-1.82-.33 1.65 1.65 0 00-1 1.51V21a2 2 0 11-4 0v-.09A1.65 1.65 0 009 19.4a1.65 1.65 0 00-1.82.33l-.06.06a2 2 0 11-2.83-2.83l.06-.06a1.65 1.65 0 00.33-1.82 1.65 1.65 0 00-1.51-1H3a2 2 0 110-4h.09A1.65 1.65 0 004.6 9a1.65 1.65 0 00-.33-1.82l-.06-.06a2 2 0 112.83-2.83l.06.06a1.65 1.65 0 001.82.33H9a1.65 1.65 0 001-1.51V3a2 2 0 114 0v.09a1.65 1.65 0 001 1.51 1.65 1.65 0 001.82-.33l.06-.06a2 2 0 112.83 2.83l-.06.06a1.65 1.65 0 00-.33 1.82V9a1.65 1.65 0 001.51 1H21a2 2 0 110 4h-.09a1.65 1.65 0 00-1.51 1z" />
        </svg>
      </button>
    </div>
  </header>

  {#if needsToken}
    <div in:fade={{ duration: 200 }} class="mx-auto mt-12 w-full max-w-sm rounded-3xl border border-ink/10 bg-ink/[0.06] p-6 text-center backdrop-blur-xl">
      <div class="mx-auto mb-4 grid h-12 w-12 place-items-center rounded-2xl bg-ink/10">
        <svg class="h-6 w-6 text-ink/80" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true">
          <rect x="4" y="11" width="16" height="9" rx="2" />
          <path d="M8 11V7a4 4 0 118 0v4" />
        </svg>
      </div>
      <h2 class="text-lg font-semibold">Connect to Setu</h2>
      <p class="mt-1 text-sm text-ink/50">Enter the access token from your <code class="rounded bg-ink/10 px-1">config.yaml</code>.</p>
      <input
        class="mt-4 w-full rounded-xl border border-ink/10 bg-ink/5 px-4 py-2.5 text-center outline-none ring-indigo-400/50 focus:ring-2"
        type="password"
        placeholder="access token"
        bind:value={tokenDraft}
        onkeydown={(e) => e.key === 'Enter' && saveToken()}
      />
      <button
        onclick={saveToken}
        class="mt-3 w-full rounded-xl bg-gradient-to-r from-indigo-500 to-fuchsia-500 py-2.5 font-medium text-white shadow-lg shadow-indigo-500/30 transition hover:opacity-95 active:scale-[0.99]"
      >
        Connect
      </button>
    </div>
  {:else if $devices.length === 0}
    <div in:fade={{ duration: 200 }} class="flex flex-1 flex-col items-center justify-center py-20 text-center">
      <div class="grid h-20 w-20 place-items-center rounded-3xl bg-ink/5 ring-1 ring-ink/10">
        <svg class="h-10 w-10 text-ink/40" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" aria-hidden="true">
          <path d="M3 16h18" />
          <path d="M5 16a7 7 0 0114 0" />
          <path d="M12 9v7M8 12.5V16M16 12.5V16" />
        </svg>
      </div>
      <h2 class="mt-5 text-xl font-semibold">No devices yet</h2>
      <p class="mt-2 max-w-xs text-sm leading-relaxed text-ink/50">
        Add one in <code class="rounded bg-ink/10 px-1">config.yaml</code> by its brand, model and MAC — then restart Setu.
      </p>
    </div>
  {:else}
    <main class="grid grid-cols-1 gap-4 sm:grid-cols-2">
      {#each $devices as device (device.id)}
        <div in:fly={{ y: 14, duration: 250 }} animate:flip={{ duration: 200 }}>
          <DeviceCard {device} />
        </div>
      {/each}
    </main>
  {/if}
</div>

{#if $lastError && !needsToken}
  <div
    in:fly={{ y: 20, duration: 200 }}
    out:fade
    class="fixed inset-x-0 bottom-6 z-20 mx-auto w-fit max-w-[90vw] rounded-full border border-rose-500/30 bg-rose-500/15 px-4 py-2 text-sm text-rose-700 backdrop-blur-md dark:border-rose-400/30 dark:bg-rose-500/20 dark:text-rose-100"
  >
    {$lastError}
  </div>
{/if}

{#if showSettings}
  <!-- svelte-ignore a11y_click_events_have_key_events -->
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <div
    class="fixed inset-0 z-30 grid place-items-center bg-black/50 p-4 backdrop-blur-sm"
    transition:fade={{ duration: 150 }}
    onclick={(e) => e.target === e.currentTarget && (showSettings = false)}
  >
    <div
      class="w-full max-w-sm rounded-3xl border border-ink/10 bg-panel p-6 shadow-2xl"
      role="dialog"
      aria-modal="true"
      aria-label="Settings"
    >
      <h2 class="text-lg font-semibold">Settings</h2>
      <label class="mt-4 block text-sm text-ink/60" for="token-input">Access token</label>
      <input
        id="token-input"
        class="mt-1.5 w-full rounded-xl border border-ink/10 bg-ink/5 px-4 py-2.5 outline-none ring-indigo-400/50 focus:ring-2"
        type="password"
        placeholder="access token"
        bind:value={tokenDraft}
        onkeydown={(e) => e.key === 'Enter' && saveToken()}
      />
      <div class="mt-5 flex gap-2">
        <button
          onclick={() => (showSettings = false)}
          class="flex-1 rounded-xl bg-ink/5 py-2.5 font-medium text-ink/70 transition hover:bg-ink/10"
        >
          Cancel
        </button>
        <button
          onclick={saveToken}
          class="flex-1 rounded-xl bg-gradient-to-r from-indigo-500 to-fuchsia-500 py-2.5 font-medium text-white shadow-lg shadow-indigo-500/30 transition hover:opacity-95"
        >
          Save
        </button>
      </div>
    </div>
  </div>
{/if}
