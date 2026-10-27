import fs from 'node:fs'
import fsp from 'node:fs/promises'
import path from 'node:path'
import { randomUUID } from 'node:crypto'
import JSZip from 'jszip'
import { AdminClient, errorType } from './http.mjs'
import { AdminFixture, anonymous } from './admin.mjs'
import {
  assetPartsFromUrl, buildAssets, buildConfigs, buildSimpleConfigs, configsByAlias,
  mimeOf, resolveReturnedUrl, sha256,
} from './fixtures.mjs'
import { Instance, processInfo, stopCommand } from './instance.mjs'
import { Harness } from './harness.mjs'
import { runBrowserScenarios, startBusinessPage } from './browser.mjs'

const env = process.env
const required = name => {
  const value = env[name]
  if (!value) throw new Error(`missing required environment variable ${name}; run through script/test-runtime-business.ps1`)
  return value
}

const runId = required('RB_QA_RUN_ID')
const runDir = required('RB_QA_RUN_DIR')
const artifactsDir = required('RB_QA_ARTIFACTS_DIR')
const binary = required('RB_QA_BINARY')
const adminUsername = required('RB_QA_ADMIN_USERNAME')
const adminPassword = required('RB_QA_ADMIN_PASSWORD')
const versionTag = required('RB_QA_VERSION_TAG')
const keepRunning = env.RB_QA_KEEP_RUNNING === '1'
const repo = env.RB_QA_REPO ?? process.cwd()
const suffix = Date.now().toString(36) + Math.random().toString(36).slice(2, 6)
const envA = `qa_biz_a_${suffix}`
const envB = `qa_biz_b_${suffix}`
const envEmpty = `qa_empty_${suffix}`
const envScratch = `qa_scope_${suffix}`
const envRoot = `qa_root_${suffix}`

const harness = new Harness({
  runId,
  artifactsDir,
  meta: {
    version_tag: versionTag,
    git_head: env.RB_QA_GIT_HEAD ?? 'unknown',
    dirty: env.RB_QA_DIRTY ?? 'unknown',
    node: process.version,
    repo,
    ports: { api_base_path: 18140, api_root_path: 18141, browser_a: 18142, browser_b: 18143 },
    environment_keys: { a: envA, b: envB, empty: envEmpty, scratch: envScratch, root: envRoot },
    note: 'runtime fixtures are synthetic; no user data was copied',
  },
})

const instances = []
const pages = []
const runNotes = []
let setupError = null
let admin = null
let fixtures = null
let ctx = null

function closeQuietly(value) {
  try { value?.close?.() } catch {}
}

async function stopAll() {
  for (const page of pages.reverse()) await closeQuietly(page.server)
  for (const instance of [...instances].reverse()) {
    try { await instance.stop() } catch (error) {
      console.error(`[cleanup] ${error.message}`)
    }
  }
}

function jsonData(response, context, status = 200) {
  if (response.status !== status) {
    throw new Error(`${context}: HTTP ${response.status}, expected ${status}: ${response.text.slice(0, 500)}`)
  }
  if (status === 200 && response.json?.code !== 200) {
    throw new Error(`${context}: code ${response.json?.code}: ${response.text.slice(0, 500)}`)
  }
  return response.json?.data
}

function runtimePath(options = {}) {
  const params = new URLSearchParams()
  if (Object.hasOwn(options, 'version')) params.set('version', options.version)
  if (Object.hasOwn(options, 'scopeId')) params.set('scope_id', options.scopeId)
  if (Object.hasOwn(options, 'contentFormat')) params.set('content_format', options.contentFormat)
  if (Object.hasOwn(options, 'extraQuery')) {
    for (const [key, value] of Object.entries(options.extraQuery)) params.set(key, value)
  }
  const query = params.toString()
  return '/api/v1/runtime/release' + (query ? `?${query}` : '')
}

function scopeHeaders(scope, extra = {}) {
  return { 'x-environment': scope.environmentKey, 'x-pipeline': scope.pipelineKey, ...extra }
}

function runtimeRequest(scope, options = {}) {
  return anonymous(instances[0].target, runtimePath(options), {
    headers: options.headers ?? scopeHeaders(scope),
  })
}

function configMap(data) {
  return configsByAlias(data)
}

function expectRuntime(result, data, expected, label) {
  harness.check(result, Boolean(data), `${label}: response data is present`)
  harness.check(result, data.selection_mode === expected.mode,
    `${label}: selection_mode=${expected.mode}`, data.selection_mode)
  harness.check(result, data.version === expected.version,
    `${label}: version=${expected.version}`, data.version)
  harness.check(result, data.snapshot_id === expected.snapshot,
    `${label}: snapshot=${expected.snapshot}`, data.snapshot_id)
  if (expected.scopeId) {
    harness.check(result, data.scope_id === expected.scopeId,
      `${label}: scope_id is the requested generation`, { actual: data.scope_id, expected: expected.scopeId })
  }
  if (expected.environmentKey) {
    harness.check(result, data.environment?.environment_key === expected.environmentKey,
      `${label}: environment header is reflected`, data.environment?.environment_key)
    harness.check(result, data.environment?.pipeline_key === expected.pipelineKey,
      `${label}: pipeline header is reflected`, data.environment?.pipeline_key)
  }
  const map = configMap(data)
  const greeting = map.get('greeting')
  harness.check(result, greeting?.content === expected.marker,
    `${label}: greeting marker=${expected.marker}`, greeting?.content)
  if (expected.mode === 'active') {
    harness.check(result, typeof data.active_revision === 'string' && data.active_revision.length > 0,
      `${label}: active_revision is a decimal string`, data.active_revision)
    harness.check(result, typeof data.activated_at === 'string' && data.activated_at.length > 0,
      `${label}: activated_at is present`, data.activated_at)
  } else {
    harness.check(result, !Object.hasOwn(data, 'active_revision'),
      `${label}: fixed omits active_revision`)
    harness.check(result, !Object.hasOwn(data, 'activated_at'),
      `${label}: fixed omits activated_at`)
  }
  return map
}

function pngInfo(bytes) {
  const signature = bytes.subarray(0, 8).toString('hex')
  const width = bytes.length >= 24 ? bytes.readUInt32BE(16) : 0
  const height = bytes.length >= 24 ? bytes.readUInt32BE(20) : 0
  return { signature, width, height, png: signature === '89504e470d0a1a0a' && width > 0 && height > 0 }
}

function assetIdFromContent(content, label) {
  const parts = assetPartsFromUrl(content)
  if (!parts) throw new Error(`${label}: not a managed release asset URL: ${String(content).slice(0, 300)}`)
  return parts
}

async function downloadAsset(result, url, expected, label, origin = instances[0].origin) {
  const parsed = resolveReturnedUrl(origin, url)
  const response = await anonymous(instances[0].target, parsed.href, { headers: {} })
  harness.check(result, response.status === 200, `${label}: asset HTTP 200`, response.status)
  harness.check(result, response.headers['cache-control'] === 'no-store', `${label}: asset no-store`, response.headers['cache-control'])
  harness.check(result, response.bytes.length === expected.bytes.length,
    `${label}: asset length`, { actual: response.bytes.length, expected: expected.bytes.length })
  harness.check(result, sha256(response.bytes) === expected.sha256,
    `${label}: asset SHA-256`, sha256(response.bytes))
  harness.check(result, response.contentType.startsWith(expected.contentType),
    `${label}: asset Content-Type`, response.contentType)
  if (expected.contentType === 'image/png') {
    harness.check(result, pngInfo(response.bytes).png, `${label}: PNG signature/IHDR decode`, pngInfo(response.bytes))
  }
  return response
}

async function fetchStatic(scope, version, snapshot, scopeId) {
  const params = new URLSearchParams({ version, expected_snapshot_id: snapshot })
  if (scopeId !== undefined) params.set('scope_id', scopeId)
  return anonymous(instances[0].target, `/api/v1/runtime/release/static?${params}`, {
    headers: scopeHeaders(scope),
  })
}

async function staticBundle(result, response, label) {
  harness.check(result, response.status === 200, `${label}: HTTP 200`, response.status)
  harness.check(result, response.contentType.startsWith('application/zip'), `${label}: ZIP Content-Type`, response.contentType)
  harness.check(result, Boolean(response.headers['content-disposition']?.includes('attachment')), `${label}: attachment disposition`)
  const zip = await JSZip.loadAsync(response.bytes)
  const configEntry = zip.file('config.json')
  const manifestEntry = zip.file('manifest.json')
  harness.check(result, Boolean(configEntry && manifestEntry), `${label}: config.json and manifest.json`)
  const config = JSON.parse(await configEntry.async('string'))
  const manifest = JSON.parse(await manifestEntry.async('string'))
  return { zip, config, manifest }
}

async function initFixtures() {
  const base = new Instance({
    label: 'base', dir: path.join(runDir, 'instance-base'), port: 18140,
    prefix: '/rainbow-bridge', binary, adminUsername, adminPassword, runId, versionTag, keepRunning,
  })
  instances.push(base)
  await base.start()
  admin = new AdminClient(base.target, adminUsername, adminPassword)
  await admin.login()
  const fixture = new AdminFixture(admin)

  const scopeA = await fixture.createScope(envA, 'default', { environmentName: 'QA Business A', pipelineName: 'Main' })
  const scopeABeta = await fixture.createPipeline({
    environmentKey: envA, pipelineKey: 'beta', environmentId: scopeA.environmentId,
    environmentName: 'QA Business A', pipelineName: 'Beta',
  })
  const scopeB = await fixture.createScope(envB, 'default', { environmentName: 'QA Business B', pipelineName: 'Main' })
  const scopeEmpty = await fixture.createScope(envEmpty, 'default', { environmentName: 'QA Empty', pipelineName: 'Main' })
  const assets = buildAssets(runId)

  const uploaded = {}
  async function upload(scope, key) {
    const result = await fixture.upload(scope, assets[key])
    assets[key].fileId = result.fileId
    uploaded[key] = { ...result, sha256: assets[key].sha256, length: assets[key].bytes.length, contentType: assets[key].contentType }
    return result
  }
  await upload(scopeA, 'red')
  await upload(scopeA, 'text')
  await upload(scopeB, 'yellow')
  await upload(scopeABeta, 'purple')

  const a1Configs = []
  for (const item of buildConfigs(scopeA, assets, 'a1')) {
    a1Configs.push(await fixture.createConfig(scopeA, item))
  }
  const bConfigs = []
  for (const item of buildSimpleConfigs(scopeB, assets, 'B_RELEASE_ONE', 'yellow')) {
    bConfigs.push(await fixture.createConfig(scopeB, item))
  }
  const betaConfigs = []
  for (const item of buildSimpleConfigs(scopeABeta, assets, 'A_BETA_ONE', 'purple')) {
    betaConfigs.push(await fixture.createConfig(scopeABeta, item))
  }

  const preActiveA = await runtimeRequest(scopeA, {})
  const preActiveEmpty = await runtimeRequest(scopeEmpty, {})

  const a1 = await fixture.createRelease(scopeA, 'create A1')
  await fixture.activate(scopeA, a1.version, 'activate A1')
  const b1 = await fixture.createRelease(scopeB, 'create B1')
  await fixture.activate(scopeB, b1.version, 'activate B1')
  const beta1 = await fixture.createRelease(scopeABeta, 'create beta1')
  await fixture.activate(scopeABeta, beta1.version, 'activate beta1')
  const empty1 = await fixture.createRelease(scopeEmpty, 'create empty1')
  await fixture.activate(scopeEmpty, empty1.version, 'activate empty1')

  await fixture.unlock(scopeA, 'prepare A2/A3')
  await upload(scopeA, 'blue')
  await upload(scopeA, 'green')
  let a1Map = new Map(a1Configs.map(item => [item.alias, item]))
  for (const item of buildConfigs(scopeA, assets, 'a2')) {
    a1Map.set(item.alias, await fixture.updateConfig(scopeA, a1Map.get(item.alias), item))
  }
  const a2 = await fixture.createRelease(scopeA, 'create A2 without activation')
  await fixture.unlock(scopeA, 'prepare A3 editor-only')
  for (const item of buildConfigs(scopeA, assets, 'a3')) {
    a1Map.set(item.alias, await fixture.updateConfig(scopeA, a1Map.get(item.alias), item))
  }
  const editorGreeting = await fixture.configDetail(scopeA, a1Map.get('greeting').resource_key)

  return {
    fixture, scopeA, scopeABeta, scopeB, scopeEmpty, assets, uploaded,
    a1, a2, b1, beta1, empty1, a1Configs, bConfigs, betaConfigs,
    preActiveA, preActiveEmpty, editorGreeting,
  }
}

async function runCases() {
  const { fixture, scopeA, scopeABeta, scopeB, scopeEmpty, assets, a1, a2, b1, beta1, empty1 } = fixtures
  const base = instances[0]
  const markers = {
    [a1.version]: 'A_RELEASE_ONE',
    [a2.version]: 'A_RELEASE_TWO',
    [b1.version]: 'B_RELEASE_ONE',
    [beta1.version]: 'A_BETA_ONE',
  }
  const snapshots = { [a1.version]: a1.snapshot_id, [a2.version]: a2.snapshot_id }

  await harness.case('RT01', 'A/B anonymous default release reads correct scope and fixtures', async result => {
    const a = await runtimeRequest(scopeA, {})
    const b = await runtimeRequest(scopeB, {})
    const aData = jsonData(a, 'RT01 A default')
    const bData = jsonData(b, 'RT01 B default')
    expectRuntime(result, aData, {
      mode: 'active', version: a1.version, snapshot: a1.snapshot_id,
      scopeId: scopeA.scopeId, environmentKey: envA, pipelineKey: 'default', marker: 'A_RELEASE_ONE',
    }, 'RT01 A')
    expectRuntime(result, bData, {
      mode: 'active', version: b1.version, snapshot: b1.snapshot_id,
      scopeId: scopeB.scopeId, environmentKey: envB, pipelineKey: 'default', marker: 'B_RELEASE_ONE',
    }, 'RT01 B')
    harness.check(result, !a.text.includes('B_RELEASE_ONE') && !a.text.includes(envB), 'RT01 A has no B marker/scope')
    harness.check(result, !b.text.includes('A_RELEASE_ONE') && !b.text.includes(envA), 'RT01 B has no A marker/scope')
    const aBanner = configMap(aData).get('banner')
    const bBanner = configMap(bData).get('banner')
    harness.check(result, assetIdFromContent(aBanner.content, 'A banner').snapshotId === a1.snapshot_id,
      'RT01 A banner belongs to A1 snapshot')
    harness.check(result, assetIdFromContent(bBanner.content, 'B banner').snapshotId === b1.snapshot_id,
      'RT01 B banner belongs to B1 snapshot')
    harness.check(result, a.headers['cache-control'] === 'no-store', 'RT01 runtime no-store')
    return { a_version: a1.version, b_version: b1.version, a_snapshot: a1.snapshot_id, b_snapshot: b1.snapshot_id }
  })

  await harness.case('RT02', 'A1 active, A2 unpublished, A3 editor draft isolation', async result => {
    const active = jsonData(await runtimeRequest(scopeA, {}), 'RT02 default')
    const fixed = jsonData(await runtimeRequest(scopeA, { version: a2.version }), 'RT02 fixed A2')
    expectRuntime(result, active, {
      mode: 'active', version: a1.version, snapshot: a1.snapshot_id, scopeId: scopeA.scopeId,
      environmentKey: envA, pipelineKey: 'default', marker: 'A_RELEASE_ONE',
    }, 'RT02 default')
    expectRuntime(result, fixed, {
      mode: 'fixed', version: a2.version, snapshot: a2.snapshot_id, scopeId: scopeA.scopeId,
      environmentKey: envA, pipelineKey: 'default', marker: 'A_RELEASE_TWO',
    }, 'RT02 fixed A2')
    harness.check(result, fixtures.editorGreeting.content === 'A_EDITOR_ONLY', 'RT02 A3 draft exists in editor')
    const a1Read = await runtimeRequest(scopeA, { version: a1.version })
    const a2Read = await runtimeRequest(scopeA, { version: a2.version })
    harness.check(result, !activeTextContains([a1Read, a2Read], 'A_EDITOR_ONLY'), 'RT02 no runtime response exposes A3')
    return { active_version: active.version, fixed_version: fixed.version, editor_marker: fixtures.editorGreeting.content }
  })

  await harness.case('RT03', 'fixed A1 stays fixed while active switches A2 and back', async result => {
    const fixedBefore = await runtimeRequest(scopeA, { version: a1.version })
    const fixedData = jsonData(fixedBefore, 'RT03 fixed A1')
    expectRuntime(result, fixedData, {
      mode: 'fixed', version: a1.version, snapshot: a1.snapshot_id, scopeId: scopeA.scopeId,
      environmentKey: envA, pipelineKey: 'default', marker: 'A_RELEASE_ONE',
    }, 'RT03 fixed A1')
    const compare = await fixture.compareActive(scopeA, a2.version, a2.snapshot_id)
    harness.check(result, compare.status === 200 && compare.json?.data?.comparison_mode === 'active',
      'RT03 active compare before switch', { status: compare.status, body: compare.text.slice(0, 300) })
    const before = await fixture.active(scopeA)
    await fixture.activate(scopeA, a2.version, 'RT03 switch A2')
    const afterA2 = jsonData(await runtimeRequest(scopeA, {}), 'RT03 default A2')
    expectRuntime(result, afterA2, {
      mode: 'active', version: a2.version, snapshot: a2.snapshot_id, scopeId: scopeA.scopeId,
      environmentKey: envA, pipelineKey: 'default', marker: 'A_RELEASE_TWO',
    }, 'RT03 default A2')
    harness.check(result, afterA2.active_revision !== before.revision, 'RT03 active revision advanced',
      { before: before.revision, after: afterA2.active_revision })
    const fixedAfter = jsonData(await runtimeRequest(scopeA, { version: a1.version }), 'RT03 fixed after switch')
    expectRuntime(result, fixedAfter, {
      mode: 'fixed', version: a1.version, snapshot: a1.snapshot_id, scopeId: scopeA.scopeId,
      environmentKey: envA, pipelineKey: 'default', marker: 'A_RELEASE_ONE',
    }, 'RT03 fixed A1 after switch')
    await fixture.activate(scopeA, a1.version, 'RT03 rollback A1')
    return { active_before: before.revision, active_after_a2: afterA2.active_revision }
  })

  await harness.case('RT04', 'new default requests follow switch and rollback without restart', async result => {
    await fixture.activate(scopeA, a2.version, 'RT04 switch A2')
    const a2Read = jsonData(await runtimeRequest(scopeA, {}), 'RT04 default A2')
    expectRuntime(result, a2Read, {
      mode: 'active', version: a2.version, snapshot: a2.snapshot_id, scopeId: scopeA.scopeId,
      environmentKey: envA, pipelineKey: 'default', marker: 'A_RELEASE_TWO',
    }, 'RT04 default A2')
    await fixture.activate(scopeA, a1.version, 'RT04 rollback A1')
    const a1Read = jsonData(await runtimeRequest(scopeA, {}), 'RT04 default A1')
    expectRuntime(result, a1Read, {
      mode: 'active', version: a1.version, snapshot: a1.snapshot_id, scopeId: scopeA.scopeId,
      environmentKey: envA, pipelineKey: 'default', marker: 'A_RELEASE_ONE',
    }, 'RT04 default A1')
    harness.check(result, instances[0].generation === 1, 'RT04 no restart occurred during switch')
    return { switched: a2.version, rolled_back: a1.version }
  })

  await harness.case('RT05', 'interleaved A/default, B/default, A/beta reads do not cross scopes', async result => {
    const expectations = [
      { scope: scopeA, version: a1.version, snapshot: a1.snapshot_id, marker: 'A_RELEASE_ONE', env: envA, pipeline: 'default' },
      { scope: scopeB, version: b1.version, snapshot: b1.snapshot_id, marker: 'B_RELEASE_ONE', env: envB, pipeline: 'default' },
      { scope: scopeABeta, version: beta1.version, snapshot: beta1.snapshot_id, marker: 'A_BETA_ONE', env: envA, pipeline: 'beta' },
    ]
    for (let round = 0; round < 20; round++) {
      for (const item of expectations) {
        const data = jsonData(await runtimeRequest(item.scope, {}), `RT05 ${item.scope.environmentKey}/${item.scope.pipelineKey} round ${round}`)
        expectRuntime(result, data, {
          mode: 'active', version: item.version, snapshot: item.snapshot, scopeId: item.scope.scopeId,
          environmentKey: item.env, pipelineKey: item.pipeline, marker: item.marker,
        }, `RT05 ${item.env}/${item.pipeline} round ${round}`)
      }
    }
    return { rounds: 20, scopes: 3, total_reads: 60 }
  })

  await harness.case('RT06', 'omitted version is active; explicit empty version is 400', async result => {
    const omitted = jsonData(await runtimeRequest(scopeA, {}), 'RT06 omitted')
    expectRuntime(result, omitted, {
      mode: 'active', version: a1.version, snapshot: a1.snapshot_id, scopeId: scopeA.scopeId,
      environmentKey: envA, pipelineKey: 'default', marker: 'A_RELEASE_ONE',
    }, 'RT06 omitted version')
    const explicit = await runtimeRequest(scopeA, { version: '' })
    harness.check(result, explicit.status === 400, 'RT06 explicit empty version HTTP 400', explicit.status)
    harness.check(result, errorType(explicit) === 'RELEASE_INVALID_REQUEST',
      'RT06 explicit empty version error_type', errorType(explicit))
    return { omitted_mode: omitted.selection_mode, empty_status: explicit.status }
  })

  await harness.case('RT07', 'eleven config types in json and raw modes', async result => {
    const jsonResponse = await runtimeRequest(scopeA, { contentFormat: 'json' })
    const jsonText = jsonResponse.text
    const json = jsonData(jsonResponse, 'RT07 json')
    const raw = jsonData(await runtimeRequest(scopeA, { contentFormat: 'raw' }), 'RT07 raw')
    const jsonMap = configMap(json)
    const rawMap = configMap(raw)
    harness.check(result, jsonMap.size === 11, 'RT07 json has all 11 aliases', jsonMap.size)
    harness.check(result, rawMap.size === 11, 'RT07 raw has all 11 aliases', rawMap.size)
    const expectedStrings = {
      greeting: 'A_RELEASE_ONE', note: 'A_RELEASE_ONE line one\nA_RELEASE_ONE line two',
      zero: '0', decimal: '0.00', flag: 'false', theme: '#112233',
    }
    for (const [alias, expected] of Object.entries(expectedStrings)) {
      harness.check(result, jsonMap.get(alias)?.content === expected, `RT07 ${alias} json string preserved`, jsonMap.get(alias)?.content)
      harness.check(result, rawMap.get(alias)?.content === expected, `RT07 ${alias} raw string preserved`, rawMap.get(alias)?.content)
    }
    for (const alias of ['obj', 'kv']) {
      const value = jsonMap.get(alias)?.content
      harness.check(result, value && typeof value === 'object' && !Array.isArray(value), `RT07 ${alias} json is object`, value)
      harness.check(result, typeof rawMap.get(alias)?.content === 'string', `RT07 ${alias} raw is string`)
      harness.check(result, rawMap.get(alias).content.includes('9007199254740993') &&
        rawMap.get(alias).content.includes('9007199254740992'), `RT07 ${alias} raw retains both big integers`)
      harness.check(result, rawMap.get(alias).content.includes('asset://') === false, `RT07 ${alias} raw renders managed URL`, rawMap.get(alias).content.slice(0, 200))
    }
    for (const alias of ['banner', 'attachment', 'rich']) {
      harness.check(result, typeof jsonMap.get(alias)?.content === 'string', `RT07 ${alias} content is string`)
      harness.check(result, typeof rawMap.get(alias)?.content === 'string', `RT07 ${alias} raw content is string`)
    }
    harness.check(result, jsonMap.get('banner').content.includes(`/api/v1/release/asset/${a1.snapshot_id}/`), 'RT07 image is managed URL')
    harness.check(result, rawMap.get('rich').content.includes('A_RELEASE_ONE') && rawMap.get('rich').content.includes('<img'), 'RT07 richtext marker and image preserved')
    harness.check(result, jsonText.includes('9007199254740993') && jsonText.includes('9007199254740992'), 'RT07 raw HTTP json text retains both big integers')
    return { aliases: [...jsonMap.keys()], raw_contains_big_integers: true }
  })

  await harness.case('RT08', 'big integer literals survive raw HTTP and raw content', async result => {
    const json = await runtimeRequest(scopeA, {})
    const raw = await runtimeRequest(scopeA, { contentFormat: 'raw' })
    harness.check(result, json.text.includes('9007199254740993') && json.text.includes('9007199254740992'),
      'RT08 default runtime HTTP text keeps both literals')
    harness.check(result, raw.text.includes('9007199254740993') && raw.text.includes('9007199254740992'),
      'RT08 raw runtime HTTP text keeps both literals')
    const rawData = jsonData(raw, 'RT08 raw')
    harness.check(result, configMap(rawData).get('obj').content.includes('9007199254740993'), 'RT08 raw obj content exact large literal')
    return { note: 'Node JSON.parse would round these numbers; assertions inspect raw HTTP text/content strings' }
  })

  await harness.case('RT09', 'image/file/rich/JSON managed references download without credentials', async result => {
    const data = jsonData(await runtimeRequest(scopeA, {}), 'RT09 A default')
    const map = configMap(data)
    const banner = map.get('banner')
    const attachment = map.get('attachment')
    const rich = map.get('rich')
    const object = map.get('obj')
    const kv = map.get('kv')
    const richUrl = rich.content.match(/src="([^"]+)"/)?.[1]
    const refs = [
      ['banner', banner.content], ['attachment', attachment.content],
      ['rich', richUrl],
      ['obj', object.content?.image],
      ['kv', kv.content?.image],
    ]
    for (const [label, value] of refs) {
      harness.check(result, typeof value === 'string' && value.length > 0, `RT09 ${label} has managed URL`)
      const parts = assetIdFromContent(value, `RT09 ${label}`)
      harness.check(result, parts.snapshotId === a1.snapshot_id, `RT09 ${label} belongs to A1 snapshot`, parts.snapshotId)
    }
    harness.check(result, !JSON.stringify(data).includes('/api/v1/asset/file/'), 'RT09 no editor asset route leaks')
    await downloadAsset(result, banner.content, assets.red, 'RT09 banner red')
    await downloadAsset(result, attachment.content, assets.text, 'RT09 text file')
    await downloadAsset(result, richUrl, assets.red, 'RT09 rich image')
    return { banner_url: banner.content, attachment_url: attachment.content }
  })

  await harness.case('RT10', 'fresh URL-only requests fetch correct PNG bytes and text without headers', async result => {
    const data = jsonData(await runtimeRequest(scopeA, {}), 'RT10 A default')
    const map = configMap(data)
    const pngUrl = resolveReturnedUrl(base.origin, map.get('banner').content).href
    const textUrl = resolveReturnedUrl(base.origin, map.get('attachment').content).href
    harness.check(result, new URL(pngUrl).origin === base.origin, 'RT10 image URL uses API origin')
    const png = await anonymous(base.target, pngUrl, { headers: {} })
    harness.check(result, png.status === 200 && sha256(png.bytes) === assets.red.sha256, 'RT10 bare PNG request exact bytes')
    harness.check(result, pngInfo(png.bytes).width === 8 && pngInfo(png.bytes).height === 8, 'RT10 PNG decodes to 8x8')
    const text = await anonymous(base.target, textUrl, { headers: {} })
    harness.check(result, text.status === 200 && text.bytes.equals(assets.text.bytes), 'RT10 bare text request exact bytes')
    harness.check(result, !text.contentType.startsWith('text/html'), 'RT10 text is not HTML', text.contentType)
    return { png_url: pngUrl, png_sha256: sha256(png.bytes), text_sha256: sha256(text.bytes) }
  })

  await harness.case('RT11', 'legacy runtime/config matches new active release; overview lists identities only', async result => {
    const newData = jsonData(await runtimeRequest(scopeA, {}), 'RT11 new runtime')
    const legacyResponse = await anonymous(base.target, '/api/v1/runtime/config', { headers: scopeHeaders(scopeA) })
    const legacy = jsonData(legacyResponse, 'RT11 legacy runtime/config')
    harness.check(result, legacy.snapshot_id === newData.snapshot_id && legacy.version === newData.version &&
      legacy.scope_id === scopeA.scopeId, 'RT11 legacy version/snapshot/scope match new active release')
    harness.check(result, legacyResponse.headers['x-rainbow-snapshot-id'] === legacy.snapshot_id,
      'RT11 legacy snapshot response header matches body')
    harness.check(result, legacyResponse.headers['x-rainbow-version'] === legacy.version,
      'RT11 legacy version response header matches body')
    const legacyMap = configMap(legacy)
    harness.check(result, legacyMap.get('greeting').content === newData.configs.find(c => c.alias === 'greeting').content,
      'RT11 legacy greeting matches new runtime')
    harness.check(result, !legacyResponse.text.includes('A_EDITOR_ONLY'), 'RT11 legacy does not read A3')
    const overview = jsonData(await anonymous(base.target, '/api/v1/runtime/overview'), 'RT11 overview')
    const aEnv = overview.list.find(item => item.environment_key === envA)
    const bEnv = overview.list.find(item => item.environment_key === envB)
    harness.check(result, Boolean(aEnv && bEnv), 'RT11 overview lists A and B environments')
    harness.check(result, aEnv.pipelines.some(item => item.scope_id === scopeA.scopeId && item.pipeline_key === 'default'),
      'RT11 overview A/default scope identity')
    harness.check(result, aEnv.pipelines.some(item => item.scope_id === scopeABeta.scopeId && item.pipeline_key === 'beta'),
      'RT11 overview A/beta scope identity')
    return { legacy_snapshot: legacy.snapshot_id, overview_environments: overview.total }
  })

  await harness.case('RT12', 'no active is 404 without editor fallback; empty snapshot yields configs []', async result => {
    harness.check(result, fixtures.preActiveA.status === 404 &&
      errorType(fixtures.preActiveA) === 'RELEASE_NOT_ACTIVE', 'RT12 A before activation is 404 RELEASE_NOT_ACTIVE',
      { status: fixtures.preActiveA.status, type: errorType(fixtures.preActiveA) })
    harness.check(result, fixtures.preActiveEmpty.status === 404 &&
      errorType(fixtures.preActiveEmpty) === 'RELEASE_NOT_ACTIVE', 'RT12 empty scope before activation is 404 RELEASE_NOT_ACTIVE')
    harness.check(result, !fixtures.preActiveA.text.includes('A_RELEASE_ONE') && !fixtures.preActiveA.text.includes('A_EDITOR_ONLY'),
      'RT12 pre-active A does not leak editor or snapshot data')
    const empty = jsonData(await runtimeRequest(scopeEmpty, {}), 'RT12 empty active')
    harness.check(result, Array.isArray(empty.configs) && empty.configs.length === 0,
      'RT12 empty active snapshot returns configs []', empty.configs)
    expectRuntime(result, empty, {
      mode: 'active', version: empty1.version, snapshot: empty1.snapshot_id, scopeId: scopeEmpty.scopeId,
      environmentKey: envEmpty, pipelineKey: 'default', marker: undefined,
    }, 'RT12 empty runtime')
    return { pre_active_status: fixtures.preActiveA.status, empty_version: empty1.version }
  })

  await harness.case('RT13', 'runtime/overview is identity discovery, not config delivery', async result => {
    const response = await anonymous(base.target, '/api/v1/runtime/overview')
    const data = jsonData(response, 'RT13 overview')
    harness.check(result, response.status === 200 && Array.isArray(data.list), 'RT13 overview HTTP 200 with list')
    harness.check(result, !Object.hasOwn(data, 'configs') && !Object.hasOwn(data, 'snapshot_id'),
      'RT13 overview does not pretend to be a release snapshot')
    const empty = data.list.find(item => item.environment_key === envEmpty)
    harness.check(result, Boolean(empty?.pipelines?.some(item => item.scope_id === scopeEmpty.scopeId)),
      'RT13 overview includes empty scope identity')
    return { environment_count: data.total }
  })

  await harness.case('RT14', 'missing/blank range headers are 400 and never default silently', async result => {
    const cases = [
      ['no headers', { headers: {} }],
      ['only environment', { headers: { 'x-environment': envA } }],
      ['only pipeline', { headers: { 'x-pipeline': 'default' } }],
      ['blank environment', { headers: { 'x-environment': '   ', 'x-pipeline': 'default' } }],
      ['blank pipeline', { headers: { 'x-environment': envA, 'x-pipeline': '   ' } }],
    ]
    for (const [label, init] of cases) {
      const response = await anonymous(base.target, '/api/v1/runtime/release', init)
      harness.check(result, response.status === 400, `RT14 ${label} HTTP 400`, response.status)
      harness.check(result, errorType(response) === 'RELEASE_INVALID_REQUEST', `RT14 ${label} error_type`, errorType(response))
      harness.check(result, !response.text.includes('configs'), `RT14 ${label} does not return default data`)
    }
    return { cases: cases.length }
  })

  await harness.case('RT15', 'body/query ranges are not headers; header names are case-insensitive', async result => {
    const bodyOnly = await anonymous(base.target, '/api/v1/runtime/release', {
      method: 'GET', headers: { 'Content-Type': 'application/json' },
      body: { environment_key: envA, pipeline_key: 'default' },
    })
    harness.check(result, bodyOnly.status === 400, 'RT15 GET JSON body is not a range header', bodyOnly.status)
    const queryOnly = await anonymous(base.target,
      `/api/v1/runtime/release?environment_key=${encodeURIComponent(envA)}&pipeline_key=default`, { headers: {} })
    harness.check(result, queryOnly.status === 400, 'RT15 unsupported query range is not accepted', queryOnly.status)
    const upper = await anonymous(base.target, '/api/v1/runtime/release', {
      headers: { 'X-Environment': envA, 'X-Pipeline': 'default' },
    })
    const data = jsonData(upper, 'RT15 uppercase headers')
    expectRuntime(result, data, {
      mode: 'active', version: a1.version, snapshot: a1.snapshot_id, scopeId: scopeA.scopeId,
      environmentKey: envA, pipelineKey: 'default', marker: 'A_RELEASE_ONE',
    }, 'RT15 uppercase header')
    return { body_status: bodyOnly.status, query_status: queryOnly.status, uppercase_status: upper.status }
  })

  await harness.case('RT16', 'unknown scope/version/asset return their own 404, not another scope', async result => {
    const unknownScope = await anonymous(base.target, '/api/v1/runtime/release', {
      headers: { 'x-environment': `missing_${suffix}`, 'x-pipeline': 'default' },
    })
    harness.check(result, unknownScope.status === 404, 'RT16 unknown scope HTTP 404', unknownScope.status)
    harness.check(result, errorType(unknownScope).includes('SCOPE'), 'RT16 unknown scope error family', errorType(unknownScope))
    const unknownVersion = await runtimeRequest(scopeA, { version: 'v_missing_qa' })
    harness.check(result, unknownVersion.status === 404 && errorType(unknownVersion) === 'RELEASE_VERSION_NOT_FOUND',
      'RT16 unknown version 404', { status: unknownVersion.status, type: errorType(unknownVersion) })
    const unknownAsset = await anonymous(base.target,
      `/api/v1/release/asset/${a1.snapshot_id}/missing-asset-${suffix}`, { headers: {} })
    harness.check(result, unknownAsset.status === 404 && errorType(unknownAsset) === 'RELEASE_ASSET_NOT_FOUND',
      'RT16 unknown asset 404', { status: unknownAsset.status, type: errorType(unknownAsset) })
    harness.check(result, !unknownAsset.text.includes('A_RELEASE_ONE'), 'RT16 unknown asset does not leak scope config')
    return {
      unknown_scope: { status: unknownScope.status, type: errorType(unknownScope) },
      unknown_version: { status: unknownVersion.status, type: errorType(unknownVersion) },
      unknown_asset: { status: unknownAsset.status, type: errorType(unknownAsset) },
    }
  })

  await harness.case('RT17', 'content_format validation and legacy selection-parameter rejection', async result => {
    const jsonResponse = await runtimeRequest(scopeA, { contentFormat: 'json' })
    const rawResponse = await runtimeRequest(scopeA, { contentFormat: 'raw' })
    harness.check(result, jsonResponse.status === 200 && rawResponse.status === 200, 'RT17 json/raw succeed')
    const emptyFormat = await runtimeRequest(scopeA, { contentFormat: '' })
    harness.check(result, emptyFormat.status === 400, 'RT17 empty content_format 400', emptyFormat.status)
    const badFormat = await runtimeRequest(scopeA, { contentFormat: 'xml' })
    harness.check(result, badFormat.status === 400, 'RT17 invalid content_format 400', badFormat.status)
    for (const [name, pathValue] of [
      ['version', `/api/v1/runtime/config?version=${encodeURIComponent(a1.version)}`],
      ['content_format', '/api/v1/runtime/config?content_format=raw'],
      ['expected_snapshot_id', `/api/v1/runtime/config?expected_snapshot_id=${a1.snapshot_id}`],
    ]) {
      const response = await anonymous(base.target, pathValue, { headers: scopeHeaders(scopeA) })
      harness.check(result, response.status === 400, `RT17 legacy config rejects ${name}`, response.status)
      harness.check(result, errorType(response) === 'RELEASE_INVALID_REQUEST', `RT17 legacy config ${name} error_type`, errorType(response))
    }
    return { json_status: jsonResponse.status, raw_status: rawResponse.status, empty_status: emptyFormat.status, bad_status: badFormat.status }
  })

  await harness.case('RT18', 'anonymous runtime is public; management remains authenticated', async result => {
    const before = await fixture.configList(scopeA)
    const runtime = await runtimeRequest(scopeA, {})
    harness.check(result, jsonData(runtime, 'RT18 runtime').snapshot_id === a1.snapshot_id, 'RT18 anonymous runtime works')
    const list = await anonymous(base.target, `/api/v1/release/list?environment_key=${encodeURIComponent(envA)}&pipeline_key=default`)
    harness.check(result, [401, 403].includes(list.status), 'RT18 anonymous management list is 401/403', list.status)
    const write = await anonymous(base.target, '/api/v1/config/create', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: { expected_scope_id: scopeA.scopeId, config: {
        environment_key: envA, pipeline_key: 'default', alias: `unauth_${suffix}`,
        name: 'unauth', type: 'text', content: 'must-not-exist',
      } },
    })
    harness.check(result, [401, 403].includes(write.status), 'RT18 anonymous write is rejected', write.status)
    const after = await fixture.configList(scopeA)
    harness.check(result, before.total === after.total && !after.list.some(item => item.alias === `unauth_${suffix}`),
      'RT18 rejected write has no side effect')
    return { runtime_status: runtime.status, list_status: list.status, write_status: write.status }
  })

  await harness.case('RT23', 'scope_id pin works on declared endpoints; legacy ignores it (defect)', async result => {
    const pinned = await runtimeRequest(scopeA, { scopeId: scopeA.scopeId })
    const pinData = jsonData(pinned, 'RT23 pinned runtime')
    expectRuntime(result, pinData, {
      mode: 'active', version: a1.version, snapshot: a1.snapshot_id, scopeId: scopeA.scopeId,
      environmentKey: envA, pipelineKey: 'default', marker: 'A_RELEASE_ONE',
    }, 'RT23 pinned runtime')
    const wrong = await runtimeRequest(scopeA, { scopeId: scopeB.scopeId })
    harness.check(result, wrong.status === 409 && errorType(wrong) === 'SCOPE_IDENTITY_CONFLICT',
      'RT23 wrong live scope pin is 409', { status: wrong.status, type: errorType(wrong) })
    const fixedPin = await fetchStatic(scopeA, a1.version, a1.snapshot_id, scopeA.scopeId)
    harness.check(result, fixedPin.status === 200, 'RT23 static fixed pin succeeds', fixedPin.status)
    const fixedWrong = await fetchStatic(scopeA, a1.version, a1.snapshot_id, scopeB.scopeId)
    harness.check(result, [409, 410].includes(fixedWrong.status), 'RT23 static wrong pin rejected', fixedWrong.status)

    const legacyConfig = await anonymous(base.target,
      `/api/v1/runtime/config?scope_id=${encodeURIComponent(scopeB.scopeId)}`, { headers: scopeHeaders(scopeA) })
    const legacyStatic = await anonymous(base.target,
      `/api/v1/runtime/static?environment_key=${encodeURIComponent(envA)}&pipeline_key=default&scope_id=${encodeURIComponent(scopeB.scopeId)}`)
    const ignoredConfig = legacyConfig.status === 200
    const ignoredStatic = legacyStatic.status === 200
    if (ignoredConfig || ignoredStatic) {
      harness.defects.push({
        id: 'RT23',
        description: 'Legacy runtime config/static declare scope_id in IDL but handlers ignore it',
        message: `legacy config status=${legacyConfig.status}, legacy static status=${legacyStatic.status} with wrong scope_id`,
        minimal_reproduction: `/api/v1/runtime/config?scope_id=<other-live-scope> with A headers returns ${legacyConfig.status}; ` +
          `/api/v1/runtime/static?...&scope_id=<other-live-scope> returns ${legacyStatic.status}`,
        impact: 'A caller that believes it pinned an older generation can silently receive the current generation',
        status: 'confirmed_product_defect',
      })
    }
    if (ignoredConfig || ignoredStatic) {
      const error = new Error('RT23 legacy endpoints silently ignored a wrong scope_id')
      error.recordedDefect = true
      throw error
    }
    harness.check(result, !ignoredConfig, 'RT23 legacy runtime/config must reject or honor wrong scope_id', legacyConfig.status)
    harness.check(result, !ignoredStatic, 'RT23 legacy runtime/static must reject or honor wrong scope_id', legacyStatic.status)
    return {
      pinned: pinned.status,
      wrong_pin: { status: wrong.status, type: errorType(wrong) },
      legacy_config: { status: legacyConfig.status, ignored: ignoredConfig },
      legacy_static: { status: legacyStatic.status, ignored: ignoredStatic },
    }
  })

  await harness.case('RT24', 'unknown API and double-prefixed route return 404 JSON, not SPA success', async result => {
    const unknown = await anonymous(base.target, '/api/v1/runtime/does-not-exist')
    harness.check(result, unknown.status === 404, 'RT24 unknown API HTTP 404', unknown.status)
    const doubled = await anonymous(base.target, '/rainbow-bridge/api/v1/runtime/release', { headers: scopeHeaders(scopeA) })
    harness.check(result, doubled.status === 404, 'RT24 double-prefixed path is 404', doubled.status)
    harness.check(result, !unknown.text.toLowerCase().includes('<html') && !doubled.text.toLowerCase().includes('<html'),
      'RT24 unknown routes are not SPA HTML')
    if (unknown.json === null || doubled.json === null) {
      harness.defects.push({
        id: 'RT24',
        description: 'Unknown API GET returns plain-text 404 instead of the documented JSON error envelope',
        message: `unknown=${JSON.stringify(unknown.text.slice(0, 120))}, doubled=${JSON.stringify(doubled.text.slice(0, 120))}, contentType=${unknown.contentType}`,
        minimal_reproduction: 'curl -i http://127.0.0.1:<port>/rainbow-bridge/api/v1/runtime/does-not-exist',
        impact: 'Clients that parse JSON error envelopes receive a plain body and may misclassify the 404 as an HTML/SPA response',
        status: 'confirmed_product_defect',
      })
      const error = new Error('RT24 unknown API returns plain text instead of JSON')
      error.recordedDefect = true
      throw error
    }
    return { unknown_status: unknown.status, doubled_status: doubled.status }
  })

  await harness.case('RT30', 'fixed static package and legacy active static package are consistent ZIPs', async result => {
    const fixed = await fetchStatic(scopeA, a2.version, a2.snapshot_id)
    const fixedBundle = await staticBundle(result, fixed, 'RT30 fixed A2')
    harness.check(result, fixed.headers['x-rainbow-snapshot-id'] === a2.snapshot_id, 'RT30 fixed snapshot header')
    harness.check(result, fixed.headers['x-rainbow-version'] === a2.version, 'RT30 fixed version header')
    harness.check(result, fixedBundle.config.data.snapshot_id === a2.snapshot_id &&
      fixedBundle.config.data.version === a2.version &&
      fixedBundle.config.data.environment.environment_key === envA &&
      fixedBundle.config.data.environment.pipeline_key === 'default', 'RT30 fixed config.json identity')
    harness.check(result, fixedBundle.manifest.snapshot_id === a2.snapshot_id &&
      fixedBundle.manifest.version === a2.version &&
      fixedBundle.manifest.environment_key === envA &&
      fixedBundle.manifest.pipeline_key === 'default', 'RT30 fixed manifest identity')
    harness.check(result, fixedBundle.config.data.configs.some(item => item.alias === 'greeting' && item.content === 'A_RELEASE_TWO'),
      'RT30 fixed package A2 marker')
    harness.check(result, !JSON.stringify(fixedBundle.config).includes('/api/v1/release/asset/'), 'RT30 package has no managed runtime URLs')
    const knownAssets = new Map(Object.values(assets).filter(asset => asset.fileId).map(asset => [asset.sha256, asset]))
    for (const entry of fixedBundle.manifest.assets) {
      const bytes = await fixedBundle.zip.file(entry.path)?.async('nodebuffer')
      harness.check(result, Boolean(bytes), `RT30 asset ${entry.path} exists`)
      harness.check(result, sha256(bytes) === entry.sha256, `RT30 asset ${entry.path} manifest SHA`)
      harness.check(result, knownAssets.has(entry.sha256), `RT30 asset ${entry.path} matches local fixture SHA`)
    }
    const legacy = await anonymous(base.target,
      `/api/v1/runtime/static?environment_key=${encodeURIComponent(envA)}&pipeline_key=default`)
    const legacyBundle = await staticBundle(result, legacy, 'RT30 legacy active static')
    harness.check(result, legacy.headers['x-rainbow-snapshot-id'] === a1.snapshot_id &&
      legacy.headers['x-rainbow-version'] === a1.version, 'RT30 legacy static headers match active A1')
    harness.check(result, legacyBundle.config.data.snapshot_id === a1.snapshot_id &&
      legacyBundle.config.data.version === a1.version, 'RT30 legacy static config identity')
    const mismatch = await fetchStatic(scopeA, a1.version, a2.snapshot_id)
    harness.check(result, mismatch.status === 409 && errorType(mismatch) === 'RELEASE_SNAPSHOT_MISMATCH',
      'RT30 snapshot mismatch is 409 JSON', { status: mismatch.status, type: errorType(mismatch), contentType: mismatch.contentType })
    harness.check(result, !mismatch.contentType.startsWith('application/zip'), 'RT30 mismatch is not a ZIP')
    return {
      fixed_assets: fixedBundle.manifest.assets.length,
      fixed_version: a2.version,
      legacy_version: a1.version,
      mismatch_status: mismatch.status,
    }
  })

  await harness.case('RT25', 'bounded concurrency: 5 clients x 40 reads during 10 A1/A2 switches', async result => {
    const expectedByVersion = {
      [a1.version]: { snapshot: a1.snapshot_id, marker: 'A_RELEASE_ONE' },
      [a2.version]: { snapshot: a2.snapshot_id, marker: 'A_RELEASE_TWO' },
    }
    const counts = { [a1.version]: 0, [a2.version]: 0, other: 0, errors: 0 }
    const failures = []
    const started = Date.now()
    const readers = Array.from({ length: 5 }, async (_, clientIndex) => {
      for (let index = 0; index < 40; index++) {
        try {
          const response = await runtimeRequest(scopeA, {})
          if (response.status !== 200) {
            counts.errors++
            failures.push({ clientIndex, index, status: response.status, text: response.text.slice(0, 200) })
            continue
          }
          const data = jsonData(response, 'RT25 read')
          const expected = expectedByVersion[data.version]
          if (!expected) {
            counts.other++
            failures.push({ clientIndex, index, version: data.version })
            continue
          }
          const marker = configMap(data).get('greeting')?.content
          const banner = assetIdFromContent(configMap(data).get('banner')?.content, 'RT25 banner')
          if (data.selection_mode !== 'active' || data.snapshot_id !== expected.snapshot ||
            marker !== expected.marker || banner.snapshotId !== expected.snapshot) {
            failures.push({ clientIndex, index, version: data.version, snapshot: data.snapshot_id, marker, banner: banner.snapshotId })
            continue
          }
          counts[data.version]++
        } catch (error) {
          counts.errors++
          failures.push({ clientIndex, index, error: error.message })
        }
      }
    })
    const switches = (async () => {
      for (let round = 0; round < 10; round++) {
        await fixture.activate(scopeA, a2.version, `RT25 switch ${round + 1} to A2`)
        await fixture.activate(scopeA, a1.version, `RT25 switch ${round + 1} to A1`)
      }
    })()
    await Promise.all([switches, ...readers])
    harness.check(result, failures.length === 0, 'RT25 every read is a complete A1 or A2 response', failures.slice(0, 10))
    harness.check(result, counts[a1.version] + counts[a2.version] === 200, 'RT25 exactly 200 successful reads',
      counts)
    const finalActive = await fixture.active(scopeA)
    harness.check(result, finalActive?.version === a1.version, 'RT25 final active version is A1', finalActive)
    return {
      read_requests: 200,
      switch_requests: 20,
      status_distribution: { HTTP200: counts[a1.version] + counts[a2.version], errors: counts.errors },
      version_distribution: counts,
      duration_ms: Date.now() - started,
    }
  })

  await harness.case('RT26', 'restart keeps active/fixed snapshots and asset bytes without login', async result => {
    const beforeDefault = jsonData(await runtimeRequest(scopeA, {}), 'RT26 before restart')
    await base.restart()
    const afterDefaultResponse = await runtimeRequest(scopeA, {})
    const afterDefault = jsonData(afterDefaultResponse, 'RT26 after restart default')
    expectRuntime(result, afterDefault, {
      mode: 'active', version: a1.version, snapshot: a1.snapshot_id, scopeId: scopeA.scopeId,
      environmentKey: envA, pipelineKey: 'default', marker: 'A_RELEASE_ONE',
    }, 'RT26 after restart default')
    const fixed = jsonData(await runtimeRequest(scopeA, { version: a2.version }), 'RT26 after restart fixed')
    expectRuntime(result, fixed, {
      mode: 'fixed', version: a2.version, snapshot: a2.snapshot_id, scopeId: scopeA.scopeId,
      environmentKey: envA, pipelineKey: 'default', marker: 'A_RELEASE_TWO',
    }, 'RT26 after restart fixed')
    await downloadAsset(result, configMap(afterDefault).get('banner').content, assets.red, 'RT26 asset after restart')
    harness.check(result, beforeDefault.snapshot_id === afterDefault.snapshot_id, 'RT26 snapshot persisted across restart')
    harness.check(result, base.generation === 2, 'RT26 actually restarted the instance')
    return { generation: base.generation, pid: base.child?.pid ?? null }
  })

  await harness.case('RT19', 'moved asset gives explicit 503 while unrelated B still reads', async result => {
    const data = jsonData(await runtimeRequest(scopeA, {}), 'RT19 A default')
    const banner = configMap(data).get('banner')
    const parts = assetIdFromContent(banner.content, 'RT19 banner')
    const filePath = path.join(base.releaseDir, parts.snapshotId, 'assets', `${parts.assetId}.bin`)
    const moved = `${filePath}.qa-moved`
    harness.check(result, fs.existsSync(filePath), 'RT19 snapshot asset exists before move', filePath)
    fs.renameSync(filePath, moved)
    try {
      const active = await runtimeRequest(scopeA, {})
      harness.check(result, active.status === 503 && errorType(active) === 'RELEASE_INCOMPLETE',
        'RT19 missing asset makes runtime 503 RELEASE_INCOMPLETE', { status: active.status, type: errorType(active) })
      const bannerUrl = resolveReturnedUrl(base.origin, banner.content).href
      const download = await anonymous(base.target, bannerUrl, { headers: {} })
      harness.check(result, download.status === 503, 'RT19 missing asset download 503', download.status)
      const b = await runtimeRequest(scopeB, {})
      harness.check(result, b.status === 200, 'RT19 unrelated B remains readable', b.status)
    } finally {
      fs.renameSync(moved, filePath)
    }
    const restored = await runtimeRequest(scopeA, {})
    harness.check(result, restored.status === 200, 'RT19 restored file works without restart', restored.status)
    await downloadAsset(result, banner.content, assets.red, 'RT19 restored banner')
    return { asset_path: filePath, restored_without_restart: true }
  })

  await harness.case('RT20', 'same-length byte corruption: runtime metadata 200, download 503, restore fixes bytes', async result => {
    const data = jsonData(await runtimeRequest(scopeA, {}), 'RT20 A default')
    const banner = configMap(data).get('banner')
    const parts = assetIdFromContent(banner.content, 'RT20 banner')
    const filePath = path.join(base.releaseDir, parts.snapshotId, 'assets', `${parts.assetId}.bin`)
    const original = fs.readFileSync(filePath)
    const corrupted = Buffer.from(original)
    corrupted[corrupted.length - 1] ^= 0x01
    fs.writeFileSync(filePath, corrupted)
    try {
      const runtimeResponse = await runtimeRequest(scopeA, {})
      harness.check(result, runtimeResponse.status === 200, 'RT20 runtime metadata read stays 200 for same length', runtimeResponse.status)
      const bannerUrl = resolveReturnedUrl(base.origin, banner.content).href
      const download = await anonymous(base.target, bannerUrl, { headers: {} })
      harness.check(result, download.status === 503, 'RT20 SHA mismatch download is 503', download.status)
      harness.check(result, errorType(download) === 'RELEASE_INCOMPLETE', 'RT20 SHA mismatch error_type', errorType(download))
    } finally {
      fs.writeFileSync(filePath, original)
    }
    await downloadAsset(result, banner.content, assets.red, 'RT20 restored exact bytes')
    return { file_path: filePath, original_sha256: sha256(original), corrupted_length: corrupted.length }
  })

  await harness.case('RT21', 'deleted non-active A2 is 410 for fixed reads and old asset URLs', async result => {
    await admin.login()
    const data = jsonData(await runtimeRequest(scopeA, { version: a2.version }), 'RT21 A2 before delete')
    const parts = assetIdFromContent(configMap(data).get('banner').content, 'RT21 A2 banner')
    const deleted = await fixture.deleteRelease(scopeA, a2.version, a2.snapshot_id, 'RT21 delete A2')
    harness.check(result, deleted.deleted === true, 'RT21 A2 deleted', deleted)
    const fixed = await runtimeRequest(scopeA, { version: a2.version })
    harness.check(result, fixed.status === 410 && errorType(fixed) === 'RELEASE_VERSION_DELETED',
      'RT21 fixed A2 is 410', { status: fixed.status, type: errorType(fixed) })
    const asset = await anonymous(base.target,
      `/api/v1/release/asset/${parts.snapshotId}/${parts.assetId}`, { headers: {} })
    harness.check(result, asset.status === 410 && errorType(asset) === 'RELEASE_VERSION_DELETED',
      'RT21 old A2 asset URL is 410', { status: asset.status, type: errorType(asset) })
    const active = await runtimeRequest(scopeA, {})
    harness.check(result, active.status === 200, 'RT21 active A1 remains readable', active.status)
    return { deleted_version: a2.version, fixed_status: fixed.status, asset_status: asset.status }
  })

  await harness.case('RT22', 'scope delete and same-key recreation produce a new generation, old pins do not map', async result => {
    await admin.login()
    const scratch = await fixture.createScope(envScratch, 'default', { environmentName: 'QA Scope Lifecycle', pipelineName: 'Main' })
    const asset = buildAssets(`${runId}-scratch`).red
    const upload = await fixture.upload(scratch, asset)
    asset.fileId = upload.fileId
    await fixture.createConfig(scratch, { alias: 'greeting', name: 'Greeting', type: 'text', content: 'SCRATCH_ONE' })
    await fixture.createConfig(scratch, { alias: 'banner', name: 'Banner', type: 'image', content: `asset://${asset.fileId}` })
    const version = await fixture.createRelease(scratch, 'RT22 create scratch')
    await fixture.activate(scratch, version.version, 'RT22 activate scratch')
    const active = jsonData(await runtimeRequest(scratch, {}), 'RT22 scratch active')
    const oldParts = assetIdFromContent(configMap(active).get('banner').content, 'RT22 scratch banner')
    await fixture.unlock(scratch, 'RT22 unlock before delete')
    const deleted = await fixture.deletePipeline(scratch)
    harness.check(result, deleted.status === 200 && deleted.json?.code === 200,
      'RT22 pipeline delete succeeds', { status: deleted.status, body: deleted.text.slice(0, 300) })
    const recreated = await fixture.createPipeline({
      environmentKey: envScratch, pipelineKey: 'default', environmentId: scratch.environmentId,
      environmentName: 'QA Scope Lifecycle', pipelineName: 'Main Again',
    })
    harness.check(result, recreated.scopeId !== scratch.scopeId, 'RT22 recreated scope has a new scope_id',
      { old: scratch.scopeId, new: recreated.scopeId })
    const newDefault = await runtimeRequest(recreated, {})
    harness.check(result, newDefault.status === 404 && errorType(newDefault) === 'RELEASE_NOT_ACTIVE',
      'RT22 new generation is empty and has no active release', { status: newDefault.status, type: errorType(newDefault) })
    const oldPin = await runtimeRequest(recreated, { scopeId: scratch.scopeId })
    harness.check(result, [409, 410].includes(oldPin.status), 'RT22 old scope pin is rejected', { status: oldPin.status, type: errorType(oldPin) })
    harness.check(result, !oldPin.text.includes('SCRATCH_ONE'), 'RT22 old pin does not map to the new generation')
    const oldAsset = await anonymous(base.target,
      `/api/v1/release/asset/${oldParts.snapshotId}/${oldParts.assetId}`, { headers: {} })
    harness.check(result, [410, 404].includes(oldAsset.status), 'RT22 old asset URL no longer maps', { status: oldAsset.status, type: errorType(oldAsset) })
    const oldFixed = await runtimeRequest(recreated, { version: version.version })
    harness.check(result, oldFixed.status === 404, 'RT22 old fixed version does not appear in new generation', oldFixed.status)
    return {
      old_scope_id: scratch.scopeId, new_scope_id: recreated.scopeId,
      old_pin: { status: oldPin.status, type: errorType(oldPin) },
      old_asset: { status: oldAsset.status, type: errorType(oldAsset) },
      old_fixed_status: oldFixed.status,
    }
  })

  await harness.case('RT27', 'root-path instance: default/fixed/image/legacy/missing/unknown routes', async result => {
    const root = new Instance({
      label: 'root', dir: path.join(runDir, 'instance-root'), port: 18141,
      prefix: '', binary, adminUsername, adminPassword, runId, versionTag, keepRunning,
    })
    instances.push(root)
    await root.start()
    const rootAdmin = new AdminClient(root.target, adminUsername, adminPassword)
    await rootAdmin.login()
    const fixtureRoot = new AdminFixture(rootAdmin)
    const scope = await fixtureRoot.createScope(envRoot, 'default', { environmentName: 'QA Root Path', pipelineName: 'Main' })
    const rootAssets = buildAssets(`${runId}-root`)
    const upload = await fixtureRoot.upload(scope, rootAssets.red)
    rootAssets.red.fileId = upload.fileId
    for (const item of buildSimpleConfigs(scope, rootAssets, 'ROOT_RELEASE_ONE', 'red')) {
      await fixtureRoot.createConfig(scope, item)
    }
    const v1 = await fixtureRoot.createRelease(scope, 'RT27 root v1')
    await fixtureRoot.activate(scope, v1.version, 'RT27 activate root v1')
    await fixtureRoot.unlock(scope, 'RT27 prepare root v2')
    const current = await fixtureRoot.configDetail(scope, (await fixtureRoot.configList(scope)).list.find(item => item.alias === 'greeting').resource_key)
    await fixtureRoot.updateConfig(scope, current, { content: 'ROOT_RELEASE_TWO' })
    const v2 = await fixtureRoot.createRelease(scope, 'RT27 root v2')
    const defaultRead = await anonymous(root.target, '/api/v1/runtime/release', { headers: scopeHeaders(scope) })
    const defaultData = jsonData(defaultRead, 'RT27 root default')
    expectRuntime(result, defaultData, {
      mode: 'active', version: v1.version, snapshot: v1.snapshot_id, scopeId: scope.scopeId,
      environmentKey: envRoot, pipelineKey: 'default', marker: 'ROOT_RELEASE_ONE',
    }, 'RT27 root default')
    const fixedRead = await anonymous(root.target,
      `/api/v1/runtime/release?version=${encodeURIComponent(v2.version)}`, { headers: scopeHeaders(scope) })
    const fixedData = jsonData(fixedRead, 'RT27 root fixed')
    expectRuntime(result, fixedData, {
      mode: 'fixed', version: v2.version, snapshot: v2.snapshot_id, scopeId: scope.scopeId,
      environmentKey: envRoot, pipelineKey: 'default', marker: 'ROOT_RELEASE_TWO',
    }, 'RT27 root fixed')
    const banner = configMap(defaultData).get('banner')
    const bannerUrl = resolveReturnedUrl(root.origin, banner.content).href
    harness.check(result, new URL(bannerUrl).pathname.startsWith('/api/v1/release/asset/'),
      'RT27 root image URL has no double prefix', new URL(bannerUrl).pathname)
    await downloadAsset(result, banner.content, rootAssets.red, 'RT27 root image', root.origin)
    const legacy = await anonymous(root.target, '/api/v1/runtime/config', { headers: scopeHeaders(scope) })
    const legacyData = jsonData(legacy, 'RT27 root legacy')
    harness.check(result, legacyData.snapshot_id === v1.snapshot_id && legacyData.version === v1.version,
      'RT27 root legacy config follows active v1')
    const missing = await anonymous(root.target, '/api/v1/runtime/release', { headers: {} })
    harness.check(result, missing.status === 400, 'RT27 root missing header 400', missing.status)
    const unknown = await anonymous(root.target, '/api/v1/runtime/no-such-route')
    harness.check(result, unknown.status === 404, 'RT27 root unknown route is 404', unknown.status)
    harness.check(result, !unknown.text.toLowerCase().includes('<html'),
      'RT27 root unknown route is not SPA HTML', unknown.text.slice(0, 120))
    if (unknown.json === null) {
      harness.note(result, 'Unknown-route error body is plain text; tracked once as RT24 for the JSON envelope contract.')
    }
    return {
      root_origin: root.origin, version_v1: v1.version, version_v2: v2.version,
      image_url: bannerUrl, generation: root.generation,
    }
  })

  await harness.case('RT28', 'Chromium business pages fetch cross-origin with credentials omit', async result => {
    const apiRoot = `${base.origin}${base.prefix}`
    const preflight = await anonymous(base.target, '/api/v1/runtime/release', {
      method: 'OPTIONS',
      headers: {
        Origin: 'http://127.0.0.1:18142',
        'Access-Control-Request-Method': 'GET',
        'Access-Control-Request-Headers': 'x-environment,x-pipeline',
      },
    })
    harness.check(result, preflight.status === 204, 'RT28 public CORS preflight 204', preflight.status)
    harness.check(result, [base.origin, '*'].includes(preflight.headers['access-control-allow-origin']),
      'RT28 preflight allows public origin', preflight.headers['access-control-allow-origin'])
    const serverA = await startBusinessPage(18142, {
      title: 'Business A', apiRoot, environment: envA, pipeline: 'default',
    })
    const serverB = await startBusinessPage(18143, {
      title: 'Business B', apiRoot, environment: envB, pipeline: 'default',
    })
    pages.push({ server: serverA }, { server: serverB })
    const browser = await runBrowserScenarios({
      pages: [
        { key: 'a', server: serverA },
        { key: 'b', server: serverB },
      ],
      expected: {
        a: { marker: 'A_RELEASE_ONE', scope: `${envA}/default`, imageSha: assets.red.sha256 },
        b: { marker: 'B_RELEASE_ONE', scope: `${envB}/default`, imageSha: assets.yellow.sha256 },
      },
    })
    for (const item of browser) {
      await fsp.writeFile(path.join(artifactsDir, `browser-${item.key}.png`), item.screenshot)
      result.evidence.push(path.join(artifactsDir, `browser-${item.key}.png`))
      harness.check(result, item.runtime.image_status === 200, `RT28 browser ${item.key} image HTTP 200`)
      harness.check(result, item.runtime.image_type?.startsWith('image/png'), `RT28 browser ${item.key} image MIME`)
    }
    harness.check(result, browser[0].runtime.marker !== browser[1].runtime.marker, 'RT28 A/B browser markers differ')
    return {
      preflight: { status: preflight.status, allow_origin: preflight.headers['access-control-allow-origin'] },
      a: browser[0].runtime,
      b: browser[1].runtime,
    }
  })

  await harness.case('RT29', 'browser img.src loads A/B banner without headers and screenshots differ', async result => {
    const aShot = path.join(artifactsDir, 'browser-a.png')
    const bShot = path.join(artifactsDir, 'browser-b.png')
    harness.check(result, fs.existsSync(aShot) && fs.existsSync(bShot), 'RT29 A/B screenshots exist')
    const a = fs.readFileSync(aShot)
    const b = fs.readFileSync(bShot)
    harness.check(result, sha256(a) !== sha256(b), 'RT29 A/B screenshots differ', { a: sha256(a), b: sha256(b) })
    const browserSummary = JSON.parse(fs.readFileSync(path.join(artifactsDir, 'evidence', 'RT28.json'), 'utf8'))
    const details = browserSummary.details
    for (const key of ['a', 'b']) {
      harness.check(result, details[key].naturalWidth > 0 && details[key].naturalHeight > 0,
        `RT29 browser ${key} naturalWidth/Height > 0`, details[key])
      harness.check(result, details[key].image_sha256 === (key === 'a' ? assets.red.sha256 : assets.yellow.sha256),
        `RT29 browser ${key} downloaded fixture SHA`, details[key].image_sha256)
    }
    return { screenshot_sha_a: sha256(a), screenshot_sha_b: sha256(b) }
  })
}

function activeTextContains(responses, marker) {
  return responses.some(response => response.text.includes(marker))
}

async function writePostmanEnvironment() {
  const { fixture, scopeA, scopeABeta, scopeB, assets, a1, a2, b1, beta1 } = fixtures
  const base = instances[0]
  const apiRoot = `${base.origin}${base.prefix}`
  const a1Runtime = JSON.parse(fs.readFileSync(path.join(artifactsDir, 'evidence', 'RT01.json'), 'utf8'))
  let noActiveScope = ''
  try {
    const rt22 = JSON.parse(fs.readFileSync(path.join(artifactsDir, 'evidence', 'RT22.json'), 'utf8'))
    noActiveScope = rt22.details?.new_scope_id ?? ''
  } catch {}
  const banner = a1Runtime.details?.banner_url
  const currentBanner = banner ?? (await anonymous(base.target, runtimePath(scopeA, {}), { headers: scopeHeaders(scopeA) })).json?.data?.configs?.find(c => c.alias === 'banner')?.content
  const values = [
    ['api_root', apiRoot], ['environment_a', envA], ['environment_b', envB],
    ['pipeline_a', 'default'], ['pipeline_b', 'default'], ['pipeline_beta', 'beta'],
    ['version_a1', a1.version], ['snapshot_a1', a1.snapshot_id],
    ['version_a2', a2.version], ['snapshot_a2', a2.snapshot_id],
    // A2 is deleted by RT21, so the importable demo uses the still-present A1.
    ['version_fixed_a', a1.version],
    ['scope_a', scopeA.scopeId], ['scope_b', scopeB.scopeId], ['scope_beta', scopeABeta.scopeId],
    ['version_b1', b1.version], ['snapshot_b1', b1.snapshot_id],
    ['version_beta1', beta1.version], ['snapshot_beta1', beta1.snapshot_id],
    ['image_url', currentBanner ? resolveReturnedUrl(base.origin, currentBanner).href : ''],
    ['environment_no_active', envScratch], ['pipeline_no_active', 'default'],
    ['scope_no_active', noActiveScope],
  ]
  const payload = {
    id: randomUUID(),
    name: `Rainbow runtime business ${runId}`,
    values: values.map(([key, value]) => ({ key, value, type: 'default', enabled: true })),
    _postman_variable_scope: 'environment',
  }
  const target = path.join(artifactsDir, 'postman', 'runtime-business.postman_environment.json')
  await fsp.mkdir(path.dirname(target), { recursive: true })
  await fsp.writeFile(target, JSON.stringify(payload, null, 2))
  return target
}

async function main() {
  await fsp.mkdir(artifactsDir, { recursive: true })
  try {
    fixtures = await initFixtures()
    ctx = { base: instances[0], ...fixtures }
    await fsp.writeFile(path.join(artifactsDir, 'fixtures.json'), JSON.stringify({
      environment_keys: { a: envA, b: envB, empty: envEmpty, scratch: envScratch, root: envRoot },
      scopes: {
        a: fixtures.scopeA, beta: fixtures.scopeABeta, b: fixtures.scopeB, empty: fixtures.scopeEmpty,
      },
      versions: { a1: fixtures.a1, a2: fixtures.a2, b1: fixtures.b1, beta1: fixtures.beta1, empty1: fixtures.empty1 },
      editor_marker: fixtures.editorGreeting.content,
    }, null, 2))
    await runCases()
    const postmanEnv = await writePostmanEnvironment()
    runNotes.push(`Postman environment generated at ${postmanEnv}`)
  } catch (error) {
    setupError = error
    console.error(`[fatal] ${error.stack ?? error.message}`)
  } finally {
    let postmanEnv = null
    try { if (fixtures) postmanEnv = await writePostmanEnvironment() } catch (error) { console.error(`[postman] ${error.message}`) }
    if (!keepRunning) await stopAll()
    const summary = harness.finish({
      blockedReason: setupError?.message,
      keptRunning: keepRunning,
      processInfo: instances.map(processInfo),
      notes: [
        'Tests use synthetic data only; no user database or backup was copied.',
        postmanEnv ? `Postman environment: ${postmanEnv}` : 'Postman environment was not generated.',
        ...runNotes,
      ],
    })
    if (keepRunning) {
      console.log('\nRuntime instances left running for manual Postman/browser demo:')
      for (const instance of instances) {
        console.log(`- ${instance.label}: ${instance.origin}${instance.prefix} pid=${instance.child?.pid ?? 'n/a'}`)
        console.log(`  stop: ${stopCommand(instance)}`)
      }
      console.log(`Logs: ${instances.map(item => item.logPath).join(', ')}`)
    }
    console.log(JSON.stringify({
      run_id: runId,
      counts: summary.counts,
      requests: summary.requests,
      defects: summary.defects.length,
      artifacts: artifactsDir,
      blocked_reason: summary.blocked_reason,
    }, null, 2))
    process.exitCode = setupError ? 2 : (summary.counts.fail > 0 ? 1 : 0)
  }
}

main().catch(error => {
  console.error(error.stack ?? error)
  process.exitCode = 2
})