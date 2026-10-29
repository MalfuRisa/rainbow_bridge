import { chromium } from '@playwright/test'
import { randomUUID } from 'node:crypto'
import { mkdir, writeFile } from 'node:fs/promises'
import { resolve, join } from 'node:path'
import { execFileSync } from 'node:child_process'
import { remoteClient, overlayStatic } from './remote-support.mjs'

const root = resolve(import.meta.dirname, '../../..')
const base = new URL(process.env.RB_REMOTE_URL || 'http://10.60.226.43:31880/rainbow-bridge/')
if (base.href !== 'http://10.60.226.43:31880/rainbow-bridge/') throw new Error('This run is bound to the specifically authorized remote origin')
const user = process.env.RB_REMOTE_USER, password = process.env.RB_REMOTE_PASSWORD
if (!user || !password) throw new Error('Provide the confirmed web credentials only through process environment')
const runId = 'front_uuid_20261029_' + randomUUID().replaceAll('-', '').slice(0, 12)
const output = join(root, '.local-qa/request-id-frontend', runId)
await mkdir(output, { recursive: true })
const save = (file, data) => writeFile(join(output, file), JSON.stringify(data, null, 2))
const browser = await chromium.launch({ headless: true })
const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } })
const page = await context.newPage(), client = remoteClient(context, base)
const ledger = [], network = [], pageErrors = []
const loginHTTP = [], staticFailures = []
const transportFailures = [], authTransport = []
let loginAttempts = 0
const summary = { run_id: runId, evidence_type: 'client-static-replacement; real remote API pass-through', server_updated: false,
  local_head: execFileSync('git', ['rev-parse', 'HEAD'], { cwd: root, encoding: 'utf8' }).trim() }
let environment, scope, before, attemptedCreate = false, session = false
const cleanup = { environment_removed: false, original_scopes_unchanged: false, session_logged_out: false, physical_resources_uploaded: false }
const envKey = runId
const identity = () => ({ environment_key: envKey, pipeline_key: scope.pipeline_key, expected_scope_id: scope.scope_id })
const query = () => new URLSearchParams({ environment_key: envKey, pipeline_key: scope.pipeline_key, scope_id: scope.scope_id })
function own() {
  if (!environment?.environment_id || environment.environment_key !== envKey || !scope?.scope_id || scope.environment_key !== envKey) throw new Error('Write target is not owned by this run')
}
page.on('pageerror', error => pageErrors.push(error.name))
page.on('request', request => {
  const path = new URL(request.url()).pathname
  if (path.includes('/auth/')) authTransport.push({ method: request.method(), origin: new URL(request.url()).origin, path })
  if (request.method() === 'POST' && path.endsWith('/auth/login')) loginAttempts++
  if (request.method() !== 'POST' || !/\/(freeze\/lock|release\/create)$/.test(path)) return
  const p = request.postDataJSON()
  network.push({ method: 'POST', path, request_id: p.request_id, expected_scope_id: p.expected_scope_id,
    expected_revision: p.expected_revision, expected_freeze_revision: p.expected_freeze_revision })
})
page.on('response', response => {
  const path = new URL(response.url()).pathname
  if (path.endsWith('/auth/login')) loginHTTP.push(response.status())
  if (path.includes('/assets/') && response.status() !== 200) staticFailures.push({ path, status: response.status() })
  if (/\/(freeze\/lock|release\/create)$/.test(path)) network.push({ path, response: response.status() })
})
page.on('requestfailed', request => transportFailures.push({ path: new URL(request.url()).pathname, failure: request.failure()?.errorText }))
try {
  const version = await client.checked('GET', 'version')
  summary.backend_version = version.version_info
  // This deployment is known to reject MinIO creation before producing snapshots.
  // A changed server needs a separately reviewed physical-snapshot cleanup plan.
  if (version.version_info?.git_commit !== 'c49194a066f448a0730e485fca18f7f60e6e0e0b') throw new Error('Remote version changed; refusing writes without reconciling the new cleanup contract')
  summary.frontend_assets = await overlayStatic(page, join(root, 'vue/dist'), base)
  await page.goto(new URL('login', base).href)
  try {
    await page.getByLabel('账号', { exact: true }).fill(user)
    await page.getByLabel('密码', { exact: true }).fill(password)
    const loggedIn = page.waitForResponse(response => response.url().endsWith('/auth/login'))
    await page.getByRole('button', { name: '登录', exact: true }).click()
    if ((await loggedIn).status() !== 200) throw new Error('login rejected')
  } catch {
    summary.login_diagnostics = { attempts: loginAttempts, http: loginHTTP, path: new URL(page.url()).pathname,
      headings: await page.getByRole('heading').allTextContents(),
      fields: await page.locator('input').evaluateAll(inputs => inputs.map(input => ({ id: input.id, type: input.type }))),
      static_failures: staticFailures, transport_failures: transportFailures, auth_transport: authTransport }
    throw new Error('Confirmed browser login failed; no credential values retained or password retry')
  }
  await page.waitForURL(url => !url.pathname.endsWith('/login'))
  await client.session(); session = true
  before = await client.snapshot(envKey); await save('baseline-before.json', before)
  const existing = (await client.checked('GET', 'environment/list')).list
  if (existing.some(env => env.environment_key === envKey)) throw new Error('Run key already exists; refusing reuse')
  attemptedCreate = true
  ledger.push({ type: 'environment-intent', key: envKey }); await save('created-objects.json', ledger)
  environment = (await client.checked('POST', 'environment/create', { environment: {
    environment_key: envKey, environment_name: runId, description: 'isolated frontend UUID verification ' + runId, is_active: true,
  } })).environment
  if (!environment?.environment_id || environment.environment_key !== envKey) throw new Error('Missing created environment identity')
  ledger.push({ type: 'environment', key: envKey, environment_id: environment.environment_id }); await save('created-objects.json', ledger)
  scope = (await client.checked('GET', 'pipeline/list?' + new URLSearchParams({ environment_key: envKey }))).list.find(row => row.pipeline_key === 'default')
  own()
  ledger.push({ type: 'scope', environment_key: envKey, pipeline_key: scope.pipeline_key, scope_id: scope.scope_id }); await save('created-objects.json', ledger)
  const config = (await client.checked('POST', 'config/create', { expected_scope_id: scope.scope_id, config: {
    environment_key: envKey, pipeline_key: scope.pipeline_key, alias: 'UUID_PROBE', name: '操作编号验证', type: 'text', content: 'isolated UUID probe',
  } })).config
  if (!config?.resource_key) throw new Error('Created config identity not returned')
  ledger.push({ type: 'config', resource_key: config.resource_key, scope_id: scope.scope_id }); await save('created-objects.json', ledger)
  await page.goto(new URL('releases?' + new URLSearchParams({ env: envKey, pipeline: scope.pipeline_key }), base).href)
  summary.browser = await page.evaluate(() => ({ isSecureContext, randomUUID: typeof globalThis.crypto?.randomUUID, getRandomValues: typeof globalThis.crypto?.getRandomValues }))
  if (summary.browser.isSecureContext || summary.browser.randomUUID !== 'undefined' || summary.browser.getRandomValues !== 'function') throw new Error('This browser does not exercise the required real HTTP fallback')
  await page.getByRole('button', { name: '发布配置', exact: true }).click()
  const panel = page.getByRole('dialog', { name: '发布审阅', exact: true })
  const createdResponse = page.waitForResponse(response => response.url().endsWith('/release/create'), { timeout: 25000 })
  await panel.getByRole('button', { name: '锁版并创建候选版本', exact: true }).click()
  const created = await createdResponse, result = await created.json()
  summary.create_http = created.status(); summary.create_error_type = result.error_type
  const id = created.request().postDataJSON().request_id
  summary.request_id = id
  if (!/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(id)) throw new Error('Remote create did not carry a standard UUID v4')
  if (created.request().postDataJSON().expected_scope_id !== scope.scope_id) throw new Error('Remote create used a different scope')
  if (created.status() === 200) {
    ledger.push({ type: 'unexpected-snapshot', version: result.data?.meta?.version, snapshot_id: result.data?.meta?.snapshot_id, scope_id: scope.scope_id })
    await save('created-objects.json', ledger)
    throw new Error('Unexpected successful snapshot: physical cleanup needs review; do not claim full cleanup')
  }
  if (created.status() !== 503 || result.error_type !== 'RELEASE_STORAGE_UNSUPPORTED') throw new Error('Remote create result differs from the known independent backend limitation')
  await panel.getByRole('alert').waitFor()
  await page.screenshot({ path: join(output, 'client-replacement-http.png') })
  summary.compatibility_verified = true
  summary.successful_remote_publish = 'blocked: deployed backend MinIO creation unsupported; no activation attempted'
} catch (error) {
  summary.failure = error.message
  process.exitCode = 1
} finally {
  if (!session && loginHTTP.includes(200)) {
    try { await client.session(); session = true } catch { cleanup.login_session_unconfirmed = true }
  }
  try {
    if (attemptedCreate && !environment) {
      const exact = (await client.checked('GET', 'environment/list')).list.filter(env => env.environment_key === envKey && env.environment_name === runId)
      if (exact.length === 1) environment = exact[0]
    }
    if (environment) {
      if (environment.environment_key !== envKey || !environment.environment_id) throw new Error('Cleanup target identity is not owned')
      const pipelines = (await client.checked('GET', 'pipeline/list?' + new URLSearchParams({ environment_key: envKey }))).list
      for (const pipeline of pipelines) {
        scope = pipeline; own()
        const status = await client.checked('GET', 'freeze/status?' + query())
        if (status.scope_id !== scope.scope_id) throw new Error('Cleanup scope changed')
        if (status.state === 'locked') await client.checked('POST', 'freeze/unlock', { ...identity(), expected_revision: status.revision, reason: runId + ' cleanup' })
      }
      if (ledger.some(item => item.type === 'unexpected-snapshot')) throw new Error('Snapshot physical cleanup not authorized by this test contract')
      await client.checked('POST', 'environment/delete', { environment_key: envKey, expected_environment_id: environment.environment_id })
      cleanup.environment_removed = !(await client.checked('GET', 'environment/list')).list.some(env => env.environment_key === envKey)
      const overview = await client.checked('GET', 'runtime/overview')
      cleanup.overview_removed = !overview.list.some(env => env.environment_key === envKey)
      const old = await client.call('GET', 'release/active?' + query())
      cleanup.old_scope_rejected = old.status === 410 && old.body.error_type === 'SCOPE_DELETED'
      cleanup.history = 'normal logical-deletion and audit history retained; no resource upload or successful release snapshot'
      if (!cleanup.environment_removed || !cleanup.overview_removed || !cleanup.old_scope_rejected) throw new Error('Test environment visibility or old identity cleanup not confirmed')
    }
    if (before) {
      const after = await client.snapshot(envKey); await save('baseline-after.json', after)
      cleanup.original_scopes_unchanged = JSON.stringify(before) === JSON.stringify(after)
      cleanup.original_scope_count = before.length
      if (!cleanup.original_scopes_unchanged) throw new Error('Observed baseline changed; do not roll back another session, review evidence')
    }
  } catch (error) { cleanup.failure = error.message; process.exitCode = 1 }
  if (session) {
    try { await client.checked('POST', 'auth/logout', {}); cleanup.session_logged_out = true }
    catch { cleanup.logout_unconfirmed = true; process.exitCode = 1 }
  }
  cleanup.no_business_create_attempted = !attemptedCreate
  await save('network.json', network); await save('browser.json', { ...summary.browser, pageErrors })
  await save('cleanup.json', cleanup); await save('summary.json', summary)
  await context.close(); await browser.close()
  delete process.env.RB_REMOTE_USER; delete process.env.RB_REMOTE_PASSWORD
  process.stdout.write(JSON.stringify({ evidence: output, compatibility_verified: summary.compatibility_verified === true,
    server_updated: false, cleanup_confirmed: cleanup.environment_removed && cleanup.original_scopes_unchanged && !cleanup.failure }) + '\n')
}
