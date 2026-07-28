import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const devices = readFileSync(
  new URL('../src/lib/components/Devices.svelte', import.meta.url),
  'utf8',
)
const api = readFileSync(new URL('../src/lib/api.ts', import.meta.url), 'utf8')
const wiz = readFileSync(
  new URL('../../internal/devices/wiz/wiz.go', import.meta.url),
  'utf8',
)
const atomberg = readFileSync(
  new URL('../../internal/devices/atomberg/atomberg.go', import.meta.url),
  'utf8',
)
const wol = readFileSync(
  new URL('../../internal/devices/wol/wol.go', import.meta.url),
  'utf8',
)

test('manual catalog is filtered as Type then Brand then Model', () => {
  assert.match(api, /export type DeviceType = \{[\s\S]*category: string/)
  assert.match(devices, /types\.map\(\(type\) => type\.category\)/)
  assert.match(devices, /type\.category === manualCategory[\s\S]*\.map\(\(type\) => type\.brand\)/)
  assert.match(
    devices,
    /type\.category === manualCategory && type\.brand === manualBrand/,
  )

  const type = devices.indexOf('Select Type')
  const brand = devices.indexOf('Select Brand')
  const model = devices.indexOf('Select Model')
  assert.ok(type >= 0 && brand > type && model > brand)
  assert.match(devices, /disabled=\{!manualCategory\}[\s\S]*aria-label="Device brand"/)
  assert.match(devices, /disabled=\{!manualBrand\}[\s\S]*aria-label="Device model"/)
})

test('changing an earlier choice clears every dependent choice', () => {
  const category = devices.slice(
    devices.indexOf('function selectManualCategory'),
    devices.indexOf('function selectManualBrand'),
  )
  assert.match(category, /manualCategory = category/)
  assert.match(category, /manualBrand = ''/)
  assert.match(category, /manualType = ''/)

  const brand = devices.slice(
    devices.indexOf('function selectManualBrand'),
    devices.indexOf('// What to call a device'),
  )
  assert.match(brand, /manualBrand = brand/)
  assert.match(brand, /manualType = ''/)
})

test('manual add resolves the hidden driver only from the selected catalog entry', () => {
  const addManual = devices.slice(
    devices.indexOf('async function addManual'),
    devices.indexOf('// One field at a time'),
  )
  assert.match(addManual, /candidate\.category === manualCategory/)
  assert.match(addManual, /candidate\.brand === manualBrand/)
  assert.match(addManual, /typeKey\(candidate\) === manualType/)
  assert.match(addManual, /brand: type\.brand/)
  assert.match(addManual, /driver: type\.driver/)
  assert.match(addManual, /manualName\.trim\(\) \|\| `\$\{type\.brand\} \$\{type\.label\}`/)
  assert.doesNotMatch(addManual, /\bmodel:/)
  assert.match(devices, /disabled=\{busy \|\| !manualType \|\| !manualMAC\.trim\(\)\}/)
})

test('catalog labels use readable title case at their source', () => {
  assert.match(wiz, /"Colour Bulb"/)
  assert.match(wiz, /"Tunable White Bulb"/)
  assert.match(atomberg, /"Fan With Light"/)
  assert.match(wol, /"Wake-on-LAN Target"/)
})
