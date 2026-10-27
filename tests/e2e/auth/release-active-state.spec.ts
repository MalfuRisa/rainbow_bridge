import { test, expect, type Page } from '@playwright/test'
import { mockApi, ok, fail, fixtureIdentity, meta, scopeQuery } from './mock'

const scopeId = fixtureIdentity('qa', 'web')
const snapshot = '00000000-0000-4000-8000-000000000001'
const candidate = { ...meta, snapshot_id: snapshot, scope_id: scopeId }
const active = (revision: string) => ({ scope_id: scopeId, snapshot_id: fixtureIdentity('baseline'), version: 'old-1', revision })
async function setup(page: Page, data: unknown) {
  const mock = await mockApi(page, true)
  let state = data as { active: ReturnType<typeof active> | null; active_revision: string }
  mock.overrides.set('release/active', route => ok(route, state))
  mock.overrides.set('release/list', route => ok(route, { items: [{ meta: candidate, is_active: false }], active: state.active ?? null, has_more: false }))
  mock.overrides.set('release/compare', route => {
    const p = route.request().postDataJSON()
    return ok(route, { comparison_mode: 'active', scope_id: scopeId, baseline: state.active, target: candidate,
      active_revision: state.active_revision, target_is_active: false, has_changes: true,
      summary: { added: 1, modified: 0, removed: 0, unchanged: 0 },
      items: [{ resource_key: 'title', change_type: 'added', changed_fields: [], before: null, after: { alias: 'title', name: '标题', type: 'text' } }],
      total_changes: 1, offset: p.offset, limit: p.limit, has_more: false, next_offset: null })
  })
  mock.overrides.set('release/activate', route => {
    const p = route.request().postDataJSON()
    state = { active_revision: (BigInt(p.expected_active_revision) + 1n).toString(),
      active: { ...active(''), version: candidate.version, snapshot_id: candidate.snapshot_id } }
    state.active!.revision = state.active_revision
    return ok(route, { version: p.version, operation: { request_id: p.request_id, replayed: false, applied_revision: state.active_revision }, active: state.active })
  })
  return mock
}
const panel = (page: Page) => page.getByRole('dialog', { name: '发布审阅', exact: true })
async function open(page: Page) {
  await page.goto('releases' + scopeQuery)
  await page.getByRole('button', { name: '查看差异', exact: true }).click()
}

for (const revision of ['0', '7', '9007199254740993']) test(`无active使用独立revision ${revision}，仅明确确认后切换`, async ({ page }) => {
  const mock = await setup(page, { active: null, active_revision: revision })
  await open(page)
  await expect(panel(page).getByLabel('差异统计')).toBeVisible()
  expect(mock.writes('release/compare')[0].postDataJSON().expected_active_revision).toBe(revision)
  expect(mock.count('release/activate')).toBe(0)
  await panel(page).getByRole('button', { name: '确认生效 v1', exact: true }).click()
  await expect(panel(page).getByRole('heading', { name: '版本 v1 已生效', exact: true })).toBeVisible()
  expect(mock.writes('release/activate')[0].postDataJSON().expected_active_revision).toBe(revision)
})

test('有active的外层/内层revision一致且大整数不改写', async ({ page }) => {
  const revision = '9007199254740993'
  const mock = await setup(page, { active: active(revision), active_revision: revision })
  await open(page)
  await expect(panel(page).getByLabel('差异统计')).toBeVisible()
  expect(mock.writes('release/compare')[0].postDataJSON()).toMatchObject({ expected_active_revision: revision, expected_active_snapshot_id: active(revision).snapshot_id })
})

const invalidStates = [
  { name: '缺revision', value: { active: null } },
  { name: 'null revision', value: { active: null, active_revision: null } },
  { name: '数字revision', value: { active: null, active_revision: 7 } },
  { name: '负revision', value: { active: null, active_revision: '-1' } },
  { name: '非法revision', value: { active: null, active_revision: '7x' } },
  { name: '空白revision', value: { active: null, active_revision: ' 7' } },
  { name: '内外不一致', value: { active: active('8'), active_revision: '7' } },
  { name: '缺active', value: { active_revision: '7' } },
  { name: '错scope', value: { active: { ...active('7'), scope_id: fixtureIdentity('other-scope') }, active_revision: '7' } },
  { name: '缺snapshot', value: { active: { ...active('7'), snapshot_id: '' }, active_revision: '7' } },
]
for (const sample of invalidStates) test(`无效状态停止审阅：${sample.name}`, async ({ page }) => {
  const mock = await setup(page, sample.value)
  await open(page)
  await expect(panel(page).getByRole('alert')).toContainText('生效状态响应不完整或不一致')
  await expect(panel(page).getByRole('button', { name: '确认生效 v1', exact: true })).toBeDisabled()
  expect(mock.count('release/compare')).toBe(0)
  expect(mock.count('release/activate')).toBe(0)
})

test('准备阶段状态失效保留备注，修复响应后只刷新不自动创建', async ({ page }) => {
  const mock = await setup(page, { active: null, active_revision: '0' })
  await page.goto('releases' + scopeQuery)
  await page.getByRole('button', { name: '发布配置', exact: true }).click()
  await expect(panel(page).getByRole('button', { name: '锁版并创建候选版本', exact: true })).toBeEnabled()
  await panel(page).getByLabel('备注（选填）', { exact: true }).fill('保留这次备注')
  mock.overrides.set('release/active', route => ok(route, { active: null }))
  await panel(page).getByRole('button', { name: '刷新状态', exact: true }).click()
  await expect(panel(page).getByRole('alert')).toContainText('生效状态响应不完整或不一致')
  await expect(panel(page).getByLabel('备注（选填）', { exact: true })).toHaveValue('保留这次备注')
  await expect(panel(page).getByRole('button', { name: '锁版并创建候选版本', exact: true })).toBeDisabled()
  mock.overrides.set('release/active', route => ok(route, { active: null, active_revision: '7' }))
  await panel(page).getByRole('button', { name: '刷新状态', exact: true }).click()
  await expect(panel(page).getByRole('button', { name: '锁版并创建候选版本', exact: true })).toBeEnabled()
  expect(mock.count('release/create')).toBe(0)
})

test('API_NOT_FOUND不清会话、不改接口重试或伪装业务404', async ({ page }) => {
  const mock = await setup(page, { active: null, active_revision: '7' })
  mock.overrides.set('release/active', route => fail(route, 404, 'API_NOT_FOUND'))
  await open(page)
  await expect(panel(page).getByRole('alert')).toContainText('接口地址不存在或服务版本不匹配')
  await expect(panel(page).getByRole('button', { name: '确认生效 v1', exact: true })).toBeDisabled()
  await expect(page.getByRole('heading', { name: '登录工作台', exact: true })).toHaveCount(0)
  expect(mock.count('auth/logout')).toBe(0)
  expect(mock.count('release/activate')).toBe(0)
  await panel(page).getByRole('button', { name: '暂不发布', exact: true }).click()
  await expect(page.getByRole('button', { name: '发布配置', exact: true })).toBeEnabled()
})

test('API_NOT_FOUND导出错误不触发ZIP下载，会话仍可操作', async ({ page }) => {
  const mock = await mockApi(page, true)
  let downloads = 0
  page.on('download', () => downloads++)
  mock.overrides.set('transfer/export', route => fail(route, 404, 'API_NOT_FOUND'))
  await page.goto('import-export')
  await page.getByRole('checkbox', { name: '选择环境 演示环境', exact: true }).check()
  await page.getByRole('button', { name: '导出数据', exact: true }).click()
  await expect(page.getByText('接口地址不存在或服务版本不匹配，请确认服务地址与版本。', { exact: true })).toBeVisible()
  expect(downloads).toBe(0)
  await expect(page.getByRole('heading', { name: '登录工作台', exact: true })).toHaveCount(0)
  expect(mock.count('transfer/export')).toBe(1)
})

test('登录接口不存在也保留地址/版本提示，不改写成密码错误', async ({ page }) => {
  const mock = await mockApi(page, false)
  mock.overrides.set('auth/login', route => fail(route, 404, 'API_NOT_FOUND'))
  await page.goto('login')
  await page.getByLabel('账号', { exact: true }).fill('admin')
  await page.getByLabel('密码', { exact: true }).fill('mock-only')
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await expect(page.getByRole('alert')).toContainText('接口地址不存在或服务版本不匹配')
  await expect(page.getByRole('alert')).not.toContainText('密码错误')
  expect(mock.count('auth/login')).toBe(1)
})
