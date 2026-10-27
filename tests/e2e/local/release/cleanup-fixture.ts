import { expect, type Page } from '@playwright/test'
import { mockApi, ok, fail, meta, config } from '../../auth/mock'
export { ok, fail } from '../../auth/mock'

export const base = () => process.env.RB_QA_A_ORIGIN
  ? process.env.RB_QA_A_ORIGIN + (process.env.RB_QA_A_PREFIX || '') + '/'
  : process.env.RAINBOW_AUTH_UI_URL || 'http://127.0.0.1:3002/rainbow-bridge/'
export const row = (page: Page, version = 'v2') => page.getByRole('row').filter({ has: page.getByRole('cell', { name: version, exact: true }) })
export async function fixture(page: Page) {
  const mock = await mockApi(page, true)
  mock.locked = true
  mock.overrides.set('runtime/overview', route => ok(route, { list: ['qa', 'other'].map(key => ({
    environment_key: key, environment_name: key === 'qa' ? '演示环境' : '另一环境',
    pipelines: [{ pipeline_key: 'web', pipeline_name: '网页渠道' }],
  })), total: 2 }))
  const active = { snapshot_id: 's1', version: 'v1', revision: '1', changed_at: '2026-10-20T00:00:00Z', changed_by: 'admin', changed_reason: '' }
  const state = { active: active as typeof active | null, items: [
    { meta: { ...meta, version: 'v2', snapshot_id: 's2', reason: '' }, is_active: false },
    { meta: { ...meta, version: 'v1', snapshot_id: 's1', reason: '' }, is_active: true },
  ], deleted: false }
  mock.overrides.set('release/list', route => ok(route, { items: state.items, active: state.active, has_more: false }))
  mock.overrides.set('release/active', route => ok(route, { active: state.active }))
  mock.overrides.set('release/detail', route => ok(route, { meta: { ...meta, reason: '' }, configs: [config], assets: [] }))
  mock.overrides.set('release/delete', async route => {
    const payload = route.request().postDataJSON()
    const replayed = state.deleted
    state.deleted = true
    state.items = state.items.filter(item => item.meta.snapshot_id !== payload.expected_snapshot_id)
    await ok(route, { request_id: payload.request_id, snapshot_id: payload.expected_snapshot_id, version: payload.version,
      deleted: true, deleted_at: '2026-10-20T12:00:00Z', deleted_by: 'admin', reason: payload.reason, replayed })
  })
  await page.goto(base() + 'releases?env=qa&pipeline=web')
  await expect(row(page).getByRole('button', { name: '删除版本', exact: true })).toBeVisible()
  return { mock, state }
}
export async function openDelete(page: Page) {
  await row(page).getByRole('button', { name: '删除版本', exact: true }).click()
  await expect(page.getByRole('dialog', { name: '删除版本 v2', exact: true })).toBeVisible()
}
export function deletionResult(payload: Record<string, unknown>) {
  return { request_id: payload.request_id, snapshot_id: payload.expected_snapshot_id, version: payload.version,
    deleted: true, deleted_at: '2026-10-20T12:00:00Z', deleted_by: 'admin', reason: payload.reason, replayed: false }
}
