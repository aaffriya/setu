<script lang="ts">
  import { onDestroy } from 'svelte'
  import { fade, fly } from 'svelte/transition'
  import { devices } from '../store'
  import {
    createUser,
    deleteUser,
    listUsers,
    rotateUserToken,
    updateUser,
    type Role,
    type User,
  } from '../api'
  import { haptics } from '../haptics'
  import { trapFocus } from '../focus-trap'

  // People who may use this installation besides the administrator.
  //
  // Only a name is asked for. There is no password to choose, because signing in
  // is a token Setu generates — and that token is shown exactly once, so this
  // screen has to put it in front of the person before moving on.

  let {
    disabled = false,
    onmodalchange = () => {},
  }: { disabled?: boolean; onmodalchange?: (open: boolean) => void } = $props()

  let open = $state(false)
  let loading = $state(false)
  let saving = $state(false)
  let message = $state('')
  let users = $state<User[]>([])
  let draft = $state<{ id: string; name: string; role: Role; devices: string[] } | null>(null)
  let issued = $state<{ name: string; token: string } | null>(null)
  let confirmDelete = $state('')

  $effect(() => onmodalchange(open))
  onDestroy(() => onmodalchange(false))

  $effect(() => {
    if (!open) return
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return
      event.preventDefault()
      event.stopPropagation()
      if (issued) issued = null
      else if (draft) draft = null
      else open = false
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  })

  const roles: Array<{ value: Role; label: string; hint: string }> = [
    { value: 'read', label: 'Control', hint: 'Use the devices below. Cannot add devices or write automations.' },
    { value: 'modify', label: 'Manage', hint: 'Also add devices and write automations — still only for the devices below.' },
  ]

  async function show() {
    open = true
    draft = null
    issued = null
    confirmDelete = ''
    await load()
  }

  async function load() {
    loading = true
    message = ''
    try {
      users = await listUsers()
    } catch (error) {
      message = error instanceof Error ? error.message : 'Could not load accounts.'
    } finally {
      loading = false
    }
  }

  function newUser() {
    draft = { id: '', name: '', role: 'read', devices: [] }
    message = ''
    // Leaving a half-armed "Remove" behind means the next tap on it deletes,
    // with the warning tap having happened before a detour into the editor.
    confirmDelete = ''
  }

  function editUser(user: User) {
    draft = { id: user.id, name: user.name, role: user.role, devices: [...user.devices] }
    message = ''
    confirmDelete = ''
  }

  function toggleDevice(id: string) {
    if (!draft) return
    draft.devices = draft.devices.includes(id)
      ? draft.devices.filter((item) => item !== id)
      : [...draft.devices, id]
  }

  async function saveDraft() {
    if (!draft || saving || !draft.name.trim()) return
    saving = true
    message = ''
    try {
      const result = draft.id
        ? await updateUser(draft.id, {
            name: draft.name.trim(),
            role: draft.role,
            devices: draft.devices,
          })
        : await createUser(draft.name.trim(), draft.role, draft.devices)
      await load()
      // A new account's token exists only in this response. Show it before the
      // editor closes, or it is lost and has to be rotated.
      if (result.token) issued = { name: result.user.name, token: result.token }
      draft = null
    } catch (error) {
      message = error instanceof Error ? error.message : 'Could not save the account.'
    } finally {
      saving = false
    }
  }

  async function rotate(user: User) {
    saving = true
    message = ''
    try {
      const result = await rotateUserToken(user.id)
      issued = { name: result.user.name, token: result.token ?? '' }
      await load()
    } catch (error) {
      message = error instanceof Error ? error.message : 'Could not issue a new token.'
    } finally {
      saving = false
    }
  }

  async function remove(user: User) {
    if (confirmDelete !== user.id) {
      confirmDelete = user.id
      return
    }
    saving = true
    message = ''
    try {
      await deleteUser(user.id)
      confirmDelete = ''
      await load()
    } catch (error) {
      message = error instanceof Error ? error.message : 'Could not remove the account.'
    } finally {
      saving = false
    }
  }

  function copyToken() {
    if (!issued) return
    if (!navigator.clipboard) {
      message = 'Clipboard is unavailable; copy the token manually.'
      return
    }
    void navigator.clipboard.writeText(issued.token).then(
      () => (message = 'Token copied.'),
      () => (message = 'Clipboard permission was denied.'),
    )
  }

  function deviceName(id: string): string {
    return $devices.find((device) => device.id === id)?.name || id
  }

  function summary(user: User): string {
    const role = user.role === 'modify' ? 'Manage' : 'Control'
    if (user.devices.length === 0) return `${role} · no devices yet`
    return `${role} · ${user.devices.length} device${user.devices.length === 1 ? '' : 's'}`
  }
</script>

<button
  type="button"
  {disabled}
  onclick={() => {
    haptics.tap()
    void show()
  }}
  class="flex w-full items-center gap-3 rounded-xl bg-ink/5 px-3 py-2.5 text-left transition hover:bg-ink/10 disabled:opacity-40"
>
  <span class="grid h-8 w-8 shrink-0 place-items-center rounded-lg bg-indigo-500/10 text-indigo-500 dark:text-indigo-300">
    <svg class="h-[18px] w-[18px]" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      <path d="M16 20v-1.5a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4V20" /><circle cx="9" cy="7" r="3.5" /><path d="M17 11h5M19.5 8.5v5" />
    </svg>
  </span>
  <span class="min-w-0 flex-1">
    <span class="block text-sm font-medium text-ink/75">People</span>
    <span class="block text-xs text-ink/40">Accounts, tokens and device access</span>
  </span>
  <span class="text-lg text-ink/30" aria-hidden="true">›</span>
</button>

{#if open}
  <!-- svelte-ignore a11y_click_events_have_key_events -->
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <div
    class="fixed inset-0 z-40 grid place-items-center overflow-hidden overscroll-none bg-black/50 p-3 backdrop-blur-sm"
    transition:fade={{ duration: 150 }}
    onclick={(event) => event.target === event.currentTarget && (open = false)}
  >
    <div
      class="flex max-h-[92dvh] w-full min-w-0 max-w-lg flex-col overflow-hidden rounded-3xl border border-ink/10 bg-panel p-4 shadow-2xl min-[360px]:p-5"
      role="dialog"
      aria-modal="true"
      aria-label="People"
      tabindex="-1"
      use:trapFocus
    >
      <div class="flex items-center gap-2">
        {#if draft}
          <button type="button" onclick={() => (draft = null)} class="grid h-8 w-8 shrink-0 place-items-center rounded-full bg-ink/5 text-ink/65" aria-label="Back to people">←</button>
        {/if}
        <div class="min-w-0 flex-1">
          <h2 class="truncate text-lg font-semibold">{draft ? (draft.id ? 'Edit person' : 'Add person') : 'People'}</h2>
          <p class="truncate text-xs text-ink/45">Each person signs in with their own token.</p>
        </div>
        <button type="button" onclick={() => (open = false)} class="grid h-8 w-8 shrink-0 place-items-center rounded-full bg-ink/5 text-ink/60" aria-label="Close people">×</button>
      </div>

      {#if loading}
        <div class="grid min-h-40 place-items-center text-sm text-ink/45">Loading…</div>
      {:else if draft}
        <div class="mt-4 min-h-0 min-w-0 flex-1 space-y-4 overflow-x-hidden overflow-y-auto overscroll-contain px-0.5 pb-0.5">
          <label class="block text-xs text-ink/55">Name
            <input bind:value={draft.name} maxlength="32" placeholder="Priya" class="mt-1 w-full rounded-xl border border-ink/10 bg-ink/5 px-3 py-2 text-sm outline-none ring-indigo-400/50 focus:ring-2" />
          </label>

          <div>
            <span class="text-xs text-ink/55">What they may do</span>
            <div class="mt-1 grid grid-cols-2 gap-1 rounded-xl bg-ink/5 p-1">
              {#each roles as role (role.value)}
                <button
                  type="button"
                  onclick={() => draft && (draft.role = role.value)}
                  aria-pressed={draft.role === role.value}
                  class="rounded-lg py-2 text-xs font-medium {draft.role === role.value ? 'bg-panel shadow-sm' : 'text-ink/50'}"
                >
                  {role.label}
                </button>
              {/each}
            </div>
            <p class="mt-1.5 text-[11px] leading-relaxed text-ink/40">
              {roles.find((role) => role.value === draft?.role)?.hint}
            </p>
          </div>

          <div>
            <div class="flex items-center justify-between">
              <span class="text-xs text-ink/55">Devices they can see</span>
              <button
                type="button"
                onclick={() => draft && (draft.devices = draft.devices.length === $devices.length ? [] : $devices.map((device) => device.id))}
                class="text-xs font-medium text-indigo-500"
              >
                {draft.devices.length === $devices.length && $devices.length > 0 ? 'Clear all' : 'Select all'}
              </button>
            </div>
            {#if $devices.length === 0}
              <p class="mt-1 rounded-xl bg-ink/[0.03] p-4 text-center text-xs text-ink/45">
                No devices yet. Add one first, then share it here.
              </p>
            {:else}
              <div class="mt-1 space-y-1">
                {#each $devices as device (device.id)}
                  <label class="flex items-center gap-2 rounded-lg bg-ink/[0.03] px-2.5 py-2 text-xs">
                    <input
                      type="checkbox"
                      checked={draft.devices.includes(device.id)}
                      onchange={() => toggleDevice(device.id)}
                      class="h-4 w-4 accent-indigo-500"
                    />
                    <span class="min-w-0 flex-1 truncate">{device.name || device.id}</span>
                    <span class="shrink-0 text-[10px] text-ink/35">{device.brand}</span>
                  </label>
                {/each}
              </div>
            {/if}
            <p class="mt-1.5 text-[11px] leading-relaxed text-ink/40">
              Nothing is shared by default, and a device added later has to be shared here too.
            </p>
          </div>
        </div>
        <div class="mt-4 flex gap-2">
          <button type="button" onclick={() => (draft = null)} class="flex-1 rounded-xl bg-ink/5 py-2.5 text-sm font-medium text-ink/65">Cancel</button>
          <button type="button" onclick={saveDraft} disabled={saving || !draft.name.trim()} class="flex-1 rounded-xl bg-indigo-500 py-2.5 text-sm font-semibold text-white disabled:opacity-40">
            {saving ? 'Saving…' : draft.id ? 'Save' : 'Create and show token'}
          </button>
        </div>
      {:else}
        <div class="mt-4 flex items-center gap-2">
          <p class="min-w-0 flex-1 text-xs text-ink/45">
            You are the administrator: your token comes from <code class="rounded bg-ink/10 px-1">SETU_TOKEN</code>.
          </p>
          <button type="button" onclick={newUser} disabled={saving} class="shrink-0 rounded-xl bg-indigo-500 px-3 py-2 text-xs font-semibold text-white disabled:opacity-40">+ Add person</button>
        </div>

        <div class="mt-3 min-h-0 min-w-0 flex-1 space-y-1.5 overflow-x-hidden overflow-y-auto overscroll-contain">
          {#if users.length === 0}
            <div class="rounded-2xl bg-ink/[0.03] p-6 text-center text-sm text-ink/45">
              Nobody else yet. Add a person to give them their own token and a few devices.
            </div>
          {:else}
            {#each users as user (user.id)}
              <div class="min-w-0 rounded-2xl border border-ink/10 bg-ink/[0.025] p-3" in:fly={{ y: 5, duration: 120 }}>
                <div class="flex min-w-0 items-center gap-2">
                  <button type="button" onclick={() => editUser(user)} class="min-w-0 flex-1 text-left">
                    <span class="block truncate text-sm font-medium">{user.name}</span>
                    <span class="block text-[11px] text-ink/40">{summary(user)}</span>
                  </button>
                  <button type="button" onclick={() => rotate(user)} disabled={saving} class="shrink-0 rounded-lg bg-indigo-500/10 px-2 py-1.5 text-[11px] font-medium text-indigo-600 disabled:opacity-30 dark:text-indigo-300">New token</button>
                  <button
                    type="button"
                    onclick={() => remove(user)}
                    disabled={saving}
                    class="shrink-0 rounded-lg px-2 py-1.5 text-[11px] font-medium {confirmDelete === user.id ? 'bg-rose-500 text-white' : 'text-rose-500'}"
                  >
                    {confirmDelete === user.id ? 'Confirm' : 'Remove'}
                  </button>
                </div>
                {#if user.devices.length}
                  <p class="mt-1.5 truncate text-[11px] text-ink/40">{user.devices.map(deviceName).join(', ')}</p>
                {/if}
              </div>
            {/each}
          {/if}
        </div>
      {/if}

      {#if issued}
        <div class="mt-3 rounded-xl border border-amber-500/25 bg-amber-500/10 p-3">
          <p class="text-xs font-medium text-ink/70">
            {issued.name}’s token — copy it now, it will not be shown again.
          </p>
          <code class="mt-1 block break-all text-[10px] text-ink/55">{issued.token}</code>
          <div class="mt-2 flex gap-2">
            <button type="button" onclick={copyToken} class="rounded-lg bg-amber-500 px-3 py-1.5 text-xs font-semibold text-white">Copy token</button>
            <button type="button" onclick={() => (issued = null)} class="rounded-lg bg-ink/5 px-3 py-1.5 text-xs font-medium text-ink/60">Done</button>
          </div>
          <p class="mt-1.5 text-[11px] leading-relaxed text-ink/45">
            They enter it in Settings → Access token on their own device. A lost token is replaced with “New token”.
          </p>
        </div>
      {/if}
      {#if message}
        <p class="mt-3 text-xs {message.includes('copied') ? 'text-emerald-600 dark:text-emerald-300' : 'text-rose-600 dark:text-rose-300'}">{message}</p>
      {/if}
    </div>
  </div>
{/if}
