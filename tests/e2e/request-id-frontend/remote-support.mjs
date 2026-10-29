import { readFile, stat, readdir } from 'node:fs/promises'
import { join, resolve, sep, extname } from 'node:path'
import { createHash } from 'node:crypto'

export function remoteClient(context, base) {
  let csrf = ''
  const endpoint = path => new URL('api/v1/' + path, base).href
  async function call(method, path, data) {
    let response
    try {
      response = await context.request.fetch(endpoint(path), { method, data, timeout: 20000,
        headers: method === 'GET' ? {} : { Origin: base.origin, 'X-CSRF-Token': csrf } })
    } catch { throw new Error(`${method} ${path.split('?')[0]} network failure; no headers retained`) }
    const body = await response.json()
    return { status: response.status(), body, data: body.data }
  }
  async function checked(method, path, data) {
    const result = await call(method, path, data)
    if (result.status !== 200 || ![0, 200].includes(result.body.code)) {
      throw new Error(`${method} ${path.split('?')[0]} rejected: ${result.status} ${result.body.error_type || ''}`)
    }
    return result.data
  }
  async function session() {
    const data = await checked('GET', 'auth/me')
    if (!data.csrf_token) throw new Error('No usable authenticated session')
    csrf = data.csrf_token
  }
  async function snapshot(excludeKey) {
    const environments = (await checked('GET', 'environment/list')).list
    const rows = []
    for (const env of environments) {
      if (env.environment_key === excludeKey) continue
      const pipelines = (await checked('GET', 'pipeline/list?' + new URLSearchParams({ environment_key: env.environment_key }))).list
      for (const scope of pipelines) {
        const query = new URLSearchParams({ environment_key: env.environment_key, pipeline_key: scope.pipeline_key, scope_id: scope.scope_id })
        const freeze = await checked('GET', 'freeze/status?' + query)
        const active = await checked('GET', 'release/active?' + query)
        rows.push({ environment_key: env.environment_key, environment_id: env.environment_id, pipeline_key: scope.pipeline_key, scope_id: scope.scope_id,
          freeze: { state: freeze.state, revision: freeze.revision }, active: active.active ? {
            snapshot_id: active.active.snapshot_id, version: active.active.version, revision: active.active.revision,
          } : null, active_revision: active.active_revision })
      }
    }
    return rows.sort((a, b) => a.scope_id.localeCompare(b.scope_id))
  }
  return { endpoint, call, checked, session, snapshot }
}

/** Replace only browser-loaded static files. Every API request goes to the real origin. */
export async function overlayStatic(page, dist, base) {
  const root = resolve(dist)
  const prefix = base.pathname.replace(/\/$/, '')
  const localHTML = await readFile(join(root, 'index.html'), 'utf8')
  const remoteHTML = await (await page.request.get(new URL('login', base).href)).text()
  const entries = html => [...html.matchAll(/<script\b[^>]*\btype="module"[^>]*\bsrc="([^"]+)"[^>]*>/g)].map(match => new URL(match[1], base).pathname)
  const localEntry = entries(localHTML)[0]
  const remoteEntries = new Set(entries(remoteHTML))
  if (!localEntry || !remoteEntries.size) throw new Error('Cannot identify local and remote module entry points')
  const files = await readdir(join(root, 'assets'))
  const fingerprint = []
  for (const name of files.filter(name => name.endsWith('.js'))) {
    fingerprint.push({ file: name, sha256: createHash('sha256').update(await readFile(join(root, 'assets', name))).digest('hex') })
  }
  // Keep the main HTML on its real HTTP transport. Fulfilled HTML can lose the
  // browser's network address-space provenance and block private-network fetches.
  // Only JavaScript/CSS/static assets are substituted; API is never intercepted.
  await page.route(url => url.origin === base.origin && url.pathname.startsWith(prefix + '/assets/'), async route => {
    const path = new URL(route.request().url()).pathname
    const selected = remoteEntries.has(path) ? localEntry : path
    const suffix = decodeURIComponent(selected.slice(prefix.length)).replace(/^\/+/, '')
    const file = resolve(root, suffix)
    if (!file.startsWith(root + sep)) throw new Error('Refusing a static path outside the build')
    const info = await stat(file).catch(() => null)
    if (!info?.isFile()) { await route.continue(); return } // Unused old modulepreloads can use the original server assets.
    const mime = { '.html': 'text/html', '.js': 'application/javascript', '.css': 'text/css', '.webp': 'image/webp', '.png': 'image/png', '.svg': 'image/svg+xml', '.woff2': 'font/woff2' }
    await route.fulfill({ status: 200, contentType: mime[extname(file)] || 'application/octet-stream', body: await readFile(file) })
  })
  return { files: fingerprint, local_entry: localEntry, replaced_remote_entries: [...remoteEntries], main_html_replaced: false }
}
