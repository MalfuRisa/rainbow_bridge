import { readdir, readFile, mkdir, writeFile } from 'node:fs/promises'
import { resolve, relative, join } from 'node:path'
import { createHash, randomUUID } from 'node:crypto'
import { chromium } from '@playwright/test'

const root = resolve(import.meta.dirname, '../../..')
const base = new URL(process.env.RB_REMOTE_URL || 'http://10.60.226.43:31880/rainbow-bridge/')
const runId = `qa_20261029_${randomUUID().slice(0, 8)}`
const output = join(root, '.local-qa/remote-api', runId)
await mkdir(output, { recursive: true })
const save = (name, data) => writeFile(join(output, name), JSON.stringify(data, null, 2))
const digest = data => createHash('sha256').update(data).digest('hex')
const publicPaths = new Set(['/ping', '/api/v1/version', '/api/v1/version/latest',
  '/api/v1/runtime/overview', '/api/v1/runtime/config', '/api/v1/runtime/static',
  '/api/v1/runtime/release', '/api/v1/runtime/release/static'])

// Read only registration source; include hand registrations absent from IDL.
const routes = new Map()
for (const module of await readdir(join(root, 'biz/router'), { withFileTypes: true })) {
  if (!module.isDirectory()) continue
  const file = join(root, 'biz/router', module.name, `${module.name}.go`)
  let source
  try { source = await readFile(file, 'utf8') } catch { continue }
  const groups = new Map([['r', '']])
  for (const line of source.split('\n')) {
    const group = line.match(/(\w+)\s*:=\s*(\w+)\.Group\("([^"]+)"/)
    if (group) groups.set(group[1], (groups.get(group[2]) || '') + group[3].replace(/\/$/, ''))
    const route = line.match(/(\w+)\.(GET|POST|HEAD|PUT|DELETE)\("([^"]+)"/)
    if (!route) continue
    const path = (groups.get(route[1]) || '') + route[3]
    const key = `${route[2]} ${path}`
    routes.set(key, { method: route[2], path, source: relative(root, file).replaceAll('\\', '/'), idl: false })
  }
}
routes.set('GET /ping', { method: 'GET', path: '/ping', source: 'biz/router/register.go', idl: false })
for (const file of await readdir(join(root, 'idl/biz'))) {
  if (!file.endsWith('.proto')) continue
  for (const match of (await readFile(join(root, 'idl/biz', file), 'utf8')).matchAll(/option\s+\(api\.(get|post|head|put|delete)\)\s*=\s*"([^"]+)"/g)) {
    const method = match[1].toUpperCase(), path = match[2], key = `${method} ${path}`
    const equivalent = [...routes.values()].find(row => row.method === method &&
      (row.path.replace('/*filepath', '/:file_id') === path ||
       row.path.endsWith('/*filepath') && path.startsWith(row.path.slice(0, -'*filepath'.length))))
    if (routes.has(key)) routes.get(key).idl = true
    else if (equivalent) equivalent.idl = true
    else routes.set(key, { method, path, source: `idl/biz/${file}`, idl: true, registrationMissing: true })
  }
}
const coverage = [...routes.values()].sort((a, b) => `${a.path} ${a.method}`.localeCompare(`${b.path} ${b.method}`))
for (const row of coverage) {
  row.public = row.method === 'GET' && (publicPaths.has(row.path) || row.path.includes('/asset/file/') || row.path.includes('/release/asset/'))
  row.status = 'blocked'
  row.reason = row.path.startsWith('/api/v1/backup/') ? 'Requires confirmed admin session; destructive/full-instance scenarios intentionally not executed.' :
    row.path === '/api/v1/auth/password' ? 'Password change prohibited on shared instance.' : 'Requires confirmed web administrator credentials; no authenticated functional execution.'
}
await save('coverage.json', coverage)
await save('created-objects.json', { runId, objects: [], intents: [], authenticated: false })

async function request(method, path, { headers = {}, body } = {}) {
  const url = new URL(path.replace(/^\//, ''), base)
  const response = await fetch(url, { method, headers, body, redirect: 'manual', signal: AbortSignal.timeout(12000) })
  const reader = response.body?.getReader(), chunks = []
  let length = 0
  if (reader) for (;;) {
    const part = await reader.read()
    if (part.done) break
    length += part.value.length
    if (length > 2 * 1024 * 1024) { await reader.cancel(); throw new Error('Response exceeded 2MiB evidence limit') }
    chunks.push(Buffer.from(part.value))
  }
  const bytes = Buffer.concat(chunks), text = bytes.toString('utf8')
  let json
  try { json = JSON.parse(text) } catch {}
  return { response, bytes, text, json }
}
const observations = []
async function observe(method, path, scenario, expected, options) {
  const evidence = { method, path, scenario, expected, at: new Date().toISOString() }
  try {
    const result = await request(method, path, options)
    Object.assign(evidence, { http: result.response.status, contentType: result.response.headers.get('content-type'),
      code: result.json?.code, errorType: result.json?.error_type, bytes: result.bytes.length,
      bodySha256: digest(result.bytes), passed: expected.includes(result.response.status) })
    // Never save business contents or credential/session headers.
    if (path.includes('/version') || path === '/ping') evidence.publicBody = result.json
    if (path.includes('/runtime/overview') && result.json?.data?.list) evidence.scopeMetadata = result.json.data.list.map(env => ({
      environment_key: env.environment_key, environment_id: env.environment_id,
      pipelines: (env.pipelines || []).map(p => ({ pipeline_key: p.pipeline_key, scope_id: p.scope_id })),
    }))
    return { evidence, ...result }
  } catch (error) { evidence.passed = false; evidence.networkError = error.message; return { evidence } }
  finally { observations.push(evidence); await save('http-observations.json', observations) }
}

for (const row of coverage) {
  if (row.path === '/api/v1/auth/login' || row.path === '/api/v1/auth/password' || row.path === '/api/v1/auth/logout') continue
  const path = row.path.replace('/*filepath', '/qa-nonexistent-' + runId).replace(':file_id', 'qa-nonexistent-' + runId)
  const expected = !row.public ? [401, 403] : ['/ping', '/api/v1/version', '/api/v1/version/latest', '/api/v1/runtime/overview'].includes(path) ? [200] : path.includes('/asset/') ? [400, 404] : [400]
  const { evidence } = await observe(row.method, path, row.public ? 'public read / missing required input / nonexistent test file' : 'anonymous authentication rejection only', expected,
    row.method === 'POST' ? { headers: { 'Content-Type': 'application/json' }, body: '{}' } : undefined)
  row.probe = evidence
  row.deploymentObserved = evidence.http !== undefined ? evidence.http !== 404 : null
  if (row.public) { row.status = evidence.passed ? 'passed' : 'product-defect-or-deployment-error'; row.reason = 'Only stated public scenario verified; no successful published-scope read claimed.' }
  else row.authBoundary = evidence.passed ? 'passed' : 'failed'
  await save('coverage.json', coverage)
}
await observe('GET', '/api/v1/qa-unknown-' + runId, 'unknown API must return JSON 404', [404])
await observe('POST', '/api/v1/qa-unknown-' + runId, 'unknown API must return JSON 404', [404], { headers: { 'Content-Type': 'application/json' }, body: '{}' })
// Existing scopes are never selected: use a unique absent key for lookup semantics.
for (const path of ['/api/v1/runtime/release', '/api/v1/runtime/config']) {
  await observe('GET', path, 'nonexistent run scope, no editor fallback', [404], { headers: { 'x-environment': runId, 'x-pipeline': 'absent' } })
}
const index = await request('GET', '/')
const assets = []
for (const match of index.text.matchAll(/(?:src|href)="([^"]+\.(?:js|css))"/g)) {
  const url = new URL(match[1], base)
  if (url.origin !== base.origin) continue
  const result = await request('GET', url.pathname.replace(base.pathname.replace(/\/$/, ''), ''))
  assets.push({ url: url.pathname, http: result.response.status, sha256: digest(result.bytes), bytes: result.bytes.length })
  if (url.pathname.endsWith('.js')) for (const chunk of result.text.matchAll(/ReleasesPage-[\w-]+\.js/g)) {
    const releasePath = new URL(chunk[0], url).pathname
    if (assets.some(a => a.url === releasePath)) continue
    const release = await request('GET', releasePath.replace(base.pathname.replace(/\/$/, ''), ''))
    assets.push({ url: releasePath, http: release.response.status, sha256: digest(release.bytes), bytes: release.bytes.length,
      randomUUIDCalls: [...release.text.matchAll(/crypto\.randomUUID\(/g)].length })
  }
}
await save('deployment-fingerprints.json', { base: base.href, head: (await readFile(join(root, '.git/HEAD'), 'utf8')).trim(), indexSha256: digest(index.bytes), assets })
const browser = await chromium.launch({ headless: true })
try {
  const context = await browser.newContext()
  const page = await context.newPage(), network = [], errors = []
  page.on('request', req => { if (new URL(req.url()).pathname.includes('/api/')) network.push({ method: req.method(), path: new URL(req.url()).pathname }) })
  page.on('pageerror', error => errors.push(error.message))
  await page.goto(new URL('releases', base).href, { waitUntil: 'networkidle', timeout: 25000 })
  const state = await page.evaluate(() => ({ isSecureContext, randomUUID: typeof globalThis.crypto?.randomUUID,
    getRandomValues: typeof globalThis.crypto?.getRandomValues, path: location.pathname,
    uuidInvocation: (() => { try { return { value: globalThis.crypto.randomUUID() } } catch (e) { return { error: e.name + ': ' + e.message } } })() }))
  await save('browser.json', { state, network, pageErrors: errors, loginRequired: await page.getByRole('heading', { name: '登录工作台', exact: true }).count(),
    scopeActionExecuted: false, reason: 'No confirmed administrator credentials; no freeze/create action attempted. Console API invocation only verifies browser capability.' })
  await page.screenshot({ path: join(output, 'browser-login.png') })
  await context.close()
} finally { await browser.close() }
await save('cleanup.json', { runId, createdObjects: 0, writesOnBusinessScopes: 0, loggedInSessions: 0,
  retainedHistory: [], physicalResourcesPending: [], result: 'No business test objects created; no cleanup writes necessary. Original authenticated state baseline unavailable.' })
await save('summary.json', { runId, routeCount: coverage.length, publicScenarioPassed: coverage.filter(r => r.status === 'passed').length,
  functionalBlocked: coverage.filter(r => r.status === 'blocked').length, anonymousBoundariesPassed: coverage.filter(r => r.authBoundary === 'passed').length,
  observations: observations.length, output })
console.log(JSON.stringify({ runId, routeCount: coverage.length, observations: observations.length, output }))
