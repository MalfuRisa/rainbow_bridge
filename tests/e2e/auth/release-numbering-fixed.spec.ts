import { test, expect, type Page } from '@playwright/test'
import { mkdirSync } from 'node:fs'
import { join } from 'node:path'
import { mockApi, ok, fail, meta, fixtureIdentity, scopeQuery, CSRF } from './mock'

const scopeId = fixtureIdentity('qa', 'web')
const versions = [
  { ...meta, scope_id: scopeId, version: 'v10', snapshot_id: '00000000-0000-4000-8000-000000000010' },
  { ...meta, scope_id: scopeId, version: 'v9', snapshot_id: '00000000-0000-4000-8000-000000000009' },
  { ...meta, scope_id: scopeId, version: 'test-1', snapshot_id: '00000000-0000-4000-8000-000000000001' },
]
const panel = (page: Page) => page.getByRole('dialog', { name: '版本比较', exact: true })
async function fixture(page: Page) {
  const mock = await mockApi(page, true)
  const state = { active: { ...versions[1], revision: '5' }, secondPage: false, more: false }
  mock.overrides.set('release/list', route => ok(route, { items: (new URL(route.request().url()).searchParams.get('before_id') ? versions.slice(2) : versions.slice(0, 2)).map(m => ({ meta: m, is_active: m.snapshot_id === state.active.snapshot_id })),
    active: state.active, has_more: !state.secondPage, next_before_id: state.secondPage ? '' : '2' }))
  mock.overrides.set('release/active', route => ok(route, { active: state.active, active_revision: state.active.revision }))
  mock.overrides.set('release/compare', route => {
    const p = route.request().postDataJSON()
    const fixed = p.comparison_mode === 'fixed'
    const baseline = fixed ? versions.find(v => v.version === p.baseline_version)! : state.active
    const target = versions.find(v => v.version === p.version)!
    const self = baseline.snapshot_id === target.snapshot_id
    const reversed = baseline.version === 'v10'
    const side = { alias: 'title', name: '标题', type: 'text' }
    return ok(route, { comparison_mode: p.comparison_mode, scope_id: scopeId, baseline, target,
      active_revision: fixed ? null : state.active.revision, target_is_active: fixed ? null : target.snapshot_id === state.active.snapshot_id,
      has_changes: !self, summary: { added: self ? 0 : reversed ? 0 : 1, removed: self ? 0 : reversed ? 1 : 0, modified: 0, unchanged: self ? 1 : 0 },
      items: self ? [] : [{ resource_key: 'title', change_type: reversed ? 'removed' : 'added', changed_fields: [], before: reversed ? side : null, after: reversed ? null : side }],
      total_changes: self ? 0 : 1, offset: p.offset, limit: p.limit, has_more: state.more, next_offset: state.more ? p.offset + 20 : null })
  })
  mock.overrides.set('release/detail', route => {
    const v = versions.find(v => v.version === new URL(route.request().url()).searchParams.get('version'))!
    return ok(route, { meta: v, configs: v.version === 'v10' ? [{ resource_key: 'title', alias: 'title', name: '标题', type: 'number', content: '900719925474099312345' }] : [], assets: [] })
  })
  return { mock, state }
}
async function open(page: Page) {
  await page.goto('releases' + scopeQuery)
  await page.getByRole('button', { name: '版本比较', exact: true }).click()
  await expect(panel(page).getByLabel('差异统计')).toBeVisible()
}
async function select(page: Page, label: string, value: string) {
  await panel(page).getByRole('combobox', { name: label, exact: true }).click()
  await page.getByRole('option', { name: value, exact: true }).click()
}

test('fixed方向、交换、自比、旧名字和后端顺序；从历史比较发布必须重走active', async ({ page }) => {
  const { mock, state } = await fixture(page)
  await open(page)
  const first = mock.writes('release/compare')[0].postDataJSON()
  expect(first).toMatchObject({ comparison_mode: 'fixed', baseline_version: 'v9', version: 'v10' })
  expect(first).not.toHaveProperty('expected_active_revision')
  expect(first).not.toHaveProperty('expected_active_snapshot_id')
  await panel(page).getByRole('button', { name: '交换比较方向', exact: true }).click()
  await expect.poll(() => mock.writes('release/compare').length).toBe(2)
  expect(mock.writes('release/compare')[1].postDataJSON()).toMatchObject({ baseline_version: 'v10', version: 'v9', expected_snapshot_id: versions[1].snapshot_id })
  await expect(panel(page).locator('.review-summary')).toContainText('删除')
  await select(page, '目标版本', 'v10')
  await expect(panel(page).getByText('无差异', { exact: true })).toBeVisible()
  state.secondPage = true
  await panel(page).getByRole('button', { name: '加载更多版本', exact: true }).click()
  await panel(page).getByRole('combobox', { name: '目标版本', exact: true }).click()
  expect(await page.getByRole('option').allTextContents()).toEqual(['v10', 'v9', 'test-1'])
  await page.getByRole('option', { name: 'test-1', exact: true }).click()
  await expect(panel(page).getByLabel('差异统计')).toBeVisible()
  expect(mock.count('release/activate')).toBe(0)
  await panel(page).getByRole('button', { name: '审阅目标版本发布', exact: true }).click()
  const review = page.getByRole('dialog', { name: '发布审阅', exact: true })
  await expect(review.getByLabel('差异统计')).toBeVisible()
  const activeCompare = mock.writes('release/compare').at(-1)!.postDataJSON()
  expect(activeCompare).toMatchObject({ comparison_mode: 'active', version: 'test-1', expected_active_revision: '5' })
  expect(activeCompare).not.toHaveProperty('baseline_version')
  expect(activeCompare).not.toHaveProperty('expected_baseline_snapshot_id')
  await expect(review.getByRole('button', { name: '确认生效 test-1', exact: true })).toBeEnabled()
  expect(mock.count('release/activate')).toBe(0)
})

test('fixed 翻页不受 active 切换影响，固定两侧完整身份并保留大整数原文', async ({ page }) => {
  const { mock, state } = await fixture(page)
  state.more = true
  await open(page)
  const before = mock.writes('release/compare')[0].postDataJSON()
  state.active = { ...versions[0], revision: '7' }
  await panel(page).getByRole('button', { name: '加载更多差异', exact: true }).click()
  await expect.poll(() => mock.count('release/compare')).toBe(2)
  const next = mock.writes('release/compare')[1].postDataJSON()
  expect(next).toEqual({ ...before, offset: 20 })
  expect(mock.count('release/active')).toBe(1) // only the version page, not fixed comparison
  await panel(page).locator('.comparison-row').first().click()
  await expect(panel(page).locator('pre').first()).toHaveText('900719925474099312345')
  expect(mock.writes('release/compare')[0].headers()['x-csrf-token']).toBe(CSRF)
})

test('交换或关闭后迟到的fixed响应不能覆盖新方向', async ({ page }) => {
  const { mock } = await fixture(page)
  await open(page)
  const original = mock.overrides.get('release/compare')!
  let release: (() => void) | undefined
  mock.overrides.set('release/compare', async route => {
    if (route.request().postDataJSON().baseline_version === 'v10') await new Promise<void>(resolve => { release = resolve })
    await original(route)
  })
  await panel(page).getByRole('button', { name: '交换比较方向', exact: true }).click()
  await expect.poll(() => Boolean(release)).toBe(true)
  await select(page, '基准版本', 'v9')
  await expect(panel(page).getByText('无差异', { exact: true })).toBeVisible()
  release!()
  await expect(panel(page).getByText('无差异', { exact: true })).toBeVisible()
  expect(await panel(page).locator('.fixed-direction').textContent()).toContain('v9')
  await panel(page).getByRole('button', { name: '关闭', exact: true }).click()
  await expect(panel(page)).toBeHidden()
})

for (const side of ['baseline', 'target']) test(`fixed ${side} 异常指明哪一侧，不留下可用差异`, async ({ page }) => {
  const { mock } = await fixture(page)
  mock.overrides.set('release/compare', route => route.fulfill({ status: 410, contentType: 'application/json',
    body: JSON.stringify({ code: 410, error_type: 'RELEASE_VERSION_DELETED', side }) }))
  await page.goto('releases' + scopeQuery)
  await page.getByRole('button', { name: '版本比较', exact: true }).click()
  await expect(panel(page).getByRole('alert')).toContainText(side === 'baseline' ? '基准版本：' : '目标版本：')
  await expect(panel(page).getByLabel('差异统计')).toHaveCount(0)
  await expect(panel(page).getByRole('button', { name: '审阅目标版本发布', exact: true })).toBeDisabled()
})

test('fixed null 字段必须保留，损坏模式响应不可用于发布', async ({ page }) => {
  const { mock } = await fixture(page)
  mock.overrides.set('release/compare', route => {
    const p = route.request().postDataJSON()
    return ok(route, { comparison_mode: 'fixed', scope_id: scopeId, baseline: versions[1], target: versions[0],
      active_revision: '0', target_is_active: false, has_changes: false, summary: { added: 0, removed: 0, modified: 0, unchanged: 1 },
      items: [], total_changes: 0, offset: p.offset, limit: 20, has_more: false, next_offset: null })
  })
  await page.goto('releases' + scopeQuery)
  await page.getByRole('button', { name: '版本比较', exact: true }).click()
  await expect(panel(page).getByRole('alert')).toContainText('比较结果不完整')
  await expect(panel(page).getByRole('button', { name: '审阅目标版本发布', exact: true })).toBeDisabled()
})

test('旧服务缺少独立revision时阻断发布，不将null解释为零', async ({ page }) => {
  const { mock } = await fixture(page)
  mock.overrides.set('release/active', route => ok(route, { active: null }))
  mock.overrides.set('release/compare', route => route.fulfill({ status: 409, contentType: 'application/json',
    body: JSON.stringify({ code: 409, error_type: 'RELEASE_ACTIVE_REVISION_CONFLICT', latest_active: null }) }))
  await page.goto('releases' + scopeQuery)
  await page.getByRole('row').filter({ has: page.getByRole('cell', { name: 'v10', exact: true }) }).getByRole('button', { name: '查看差异', exact: true }).click()
  const review = page.getByRole('dialog', { name: '发布审阅', exact: true })
  await expect(review.getByRole('alert')).toContainText('生效状态响应不完整或不一致')
  await expect(review.getByRole('button', { name: '确认生效 v10', exact: true })).toBeDisabled()
  expect(mock.count('release/compare')).toBe(0)
  expect(mock.count('release/activate')).toBe(0)
})

test('fixed比较期间同名scope重建后旧结果不能写回新范围', async ({ page }) => {
  const { mock } = await fixture(page)
  await open(page)
  const original = mock.overrides.get('release/compare')!
  let finish: (() => void) | undefined
  mock.overrides.set('release/compare', async route => {
    await new Promise<void>(resolve => { finish = resolve })
    await original(route)
  })
  await panel(page).getByRole('button', { name: '交换比较方向', exact: true }).click()
  await expect.poll(() => Boolean(finish)).toBe(true)
  const newScope = '00000000-0000-4000-8000-000000000099'
  mock.overrides.set('runtime/overview', route => ok(route, { list: [{ environment_key: 'qa', environment_name: '演示环境', pipelines: [
    { pipeline_key: 'web', pipeline_name: '网页渠道', scope_id: newScope, configs: [], config_count: 0 },
  ] }], total: 1 }))
  mock.overrides.set('release/list', route => ok(route, { items: [], active: null, has_more: false }))
  mock.overrides.set('release/active', route => ok(route, { active: null, active_revision: '0' }))
  await panel(page).getByRole('button', { name: '关闭', exact: true }).click()
  await page.getByRole('link', { name: '资源管理', exact: true }).click()
  await page.getByRole('link', { name: '版本发布', exact: true }).click()
  finish!()
  await expect(page.getByRole('button', { name: '版本比较', exact: true })).toBeDisabled()
  await expect(panel(page)).toHaveCount(0)
  expect(mock.count('release/activate')).toBe(0)
})

test('独立创建不传version，返回vN用于后续操作；auto重试及旧手填pending保持完整原请求', async ({ page }) => {
  const { mock } = await fixture(page)
  mock.locked = true
  mock.overrides.set('release/create', route => route.abort('failed'))
  await page.goto('releases' + scopeQuery)
  await page.getByRole('button', { name: '创建版本', exact: true }).click()
  await expect(page.locator('#release-version')).toHaveCount(0)
  await page.locator('#release-reason').fill('新候选')
  await page.getByRole('dialog').getByRole('button', { name: '保存版本', exact: true }).click()
  await expect(page.getByRole('button', { name: '确认结果', exact: true })).toBeVisible()
  const p = mock.writes('release/create')[0].postDataJSON()
  const storageKey = await page.evaluate(() => Object.keys(sessionStorage).find(key => key.startsWith('rainbow-release-review:'))!)
  expect(p).not.toHaveProperty('version')
  await page.reload()
  await page.getByRole('button', { name: '创建版本', exact: true }).click()
  mock.overrides.set('release/create', route => ok(route, { meta: versions[0], replayed: true }))
  await page.getByRole('button', { name: '确认结果', exact: true }).click()
  await expect(page.getByRole('dialog')).toBeHidden()
  expect(mock.writes('release/create')[1].postDataJSON()).toEqual(p)
  const old = { ...p, version: 'test-1', request_id: '00000000-0000-4000-8000-000000000099', reason: '历史原请求' }
  await page.evaluate(({ storageKey, old }) => sessionStorage.setItem(storageKey, JSON.stringify({ kind: 'create', payload: old })), { storageKey, old })
  await page.getByRole('button', { name: '创建版本', exact: true }).click()
  mock.overrides.set('release/create', route => ok(route, { meta: versions[2], replayed: true }))
  await page.getByRole('button', { name: '确认结果', exact: true }).click()
  await expect(page.getByRole('dialog')).toBeHidden()
  expect(mock.writes('release/create')[2].postDataJSON()).toEqual(old)
  // The same old persisted intent is supported by the concentrated entry too.
  await page.evaluate(({ storageKey, old }) => sessionStorage.setItem(storageKey, JSON.stringify({ kind: 'create', payload: old })), { storageKey, old })
  await page.getByRole('button', { name: '发布配置', exact: true }).click()
  await page.getByRole('button', { name: '确认原请求结果', exact: true }).click()
  await expect(page.getByRole('dialog').getByLabel('差异统计')).toBeVisible()
  expect(mock.writes('release/create')[3].postDataJSON()).toEqual(old)
  expect(mock.count('release/activate')).toBe(0)
})

for (const [code, kind, text] of [[400, 'RELEASE_VERSION_AUTO_ASSIGNED', '版本号由系统生成'], [409, 'RELEASE_VERSION_SEQUENCE_EXHAUSTED', '版本编号已用尽']] as const) {
  test(`编号错误明确反馈不盲目确认：${kind}`, async ({ page }) => {
    const { mock } = await fixture(page)
    mock.locked = true
    mock.overrides.set('release/create', route => fail(route, code, kind))
    await page.goto('releases' + scopeQuery)
    await page.getByRole('button', { name: '发布配置', exact: true }).click()
    await page.getByRole('dialog').getByRole('button', { name: '创建候选版本', exact: true }).click()
    await expect(page.getByRole('dialog').getByRole('alert')).toContainText(text)
    await expect(page.getByRole('button', { name: '确认原请求结果', exact: true })).toHaveCount(0)
    expect(mock.count('release/create')).toBe(1)
  })
}

test('fixed比较在1280/1440明暗主题下可操作', async ({ page }) => {
  await fixture(page)
  await open(page)
  await panel(page).locator('.comparison-row').click()
  const folder = join(process.cwd(), '../../output/release-review-ui/visuals')
  mkdirSync(folder, { recursive: true })
  for (const width of [1280, 1440]) for (const dark of [false, true]) {
    await page.setViewportSize({ width, height: 900 })
    await page.evaluate(value => document.documentElement.classList.toggle('dark', value), dark)
    await expect(panel(page).getByRole('heading', { name: '版本比较', exact: true })).toBeInViewport()
    await expect(panel(page).getByRole('combobox', { name: '目标版本', exact: true })).toBeInViewport()
    await expect(panel(page).getByRole('button', { name: '审阅目标版本发布', exact: true })).toBeInViewport()
    await panel(page).screenshot({ path: join(folder, `fixed-${width}-${dark ? 'dark' : 'light'}.png`) })
  }
})
