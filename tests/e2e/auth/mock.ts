import type { Page, Route, Request } from '@playwright/test'
import JSZip from 'jszip'

export const CSRF = 'mock-csrf-not-a-real-secret'
export const identity = { user: { id: '1', username: 'admin', role: 'admin' },
  session: { expires_at: '2099-01-01T00:00:00Z', idle_expires_at: '2099-01-01T00:00:00Z' }, csrf_token: CSRF }
export const scopeQuery = '?env=qa&pipeline=web'
const environment = { environment_key: 'qa', environment_name: '演示环境', pipelines: [
  { pipeline_key: 'web', pipeline_name: '网页渠道', config_count: 1, configs: [
    { resource_key: 'r1', alias: 'title', name: '站点标题', type: 'text' },
  ] },
] }
export const config = { resource_key: 'r1', environment_key: 'qa', pipeline_key: 'web',
  alias: 'title', name: '站点标题', type: 'text', content: '光谱标题', edit_revision: '00000000-0000-4000-8000-000000000001' }
export const meta = { snapshot_id: 'snap1', version: 'v1', environment_key: 'qa', pipeline_key: 'web',
  source_freeze_revision: '2', config_count: 1, asset_count: 0, created_at: '2026-10-20T00:00:00Z' }
export function ok(route: Route, data: unknown) {
  return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ code: 0, msg: 'success', data }) })
}
export function fail(route: Route, status: number, kind: string, extraHeaders: Record<string, string> = {}) {
  return route.fulfill({ status, contentType: 'application/json', headers: extraHeaders,
    body: JSON.stringify({ code: status, error_type: kind, msg: 'fixture error' }) })
}
export async function mockApi(page: Page, authenticated = false) {
  const state = {
    authenticated, loginStatus: 200, meStatus: 200, logoutStatus: 200, locked: false,
    requests: [] as Request[], overrides: new Map<string, (route: Route) => Promise<void>>(),
    count(path: string) { return this.requests.filter(request => new URL(request.url()).pathname.endsWith('/api/v1/' + path)).length },
    writes(path: string) { return this.requests.filter(request => new URL(request.url()).pathname.endsWith('/api/v1/' + path) && request.method() === 'POST') },
  }
  const zip = await new JSZip().file('configs.json', JSON.stringify([config])).generateAsync({ type: 'nodebuffer' })
  await page.route('**/api/v1/**', async route => {
    const request = route.request()
    state.requests.push(request)
    const path = new URL(request.url()).pathname.split('/api/v1/')[1]
    if (state.overrides.has(path)) return state.overrides.get(path)!(route)
    if (path === 'auth/me') return state.meStatus !== 200 ? fail(route, state.meStatus, 'AUTH_UNAVAILABLE') :
      state.authenticated ? ok(route, identity) : fail(route, 401, 'AUTH_REQUIRED')
    if (path === 'auth/login') {
      if (state.loginStatus !== 200) return fail(route, state.loginStatus,
        state.loginStatus === 401 ? 'AUTH_INVALID_CREDENTIALS' : state.loginStatus === 429 ? 'AUTH_RATE_LIMITED' : 'AUTH_UNAVAILABLE', { 'Retry-After': '1' })
      state.authenticated = true
      return ok(route, identity)
    }
    if (path === 'auth/logout') {
      if (state.logoutStatus !== 200) return fail(route, state.logoutStatus, 'AUTH_UNAVAILABLE')
      state.authenticated = false
      return ok(route, { logged_out: true })
    }
    if (path === 'runtime/overview') return ok(route, { list: [environment], total: 1 })
    if (path === 'runtime/release') return ok(route, { ...meta, selection_mode: 'active', active_revision: '1',
      environment: { ...environment, pipeline_key: 'web', pipeline_name: '网页渠道' }, configs: [config] })
    if (path === 'version') return ok(route, { version_info: { version: 'dev' } })
    if (path === 'version/latest') return ok(route, {})
    if (!state.authenticated) return fail(route, 401, 'AUTH_REQUIRED')
    if (path === 'config/list') return ok(route, { list: [config], total: 1 })
    if (path === 'config/detail') return ok(route, { config })
    if (path === 'freeze/status' || path === 'freeze/lock' || path === 'freeze/unlock') {
      if (path === 'freeze/lock') state.locked = true
      if (path === 'freeze/unlock') state.locked = false
      return ok(route, { environment_key: 'qa', pipeline_key: 'web', state: state.locked ? 'locked' : 'unlocked', revision: '2', admin_configured: true })
    }
    if (path === 'asset/list') return ok(route, { list: [], total: 0 })
    if (path === 'asset/upload') return ok(route, { asset: { file_id: 'a1', file_name: 'fixture.txt', content_type: 'text/plain', file_size: 4 } })
    if (path === 'transfer/export-tree') return ok(route, { environments: [environment] })
    if (path === 'transfer/export') return route.fulfill({ status: 200, contentType: 'application/zip', body: zip })
    if (path === 'transfer/import-preview') return ok(route, { format: 'zip', environments: [{ ...environment, status: 'new',
      pipelines: environment.pipelines.map(pipeline => ({ ...pipeline, status: 'new', configs: pipeline.configs.map(item => ({ ...item, status: 'new' })) })) }],
      summary: { total_environments: 1, total_pipelines: 1, total_configs: 1, total_assets: 0, new_count: 1, existing_count: 0, conflict_count: 0 } })
    if (path === 'transfer/import-selective') return ok(route, { imported: 1, skipped: 0, failed: 0 })
    if (path === 'release/list') return ok(route, { items: [{ meta, is_active: false }], has_more: false, active: null })
    if (path === 'release/active') return ok(route, { active: null })
    if (path === 'release/create') return ok(route, { meta, replayed: false })
    if (path === 'release/activate') return ok(route, { active: { version: 'v1', revision: '1' }, operation: { replayed: false } })
    return ok(route, {})
  })
  return state
}
export async function login(page: Page) {
  await page.getByLabel('密码', { exact: true }).fill('mock-password-only')
  await page.getByRole('button', { name: '登录', exact: true }).click()
}
