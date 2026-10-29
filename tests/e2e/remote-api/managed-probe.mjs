import { mkdir, readFile, writeFile } from 'node:fs/promises'
import { resolve, join } from 'node:path'
import { randomUUID, createHash } from 'node:crypto'
import { chromium } from '@playwright/test'

const root = resolve(import.meta.dirname, '../../..')
const base = new URL(process.env.RB_REMOTE_URL || 'http://10.60.226.43:31880/rainbow-bridge/')
const user = process.env.RB_REMOTE_USER, password = process.env.RB_REMOTE_PASSWORD
if (!user || !password) throw new Error('Provide confirmed web credentials through process environment only')
const runId = `qa_20261029_${randomUUID().slice(0, 8)}`
const output = join(root, '.local-qa/remote-api', runId)
await mkdir(output, { recursive: true })
const save = (name, data) => writeFile(join(output, name), JSON.stringify(data, null, 2))
const objects = [], cases = [], issues = [], scopeIds = new Set()
const envKey = runId
let environment, csrf, baseline, cleanup = {}, context, page
const browser = await chromium.launch({ headless: true })
const path = endpoint => new URL('api/v1/' + endpoint, base).href
const query = scope => `environment_key=${envKey}&pipeline_key=${scope.pipeline_key}&scope_id=${scope.scope_id}`
const identity = scope => ({ environment_key: envKey, pipeline_key: scope.pipeline_key, expected_scope_id: scope.scope_id })
const ledger = async () => save('created-objects.json', { runId, environmentKey: envKey, objects })
const own = scope => { if (!scope || scope.environment_key !== envKey || !scopeIds.has(scope.scope_id)) throw new Error('Write target is not owned by this run') }

async function call(method, endpoint, data, options = {}) {
  const response = await context.request.fetch(path(endpoint), { method, timeout: 15000,
    headers: { Origin: base.origin, ...(csrf ? { 'X-CSRF-Token': csrf } : {}), ...options.headers },
    ...(options.multipart ? { multipart: options.multipart } : data === undefined ? {} : { data }), failOnStatusCode: false })
  const bytes = await response.body()
  if (bytes.length > 2 * 1024 * 1024) throw new Error('Response exceeded 2MiB test limit')
  let body
  try { body = JSON.parse(bytes.toString()) } catch {}
  return { response, bytes, body, http: response.status(), data: body?.data }
}
function ok(result) { return result.http >= 200 && result.http < 300 && (!result.body?.code || result.body.code === 200) }
async function record(name, method, endpoint, action, assertion) {
  const row = { name, method, path: '/api/v1/' + endpoint.split('?')[0], at: new Date().toISOString() }
  try {
    const result = await action()
    Object.assign(row, { http: result.http, code: result.body?.code, errorType: result.body?.error_type,
      responseSha256: createHash('sha256').update(result.bytes).digest('hex'), bytes: result.bytes.length })
    if (endpoint.startsWith('transfer/')) row.counts = Object.fromEntries(['total', 'succeeded', 'skipped', 'failed', 'imported'].filter(k => result.data?.[k] !== undefined).map(k => [k, result.data[k]]))
    row.status = assertion(result) ? 'passed' : 'product-defect-or-contract-difference'
    if (row.status !== 'passed') issues.push(row)
    return result
  } catch (error) { row.status = 'test-or-network-error'; row.error = error.message; issues.push(row) }
  finally { cases.push(row); await save('cases.json', cases); await save('issues.json', issues) }
}
async function get(endpoint) { return call('GET', endpoint) }
async function write(endpoint, scope, data) { own(scope); return call('POST', endpoint, data) }
async function registerConfigs(scope) {
  own(scope)
  const result = await get('config/list?' + query(scope))
  if (!ok(result)) throw new Error('Cannot reconcile owned configuration IDs')
  cases.push({ name: 'transfer target contents reconciled', method: 'GET', path: '/api/v1/config/list', http: result.http, code: result.body?.code,
    scope_id: scope.scope_id, count: result.data?.list?.length || 0, status: 'passed' })
  for (const item of result.data?.list || []) if (!objects.some(o => o.type === 'config' && o.id === item.resource_key && o.scopeId === scope.scope_id)) {
    objects.push({ type: 'config', id: item.resource_key, alias: item.alias, scopeId: scope.scope_id, reconciledFromList: true })
  }
  await ledger()
}
async function snapshot() {
  const result = await get('environment/list')
  if (!ok(result)) throw new Error('Cannot read environment baseline')
  const rows = []
  for (const env of result.data?.list || []) {
    if (env.environment_key === envKey) continue
    const pipes = await get('pipeline/list?environment_key=' + encodeURIComponent(env.environment_key))
    if (!ok(pipes)) throw new Error('Cannot read pipeline baseline')
    for (const scope of pipes.data?.list || []) {
      const q = `environment_key=${encodeURIComponent(env.environment_key)}&pipeline_key=${encodeURIComponent(scope.pipeline_key)}&scope_id=${scope.scope_id}`
      const freeze = await get('freeze/status?' + q), active = await get('release/active?' + q)
      rows.push({ environment_key: env.environment_key, environment_id: env.environment_id,
        pipeline_key: scope.pipeline_key, scope_id: scope.scope_id,
        freeze: { http: freeze.http, state: freeze.data?.state, revision: freeze.data?.revision },
        active: active.data?.active ? { snapshot_id: active.data.active.snapshot_id, version: active.data.active.version, revision: active.data.active.revision } : null })
    }
  }
  return rows.sort((a, b) => a.scope_id.localeCompare(b.scope_id))
}
try {
  context = await browser.newContext({ viewport: { width: 1440, height: 1000 } })
  page = await context.newPage()
  await page.goto(new URL('login', base).href)
  await page.getByLabel('账号', { exact: true }).fill(user)
  await page.getByLabel('密码', { exact: true }).fill(password)
  const loginResponse = page.waitForResponse(r => r.url().endsWith('/auth/login'))
  await page.getByRole('button', { name: '登录', exact: true }).click()
  const loggedIn = await loginResponse
  if (loggedIn.status() !== 200) throw new Error('Confirmed web login rejected; no retry or password guess')
  await page.waitForURL(url => !url.pathname.endsWith('/login'))
  cases.push({ name: 'real browser login', method: 'POST', path: '/api/v1/auth/login', http: 200, status: 'passed' })
  const me = await get('auth/me')
  if (!ok(me) || !me.data?.csrf_token) throw new Error('No usable web session')
  csrf = me.data.csrf_token
  await record('session me', 'GET', 'auth/me', () => get('auth/me'), ok)
  baseline = await snapshot(); await save('baseline-before.json', baseline)
  const caps = await record('backup capabilities', 'GET', 'backup/capabilities', () => get('backup/capabilities'), ok)
  await save('backup-capabilities.json', caps?.data || {})
  for (const endpoint of ['backup/list', 'backup/status?job_id=' + randomUUID(), 'backup/download?backup_id=' + randomUUID(), 'backup/restore/preview?preview_id=' + randomUUID()]) {
    await record('safe backup read / absent test identifier', 'GET', endpoint, () => get(endpoint), r => r.http === 503 && r.body?.error_type === 'BACKUP_UNSUPPORTED' || [400,404].includes(r.http) || ok(r))
  }
  // No upload: scope deletion retains physical originals and there is no asset delete API.
  objects.push({ type: 'environment-intent', key: envKey, state: 'planned' }); await ledger()
  const created = await record('create owned environment', 'POST', 'environment/create', () => call('POST', 'environment/create', {
    environment: { environment_key: envKey, environment_name: runId, is_active: true, description: 'isolated remote verification' },
  }), r => ok(r) && !!r.data?.environment?.environment_id)
  environment = created?.data?.environment
  if (!environment?.environment_id) throw new Error('Creation result not confirmed; cleanup must reconcile by exact run key')
  objects.push({ type: 'environment', key: envKey, id: environment.environment_id, state: 'created' }); await ledger()
  const pipes = await get('pipeline/list?environment_key=' + envKey)
  const source = pipes.data?.list?.find(row => row.pipeline_key === 'default')
  if (!source?.scope_id) throw new Error('Default scope not returned')
  scopeIds.add(source.scope_id); objects.push({ type: 'scope', id: source.scope_id, key: source.pipeline_key, environmentId: environment.environment_id }); await ledger()
  const targetResult = await record('create owned target channel', 'POST', 'pipeline/create', () => call('POST', 'pipeline/create', {
    environment_key: envKey, expected_environment_id: environment.environment_id,
    pipeline: { pipeline_key: 'target', pipeline_name: 'target', is_active: true },
  }), r => ok(r) && !!r.data?.pipeline?.scope_id)
  const target = targetResult?.data?.pipeline
  if (!target?.scope_id) throw new Error('Target scope creation failed')
  scopeIds.add(target.scope_id); objects.push({ type: 'scope', id: target.scope_id, key: target.pipeline_key }); await ledger()
  for (const endpoint of ['runtime/config', 'runtime/release']) await record('owned scope without active', 'GET', endpoint,
    () => call('GET', endpoint + '?scope_id=' + source.scope_id, undefined, { headers: { 'x-environment': envKey, 'x-pipeline': source.pipeline_key } }),
    r => r.http === 404 && r.body?.error_type === 'RELEASE_NOT_ACTIVE')
  for (const endpoint of ['runtime/static', 'runtime/release/static']) await record('no published package', 'GET', endpoint + '?' + query(source),
    () => get(endpoint + '?' + query(source) + (endpoint.includes('/release/') ? '&version=v1&expected_snapshot_id=' + randomUUID() : '')),
    r => [400,404].includes(r.http))
  for (const [endpoint, assertion] of [
    ['environment/list', r => ok(r) && r.data.list.some(e => e.environment_id === environment.environment_id)],
    ['environment/detail?environment_key=' + envKey, r => ok(r) && r.data.environment.environment_id === environment.environment_id],
    ['pipeline/list?environment_key=' + envKey, r => ok(r) && r.data.list.length === 2],
    ['pipeline/detail?' + query(source), r => ok(r) && r.data.pipeline.scope_id === source.scope_id],
    ['asset/list?' + query(source), r => ok(r) && (r.data.list || []).length === 0],
  ]) await record('owned scope read', 'GET', endpoint, () => get(endpoint), assertion)
  await record('update owned environment', 'POST', 'environment/update', () => call('POST', 'environment/update', {
    expected_environment_id: environment.environment_id, environment: { ...environment, description: 'updated by run ' + runId },
  }), ok)
  await record('update owned channel', 'POST', 'pipeline/update', () => write('pipeline/update', target, {
    environment_key: envKey, expected_scope_id: target.scope_id, pipeline: { ...target, pipeline_name: 'target updated' },
  }), ok)
  await record('identity mismatch rejects', 'POST', 'pipeline/update', () => write('pipeline/update', target, {
    environment_key: envKey, expected_scope_id: randomUUID(), pipeline: { ...target, pipeline_name: 'must not write' },
  }), r => r.http === 409 || r.http === 410)
  const draft = { ...identity(source), config: { environment_key: envKey, pipeline_key: source.pipeline_key, alias: 'TITLE', name: 'test-title', type: 'text', content: 'first', description: runId } }
  objects.push({ type: 'config-intent', alias: 'TITLE', scopeId: source.scope_id }); await ledger()
  await record('create owned configuration', 'POST', 'config/create', () => write('config/create', source, draft), ok)
  let rows = await get('config/list?' + query(source))
  let config = rows.data?.list?.find(c => c.alias === 'TITLE')
  if (!config?.resource_key) throw new Error('Owned configuration not confirmed')
  objects.push({ type: 'config', id: config.resource_key, scopeId: source.scope_id }); await ledger()
  await record('missing CSRF rejected', 'POST', 'config/update', () => call('POST', 'config/update', {
    ...identity(source), config, expected_edit_revision: config.edit_revision,
  }, { headers: { 'X-CSRF-Token': '' } }), r => r.http === 403 && r.body?.error_type === 'AUTH_CSRF_INVALID')
  await record('wrong Origin rejected', 'POST', 'config/update', () => call('POST', 'config/update', {
    ...identity(source), config, expected_edit_revision: config.edit_revision,
  }, { headers: { Origin: 'http://invalid-origin.invalid' } }), r => r.http === 403 && r.body?.error_type === 'AUTH_ORIGIN_DENIED')
  await record('config list', 'GET', 'config/list?' + query(source), () => get('config/list?' + query(source)), r => ok(r) && r.data.list.length === 1)
  await record('fresh config detail', 'GET', 'config/detail?' + query(source) + '&resource_key=' + config.resource_key,
    () => get('config/detail?' + query(source) + '&resource_key=' + config.resource_key), r => ok(r) && !!r.data.config.edit_revision)
  const old = config.edit_revision
  const updated = await record('update config, alias immutable', 'POST', 'config/update', () => write('config/update', source, {
    ...identity(source), config: { ...config, alias: 'ATTEMPTED_CHANGE', content: 'winner' }, expected_edit_revision: old,
  }), r => ok(r) && r.data.config.alias === 'TITLE' && r.data.config.edit_revision !== old)
  config = updated?.data?.config || config
  await record('stale edit rejected', 'POST', 'config/update', () => write('config/update', source, {
    ...identity(source), config: { ...config, content: 'loser' }, expected_edit_revision: old,
  }), r => r.http === 409 && r.body?.error_type === 'CONFIG_EDIT_CONFLICT')
  for (const [type, content, valid] of [['object', '{"big":900719925474099312345,"ok":true}', true], ['object', '{bad}', false]]) {
    const result = await record('JSON validation ' + valid, 'POST', 'config/create', () => write('config/create', source, {
      ...identity(source), config: { environment_key: envKey, pipeline_key: source.pipeline_key, alias: valid ? 'BIG' : 'INVALID', name: 'json', type, content },
    }), r => valid ? ok(r) && r.data.config.content.includes('900719925474099312345') : !ok(r))
    if (result?.data?.config?.resource_key) { objects.push({ type: 'config', id: result.data.config.resource_key, scopeId: source.scope_id }); await ledger() }
  }
  const network = [], scopeErrors = []
  page.on('request', req => { const pathname = new URL(req.url()).pathname; if (/\/(freeze|release)\//.test(pathname)) network.push({ method: req.method(), path: pathname }) })
  page.on('response', response => { const pathname = new URL(response.url()).pathname; if (/\/(freeze|release)\//.test(pathname)) network.push({ response: response.status(), path: pathname }) })
  page.on('pageerror', e => scopeErrors.push(e.message))
  await page.goto(new URL(`releases?env=${envKey}&pipeline=default`, base).href)
  await page.getByRole('button', { name: '发布配置', exact: true }).click()
  const dialog = page.getByRole('dialog')
  await dialog.getByRole('button', { name: '锁版并创建候选版本', exact: true }).click()
  await page.waitForTimeout(1200)
  const browserState = await page.evaluate(() => ({ isSecureContext, randomUUID: typeof crypto.randomUUID, getRandomValues: typeof crypto.getRandomValues }))
  const dialogText = await dialog.innerText()
  await save('browser.json', { state: browserState, network, pageErrors: scopeErrors, displayedRandomUUIDError: /randomUUID/.test(dialogText),
    freezeRequestSent: network.some(x => x.method === 'POST' && x.path.endsWith('/freeze/lock')),
    createRequestSent: network.some(x => x.method === 'POST' && x.path.endsWith('/release/create')) })
  await page.screenshot({ path: join(output, 'browser-create-error.png') })
  await dialog.getByRole('button', { name: '暂不发布', exact: true }).click()
  let freeze = await record('freeze status after browser failure', 'GET', 'freeze/status?' + query(source), () => get('freeze/status?' + query(source)), r => ok(r) && r.data.state === 'locked')
  await record('unlock stale revision rejected', 'POST', 'freeze/unlock', () => write('freeze/unlock', source, { ...identity(source), expected_revision: '0' }), r => r.http === 409 && r.body?.error_type === 'FREEZE_REVISION_CONFLICT')
  await record('frozen config update rejected', 'POST', 'config/update', () => write('config/update', source, {
    ...identity(source), config, expected_edit_revision: config.edit_revision,
  }), r => r.http === 409 && r.body?.error_type === 'SCOPE_FROZEN')
  const release = await record('independent HTTP release create', 'POST', 'release/create', () => write('release/create', source, {
    ...identity(source), request_id: randomUUID(), expected_freeze_revision: String(freeze.data.revision),
  }), r => r.http === 503 && r.body?.error_type === 'RELEASE_STORAGE_UNSUPPORTED')
  if (release?.http === 200) throw new Error('Unexpected release creation: stop to preserve files and classify cleanup')
  for (const endpoint of ['freeze/history', 'release/list', 'release/active', 'release/history', 'release/detail']) {
    await record('owned frozen scope read', 'GET', endpoint + '?' + query(source), () => get(endpoint + '?' + query(source)), r => ok(r) || [400,404].includes(r.http))
  }
  for (const endpoint of ['release/activate', 'release/delete', 'release/compare']) {
    await record('no-version negative path', 'POST', endpoint, () => write(endpoint, source, {
      ...identity(source), request_id: randomUUID(), version: 'v1', expected_active_revision: '0', expected_snapshot_id: randomUUID(),
    }), r => [400,404,503].includes(r.http))
  }
  freeze = await get('freeze/status?' + query(source))
  await record('unlock owned scope', 'POST', 'freeze/unlock', () => write('freeze/unlock', source, {
    ...identity(source), expected_revision: String(freeze.data.revision),
  }), r => ok(r) && r.data.state === 'unlocked')
  const selection = { environment_key: envKey, pipeline_key: source.pipeline_key,
    expected_environment_id: environment.environment_id, expected_scope_id: source.scope_id }
  await record('editor export preview', 'POST', 'transfer/export-preview', () => write('transfer/export-preview', source, {
    source: 'editor', format: 'zip', selections: [selection],
  }), ok)
  const archive = await record('editor export isolated objects only', 'POST', 'transfer/export', () => write('transfer/export', source, {
    source: 'editor', format: 'zip', selections: [selection],
  }), r => r.http === 200 && r.response.headers()['content-type']?.includes('zip'))
  await record('export tree metadata', 'GET', 'transfer/export-tree', () => get('transfer/export-tree'), ok)
  if (archive?.http === 200 && !archive.body) {
    await record('import preview only own archive', 'POST', 'transfer/import-preview', () => call('POST', 'transfer/import-preview', undefined, {
      multipart: { file: { name: 'run.zip', mimeType: 'application/zip', buffer: archive.bytes } },
    }), ok)
    await record('archive import only own target', 'POST', 'transfer/import', () => {
      own(target); return call('POST', 'transfer/import', undefined, { multipart: { archive: { name: 'run.zip', mimeType: 'application/zip', buffer: archive.bytes },
        environment_key: envKey, pipeline_key: target.pipeline_key, expected_environment_id: environment.environment_id, expected_scope_id: target.scope_id, overwrite: 'false' } })
    }, r => ok(r) && r.data.total === 2)
    await record('selective import only own original scope', 'POST', 'transfer/import-selective', () => {
      own(source); return call('POST', 'transfer/import-selective', undefined, { multipart: { file: { name: 'run.zip', mimeType: 'application/zip', buffer: archive.bytes }, selections: JSON.stringify([selection]), overwrite: 'true' } })
    }, r => ok(r) && r.data.total === 2)
  }
  await record('JSON import owned target', 'POST', 'transfer/import', () => write('transfer/import', target, {
    expected_environment_id: environment.environment_id, expected_scope_id: target.scope_id, overwrite: true,
    configs: [{ ...config, environment_key: envKey, pipeline_key: target.pipeline_key, content: 'json import' }],
  }), ok)
  await record('migrate only owned source and target', 'POST', 'transfer/migrate', () => write('transfer/migrate', target, {
    source_environment_key: envKey, source_pipeline_key: source.pipeline_key,
    target_environment_key: envKey, target_pipeline_key: target.pipeline_key,
    source_expected_environment_id: environment.environment_id, source_expected_scope_id: source.scope_id,
    target_expected_environment_id: environment.environment_id, target_expected_scope_id: target.scope_id, overwrite: true,
  }), ok)
  const targetList = await get('config/list?' + query(target))
  const targetTextList = await get('config/list?' + query(target) + '&type=text')
  const targetDetail = await get('config/detail?' + query(target) + '&resource_key=' + config.resource_key)
  await save('transfer-readback.json', {
    targetScope: target.scope_id, normalList: { http: targetList.http, code: targetList.body?.code, errorType: targetList.body?.error_type,
      error: typeof targetList.body?.error === 'string' ? targetList.body.error.slice(0, 400) : undefined,
      total: targetList.data?.total, aliases: targetList.data?.list?.map(c => c.alias) },
    typeTextList: { http: targetTextList.http, total: targetTextList.data?.total, aliases: targetTextList.data?.list?.map(c => c.alias) },
    detail: { http: targetDetail.http, code: targetDetail.body?.code, scope_id: targetDetail.data?.config?.scope_id, alias: targetDetail.data?.config?.alias },
  })
  if (ok(targetDetail) && targetDetail.data?.config && !(targetList.data?.list || []).some(c => c.resource_key === targetDetail.data.config.resource_key)) {
    issues.push({ status: 'product-defect', name: 'target detail exists while normal target list omits it', priority: 'P1', evidence: 'transfer-readback.json' })
  }
  for (const row of [...(targetTextList.data?.list || []), ...(targetDetail.data?.config ? [targetDetail.data.config] : [])]) {
    if (!objects.some(o => o.type === 'config' && o.id === row.resource_key && o.scopeId === target.scope_id)) objects.push({ type: 'config', id: row.resource_key, scopeId: target.scope_id, directReadback: true })
  }
  await ledger()
  for (const sourceKind of ['active', 'version']) await record('export no published source ' + sourceKind, 'POST', 'transfer/export', () => write('transfer/export', source, {
    ...identity(source), source: sourceKind, expected_snapshot_id: randomUUID(), ...(sourceKind === 'version' ? { version: 'v1' } : {}),
  }), r => [404,409].includes(r.http))
  await record('delete owned config', 'POST', 'config/delete', () => write('config/delete', source, { ...identity(source), resource_key: config.resource_key }), ok)
  await registerConfigs(source); await registerConfigs(target)
  // Exercise channel deletion/recreation and stale identity without touching user keys.
  await record('delete own target channel', 'POST', 'pipeline/delete', () => write('pipeline/delete', target, identity(target)), ok)
  const rebuilt = await record('recreate same own channel key', 'POST', 'pipeline/create', () => call('POST', 'pipeline/create', {
    environment_key: envKey, expected_environment_id: environment.environment_id, pipeline: { pipeline_key: 'target', pipeline_name: 'rebuilt', is_active: true },
  }), r => ok(r) && r.data.pipeline.scope_id !== target.scope_id)
  if (rebuilt?.data?.pipeline) { scopeIds.add(rebuilt.data.pipeline.scope_id); objects.push({ type: 'scope', id: rebuilt.data.pipeline.scope_id, key: 'target', recreated: true }); await ledger() }
  await record('old scope pin rejected after recreation', 'GET', 'config/list?' + query(target), () => get('config/list?' + query(target)), r => r.http === 410)
} catch (error) {
  issues.push({ status: 'execution-stopped', error: error.message }); await save('issues.json', issues)
} finally {
  if (csrf && context) {
    try {
      // Recover ambiguous environment creation by the exact random run key only.
      if (!environment) { const result = await get('environment/detail?environment_key=' + envKey); environment = result.data?.environment }
      if (environment?.environment_key === envKey && environment.environment_id) {
        const pipes = await get('pipeline/list?environment_key=' + envKey)
        for (const scope of pipes.data?.list || []) {
          if (scope.environment_id !== environment.environment_id) throw new Error('Cleanup ownership mismatch')
          scopeIds.add(scope.scope_id)
          const configs = await get('config/list?' + query(scope))
          for (const item of configs.data?.list || []) if (!objects.some(o => o.type === 'config' && o.id === item.resource_key && o.scopeId === scope.scope_id)) {
            objects.push({ type: 'config', id: item.resource_key, alias: item.alias, scopeId: scope.scope_id, discoveredDuringCleanup: true })
          }
          await ledger()
          const state = await get('freeze/status?' + query(scope))
          if (state.data?.state === 'locked') {
            const unlocked = await write('freeze/unlock', scope, { ...identity(scope), expected_revision: String(state.data.revision) })
            if (!ok(unlocked)) throw new Error('Cleanup could not unlock owned scope')
          }
        }
        const result = await call('POST', 'environment/delete', { environment_key: envKey, expected_environment_id: environment.environment_id })
        if (!ok(result)) throw new Error('Cleanup environment deletion failed')
        cases.push({ name: 'cleanup owned environment', method: 'POST', path: '/api/v1/environment/delete', http: result.http, code: result.body?.code, status: 'passed' })
        cleanup.deletion = result.data
        const visible = await get('environment/list')
        cleanup.environmentNotVisible = !(visible.data?.list || []).some(e => e.environment_id === environment.environment_id)
        cleanup.oldPins = []
        for (const scope of pipes.data?.list || []) {
          for (const endpoint of ['config/list', 'asset/list', 'release/list']) {
            const old = await get(endpoint + '?' + query(scope))
            cleanup.oldPins.push({ endpoint, scope_id: scope.scope_id, http: old.http, error_type: old.body?.error_type })
            if (old.http !== 410 || old.body?.error_type !== 'SCOPE_DELETED') throw new Error('Deleted scope remains readable: ' + endpoint)
          }
        }
        cleanup.retainedHistory = ['soft-deleted environment/scopes/configs', 'scope deletion audit', 'freeze lock/unlock history']
        cleanup.physicalResourcesPending = []
        cleanup.noResourceUploadOrSuccessfulRelease = true
      } else cleanup.environmentNeverCreated = true
      const after = await snapshot(); await save('baseline-after.json', after)
      cleanup.originalScopesUnchanged = JSON.stringify(baseline) === JSON.stringify(after)
      cleanup.result = cleanup.originalScopesUnchanged && (cleanup.environmentNotVisible || cleanup.environmentNeverCreated) ? 'visible-test-data-cleaned' : 'needs-review'
    } catch (error) { cleanup.result = 'failed'; cleanup.error = error.message }
    try { const result = await call('POST', 'auth/logout', {}); cleanup.ownSessionLoggedOut = ok(result); cases.push({ name: 'own session logout', method: 'POST', path: '/api/v1/auth/logout', http: result.http, status: ok(result) ? 'passed' : 'failed' }) } catch { cleanup.ownSessionLoggedOut = false }
  }
  await save('cleanup.json', cleanup); await ledger(); await save('cases.json', cases); await save('issues.json', issues)
  await save('summary.json', { runId, cases: cases.length, passed: cases.filter(c => c.status === 'passed').length, issues: issues.length, cleanup: cleanup.result })
  if (context) await context.close()
  await browser.close()
  console.log(JSON.stringify({ runId, output, cases: cases.length, issues: issues.length, cleanup: cleanup.result }))
}
