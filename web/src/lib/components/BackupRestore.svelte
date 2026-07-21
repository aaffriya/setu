<script lang="ts">
  import { get } from 'svelte/store'
  import { devices } from '../store'
  import {
    backupSectionNames,
    createBackup,
    downloadBackup,
    readBackup,
    restoreBackup,
    type BackupSelection,
    type SetuBackup,
  } from '../backup'

  let selection = $state<BackupSelection>({
    favorites: true,
    rooms: true,
    scenes: true,
    appearance: true,
    automations: true,
  })
  let busy = $state(false)
  let message = $state('')
  let selected = $state<SetuBackup | null>(null)
  let allSelected = $derived(Object.values(selection).every(Boolean))

  const choices: Array<{ key: keyof BackupSelection; label: string }> = [
    { key: 'favorites', label: 'Favorites' },
    { key: 'rooms', label: 'Rooms' },
    { key: 'scenes', label: 'Manual scenes' },
    { key: 'appearance', label: 'Layout & theme' },
    { key: 'automations', label: 'Automations' },
  ]

  function toggleAll() {
    const value = !allSelected
    selection = {
      favorites: value,
      rooms: value,
      scenes: value,
      appearance: value,
      automations: value,
    }
  }

  async function exportSelected() {
    if (busy) return
    busy = true
    message = ''
    try {
      downloadBackup(await createBackup(selection))
      message = 'Backup downloaded.'
    } catch (error) {
      message = error instanceof Error ? error.message : 'Backup failed.'
    } finally {
      busy = false
    }
  }

  async function chooseFile(event: Event) {
    selected = null
    message = ''
    const file = (event.currentTarget as HTMLInputElement).files?.[0]
    if (!file) return
    try {
      selected = await readBackup(file)
    } catch (error) {
      message = error instanceof Error ? error.message : 'Could not read backup.'
    }
  }

  async function restoreSelected() {
    if (!selected || busy) return
    busy = true
    message = ''
    try {
      await restoreBackup(selected, get(devices))
      location.reload()
    } catch (error) {
      message = error instanceof Error ? error.message : 'Restore failed.'
      busy = false
    }
  }
</script>

<div class="mt-4 border-t border-ink/10 pt-4">
  <div class="flex items-center justify-between">
    <span class="text-sm font-medium text-ink/75">Backup & restore</span>
    <button
      type="button"
      onclick={toggleAll}
      class="text-xs font-medium text-indigo-500 hover:text-indigo-600 dark:text-indigo-300"
    >
      {allSelected ? 'Clear all' : 'Select all'}
    </button>
  </div>
  <p class="mt-1 text-xs leading-relaxed text-ink/45">
    One file is created from the selected types. Restore always applies everything present in that file.
  </p>

  <div class="mt-2 grid grid-cols-2 gap-1.5 rounded-xl bg-ink/[0.03] p-2">
    {#each choices as choice (choice.key)}
      <label class="flex items-center gap-2 rounded-lg px-1.5 py-1 text-xs text-ink/70">
        <input
          type="checkbox"
          bind:checked={selection[choice.key]}
          class="h-4 w-4 accent-indigo-500"
        />
        {choice.label}
      </label>
    {/each}
  </div>
  <button
    type="button"
    onclick={exportSelected}
    disabled={busy || !Object.values(selection).some(Boolean)}
    class="mt-2 w-full rounded-xl bg-ink/5 py-2 text-sm font-medium text-ink/75 transition hover:bg-ink/10 disabled:opacity-40"
  >
    {busy ? 'Working…' : 'Download backup'}
  </button>

  <label class="mt-3 block text-xs font-medium text-ink/55" for="restore-file">Restore one backup file</label>
  <input
    id="restore-file"
    type="file"
    accept="application/json,.json"
    onchange={chooseFile}
    class="mt-1 block w-full text-xs text-ink/55 file:mr-2 file:rounded-lg file:border-0 file:bg-ink/5 file:px-3 file:py-2 file:text-xs file:font-medium file:text-ink/70"
  />
  {#if selected}
    <div class="mt-2 rounded-xl border border-indigo-500/20 bg-indigo-500/10 p-2.5">
      <p class="text-xs text-ink/65">Will restore: {backupSectionNames(selected).join(', ')}</p>
      <p class="mt-1 text-[11px] text-ink/40">Types not listed here will stay unchanged.</p>
      <button
        type="button"
        onclick={restoreSelected}
        disabled={busy}
        class="mt-2 w-full rounded-lg bg-indigo-500 py-2 text-xs font-semibold text-white transition hover:bg-indigo-600 disabled:opacity-40"
      >
        {busy ? 'Restoring…' : 'Restore backup'}
      </button>
    </div>
  {/if}
  {#if message}
    <p class="mt-2 text-xs {message.endsWith('downloaded.') ? 'text-emerald-600 dark:text-emerald-300' : 'text-rose-600 dark:text-rose-300'}">{message}</p>
  {/if}
</div>
