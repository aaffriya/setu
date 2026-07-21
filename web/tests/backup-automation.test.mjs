import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import ts from 'typescript'

const backup = readFileSync(new URL('../src/lib/backup.ts', import.meta.url), 'utf8')
const backupUI = readFileSync(
  new URL('../src/lib/components/BackupRestore.svelte', import.meta.url),
  'utf8',
)
const api = readFileSync(new URL('../src/lib/api.ts', import.meta.url), 'utf8')
const validationSource = readFileSync(
  new URL('../src/lib/backup-validation.ts', import.meta.url),
  'utf8',
)
const validationCode = ts.transpileModule(validationSource, {
  compilerOptions: { module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2022 },
}).outputText
const validation = await import(
  `data:text/javascript;base64,${Buffer.from(validationCode).toString('base64')}`
)

test('backup is one sectioned file and excludes credentials and device cache', () => {
  assert.match(backup, /format:\s*'setu-backup'/)
  assert.match(backup, /sections:\s*BackupSections/)
  assert.doesNotMatch(backup, /setu\.token/)
  assert.doesNotMatch(backup, /setu\.devices/)
  assert.match(backup, /Types? not listed|omitted sections are untouched/i)
})

test('restore has one action for every section present in the file', () => {
  assert.match(backupUI, /Will restore:/)
  assert.match(backupUI, /'Restore backup'/)
  assert.match(backupUI, /Types not listed here will stay unchanged/)
  assert.match(backup, /rollbackLocal\(previous\)/)
})

test('automation API keeps webhook trigger separate from admin rule endpoints', () => {
  assert.match(api, /\/api\/automations\/export/)
  assert.match(api, /\/api\/automations\/\$\{encodeURIComponent\(id\)\}\/token/)
  assert.doesNotMatch(api, /automation-hooks.*getToken/)
})

test('backup validation rejects malformed favourites and scenes before restore', () => {
  assert.equal(
    validation.isFavoritesSection({
      lamp: [
        {
          id: 'fav-1',
          kind: 'color',
          value: { r: 10, g: 20, b: 30 },
          label: 'Blue',
          brightness: 40,
        },
      ],
    }),
    true,
  )
  assert.equal(validation.isFavoritesSection({ lamp: [null] }), false)
  assert.equal(
    validation.isFavoritesSection({
      lamp: [{ id: 'fav-1', kind: 'color', value: { r: 999, g: 0, b: 0 }, label: 'Bad' }],
    }),
    false,
  )

  assert.equal(
    validation.isScenesSection([
      {
        id: 'scene-1',
        name: 'Movie',
        commands: [{ deviceId: 'lamp', action: 'set_brightness', value: 30 }],
      },
    ]),
    true,
  )
  assert.equal(validation.isScenesSection([null]), false)
  assert.equal(
    validation.isScenesSection([
      { id: 'scene-1', name: 'Bad', commands: [{ deviceId: 'lamp', action: 'unknown' }] },
    ]),
    false,
  )
})
