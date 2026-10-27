import { test, expect, type Page } from '@playwright/test'
import { mkdirSync } from 'node:fs'
import { join } from 'node:path'
import { mockApi, ok, fail, fixtureIdentity, meta, config, scopeQuery, CSRF } from './mock'

const scopeId = fixtureIdentity('qa', 'web')
const candidate = { ...meta, scope_id: scopeId, version: 'v2', snapshot_id: '00000000-0000-4000-8000-000000000002' }
const initialActive = { scope_id: scopeId, version: 'v1', snapshot_id: '00000000-0000-4000-8000-000000000001', revision: '9007199254740993' }
const changes = [
  { resource_key: 'color', alias: 'brand', name: '品牌色', type: 'color', content: '#ff7f00' },
  { resource_key: 'image', alias: 'cover', name: '封面', type: 'image', content: '/fixture.svg' },
  { resource_key: 'file', alias: 'file', name: '文件', type: 'file', content: '/fixture.txt' },
  { resource_key: 'rich', alias: 'intro', name: '简介', type: 'richtext', content: '<h1>安全预览</h1><script>parent.BAD=true</script>' },
]
async function setup(page: Page) {
  const mock = await mockApi(page, true)
  const state = { active: { ...initialActive } as typeof initialActive | null, same: false, total: 4, empty: false }
  mock.overrides.set('release/list', route => ok(route, { items: [{ meta: candidate, is_active: false }], active: state.active, has_more: false }))
  mock.overrides.set('release/active', route => ok(route, { active: state.active, active_revision: state.active?.revision ?? '0' }))
  mock.overrides.set('release/create', route => ok(route, { meta: candidate, replayed: false }))
  mock.overrides.set('release/compare', route => {
    const body = route.request().postDataJSON()
    const active = state.active?.snapshot_id === candidate.snapshot_id
    return ok(route, { comparison_mode: 'active', scope_id: scopeId, target: { version: 'v2', snapshot_id: candidate.snapshot_id },
      baseline: state.active ? { version: state.active.version, snapshot_id: state.active.snapshot_id } : null,
      active_revision: state.active?.revision ?? '0', target_is_active: active, has_changes: !state.same && !active,
      summary: { added: state.same || active ? 0 : 1, modified: state.same || active ? 0 : 2, removed: state.same || active ? 0 : 1, unchanged: state.empty ? 0 : 3 },
      items: state.same || active ? [] : changes.map((cfg, i) => ({ resource_key: cfg.resource_key, change_type: i === 0 ? 'added' : i === 3 ? 'removed' : 'modified',
        changed_fields: ['content'], before: i === 0 ? null : cfg, after: i === 3 ? null : cfg })),
      total_changes: state.same || active ? 0 : state.total, offset: body.offset, limit: body.limit, has_more: false, next_offset: null })
  })
  mock.overrides.set('release/detail', route => {
    const version = new URL(route.request().url()).searchParams.get('version')
    return ok(route, { meta: version === 'v2' ? candidate : { ...meta, scope_id: scopeId, ...state.active },
      configs: changes.filter(cfg => version === 'v2' ? cfg.resource_key !== 'rich' : cfg.resource_key !== 'color'), assets: [], active: state.active })
  })
  mock.overrides.set('release/activate', route => {
    const body = route.request().postDataJSON()
    state.active = { ...initialActive, ...candidate, revision: '9007199254740994' }
    return ok(route, { operation: { request_id: body.request_id, replayed: false, applied_revision: state.active.revision }, active: state.active, version: body.version })
  })
  await page.route('**/fixture.svg', route => route.fulfill({ contentType: 'image/svg+xml', body: '<svg xmlns="http://www.w3.org/2000/svg" width="80" height="50"><rect width="80" height="50" fill="orange"/></svg>' }))
  return { mock, state }
}
const dialog = (page: Page) => page.getByRole('dialog', { name: '发布审阅', exact: true })
async function review(page: Page) {
  await page.goto('releases' + scopeQuery)
  await page.getByRole('button', { name: '查看差异', exact: true }).click()
  await expect(dialog(page).getByLabel('差异统计')).toBeVisible()
}

test('集中发布：未冻结时锁版并创建候选，审阅前不切换；revision 保留字符串', async ({ page }) => {
  const { mock } = await setup(page)
  await page.goto('releases' + scopeQuery)
  await page.getByRole('button', { name: '发布配置', exact: true }).click()
  await expect(dialog(page).getByLabel('版本号', { exact: true })).toHaveCount(0)
  await dialog(page).getByLabel('备注（选填）', { exact: true }).fill('候选验收')
  await dialog(page).getByRole('button', { name: '锁版并创建候选版本', exact: true }).click()
  await expect(dialog(page).getByLabel('差异统计')).toBeVisible()
  expect(mock.count('freeze/lock')).toBe(1)
  expect(mock.count('release/create')).toBe(1)
  expect(mock.writes('release/create')[0].postDataJSON()).not.toHaveProperty('version')
  expect(mock.count('release/activate')).toBe(0)
  const compared = mock.writes('release/compare')[0].postDataJSON()
  expect(compared.expected_active_revision).toBe('9007199254740993')
  expect(compared.expected_snapshot_id).toBe(candidate.snapshot_id)
  await dialog(page).getByRole('button', { name: '确认生效 v2', exact: true }).click()
  await expect(dialog(page).getByRole('heading', { name: '版本 v2 已生效', exact: true })).toBeVisible()
  const create = mock.writes('release/create')[0].postDataJSON(), activate = mock.writes('release/activate')[0].postDataJSON()
  expect(activate.expected_active_revision).toBe(compared.expected_active_revision)
  expect(activate.request_id).not.toBe(create.request_id)
  for (const endpoint of ['freeze/lock', 'release/create', 'release/compare', 'release/activate']) {
    expect(mock.writes(endpoint)[0].headers()['x-csrf-token']).toBe(CSRF)
    expect(mock.writes(endpoint)[0].postDataJSON().expected_scope_id).toBe(scopeId)
  }
})

test('差异内容复用四种预览与原文；1440/1280 明暗主题审阅可见', async ({ page }) => {
  await setup(page)
  await review(page)
  const panel = dialog(page)
  for (const name of ['品牌色', '封面', '文件', '简介']) await panel.getByRole('button', { name: new RegExp(name) }).click()
  await expect(panel.getByRole('img', { name: '颜色预览 #ff7f00' })).toHaveCount(1)
  await expect(panel.locator('img[alt="封面"]').first()).toBeVisible()
  await expect(panel.getByRole('link', { name: '打开 文件' }).first()).toBeVisible()
  await expect(panel.locator('iframe[title="简介 富文本预览"]').first()).toHaveAttribute('sandbox', '')
  await panel.getByRole('button', { name: '查看 品牌色 原文', exact: true }).first().click()
  await expect(panel.locator('pre').filter({ hasText: '#ff7f00' })).toBeVisible()
  await expect(panel.getByRole('button', { name: '复制候选版本 brand 原文', exact: true })).toBeVisible()
  await page.context().grantPermissions(['clipboard-read', 'clipboard-write'])
  await panel.getByRole('button', { name: '复制候选版本 brand 原文', exact: true }).click()
  await expect.poll(() => page.evaluate(() => navigator.clipboard.readText())).toBe('#ff7f00')
  await panel.getByRole('button', { name: '查看 品牌色 预览', exact: true }).click()
  const visual = join(process.cwd(), '../../output/release-review-ui/visuals')
  mkdirSync(visual, { recursive: true })
  for (const width of [1440, 1280]) for (const dark of [false, true]) {
    await page.setViewportSize({ width, height: 900 })
    await page.evaluate(value => document.documentElement.classList.toggle('dark', value), dark)
    await panel.locator('.review-body').evaluate(element => { element.scrollTop = 0 })
    await panel.screenshot({ path: join(visual, `review-${width}-${dark ? 'dark' : 'light'}.png`) })
    const bounds = await panel.boundingBox()
    expect(bounds!.x).toBeGreaterThanOrEqual(0)
    expect(bounds!.width).toBeLessThanOrEqual(width - 32)
    expect(bounds!.width).toBeGreaterThanOrEqual(1000)
    await expect(panel.getByRole('heading', { name: '发布审阅', exact: true })).toBeInViewport()
    await expect(panel.getByRole('button', { name: '确认生效 v2', exact: true })).toBeInViewport()
    await expect(panel.getByRole('button', { name: '确认生效 v2', exact: true })).toBeVisible()
  }
})

test('CAS 冲突清空差异，重新比较后必须再次确认；A→B→A 仍保留新 revision', async ({ page }) => {
  const { mock, state } = await setup(page)
  await review(page)
  mock.overrides.set('release/activate', route => {
    state.active!.revision = '9007199254740995'
    return fail(route, 409, 'RELEASE_ACTIVE_REVISION_CONFLICT')
  })
  await dialog(page).getByRole('button', { name: '确认生效 v2', exact: true }).click()
  await expect(dialog(page).getByLabel('差异统计')).toBeHidden()
  await expect(dialog(page).getByRole('button', { name: '确认生效 v2', exact: true })).toBeDisabled()
  await dialog(page).getByRole('button', { name: '重新比较', exact: true }).click()
  await expect(dialog(page).getByLabel('差异统计')).toBeVisible()
  expect(mock.count('release/activate')).toBe(1)
  expect(mock.writes('release/compare')[1].postDataJSON().expected_active_revision).toBe('9007199254740995')
  await dialog(page).getByRole('button', { name: '确认生效 v2', exact: true }).click()
  expect(mock.writes('release/activate')[1].postDataJSON().request_id).not.toBe(mock.writes('release/activate')[0].postDataJSON().request_id)
})

test('创建响应丢失可刷新后确认原请求，不重锁或自动生效', async ({ page }) => {
  const { mock } = await setup(page)
  mock.overrides.set('release/create', route => route.abort('failed'))
  await page.goto('releases' + scopeQuery)
  await page.getByRole('button', { name: '发布配置', exact: true }).click()
  await expect(dialog(page).getByText('版本号由系统生成。', { exact: true })).toBeVisible()
  await dialog(page).getByRole('button', { name: '锁版并创建候选版本', exact: true }).click()
  await expect(dialog(page).getByText('创建结果尚未确认', { exact: true })).toBeVisible()
  const first = mock.writes('release/create')[0].postDataJSON()
  await page.reload()
  await page.getByRole('button', { name: '发布配置', exact: true }).click()
  await expect(dialog(page).getByText('创建结果尚未确认', { exact: true })).toBeVisible()
  expect(mock.count('release/create')).toBe(1)
  mock.overrides.set('release/create', route => ok(route, { meta: candidate, replayed: true }))
  await dialog(page).getByRole('button', { name: '确认原请求结果', exact: true }).click()
  await expect(dialog(page).getByLabel('差异统计')).toBeVisible()
  expect(mock.writes('release/create')[1].postDataJSON()).toEqual(first)
  expect(mock.count('freeze/lock')).toBe(1)
  expect(mock.count('release/activate')).toBe(0)
  await dialog(page).getByRole('button', { name: '暂不发布', exact: true }).click()
  expect(mock.count('release/delete')).toBe(0)
  expect(mock.count('freeze/unlock')).toBe(0)
})

test('重放切换成功与当前生效版本分别展示', async ({ page }) => {
  const { mock, state } = await setup(page)
  await review(page)
  mock.overrides.set('release/activate', route => route.abort('failed'))
  await dialog(page).getByRole('button', { name: '确认生效 v2', exact: true }).click()
  await expect(dialog(page).getByText('切换结果尚未确认', { exact: true })).toBeVisible()
  const first = mock.writes('release/activate')[0].postDataJSON()
  state.active = { ...initialActive, version: 'v3', revision: '9007199254740995' }
  mock.overrides.set('release/activate', route => ok(route, { operation: { request_id: first.request_id, replayed: true, applied_revision: '9007199254740994' }, active: state.active, version: 'v2' }))
  await dialog(page).getByRole('button', { name: '确认原请求结果', exact: true }).click()
  await expect(dialog(page).getByRole('heading', { name: '切换操作已确认', exact: true })).toBeVisible()
  await expect(dialog(page).getByText('当前生效版本为 v3，请以最新状态为准。', { exact: true })).toBeVisible()
  expect(mock.writes('release/activate')[1].postDataJSON()).toEqual(first)
})

for (const mode of ['first', 'same', 'active', 'empty'] as const) test(`比较边界：${mode}`, async ({ page }) => {
  const { mock, state } = await setup(page)
  if (mode === 'first') state.active = null
  if (mode === 'same' || mode === 'empty') state.same = true
  if (mode === 'empty') { state.empty = true; state.active = null }
  if (mode === 'active') state.active = { ...initialActive, ...candidate }
  await review(page)
  expect(mock.count('freeze/lock')).toBe(0)
  expect(mock.count('release/create')).toBe(0)
  if (mode === 'first') expect(mock.writes('release/compare')[0].postDataJSON().expected_active_revision).toBe('0')
  if (mode === 'same' || mode === 'empty') await expect(dialog(page).getByText('配置内容相同，仍可切换到 v2。', { exact: true })).toBeVisible()
  if (mode === 'active') await expect(dialog(page).getByRole('button', { name: '确认生效 v2', exact: true })).toHaveCount(0)
  else await expect(dialog(page).getByRole('button', { name: '确认生效 v2', exact: true })).toBeEnabled()
})

test('编辑区已冻结时跳过锁版；候选详情身份不匹配后不能继续生效', async ({ page }) => {
  const { mock } = await setup(page)
  mock.locked = true
  await page.goto('releases' + scopeQuery)
  await page.getByRole('button', { name: '发布配置', exact: true }).click()
  await expect(dialog(page).getByText('版本号由系统生成。', { exact: true })).toBeVisible()
  await dialog(page).getByRole('button', { name: '创建候选版本', exact: true }).click()
  await expect(dialog(page).getByLabel('差异统计')).toBeVisible()
  expect(mock.count('freeze/lock')).toBe(0)
  mock.overrides.set('release/detail', route => ok(route, { meta: { ...candidate, snapshot_id: 'different-snapshot' }, configs: [config], assets: [] }))
  await dialog(page).locator('.comparison-row').first().click()
  await expect(dialog(page).getByLabel('差异统计')).toHaveCount(0)
  await expect(dialog(page).getByRole('button', { name: '确认生效 v2', exact: true })).toBeDisabled()
  expect(mock.count('release/activate')).toBe(0)
})

test('同名新范围不会接收旧范围未确认的创建请求', async ({ page }) => {
  const { mock } = await setup(page)
  mock.overrides.set('release/create', route => route.abort('failed'))
  await page.goto('releases' + scopeQuery)
  await page.getByRole('button', { name: '发布配置', exact: true }).click()
  await expect(dialog(page).getByText('版本号由系统生成。', { exact: true })).toBeVisible()
  await dialog(page).getByRole('button', { name: '锁版并创建候选版本', exact: true }).click()
  await expect(dialog(page).getByText('创建结果尚未确认', { exact: true })).toBeVisible()
  const replacement = '00000000-0000-4000-8000-000000000099'
  mock.overrides.set('runtime/overview', route => ok(route, { list: [{ environment_key: 'qa', environment_name: '演示环境', pipelines: [
    { pipeline_key: 'web', pipeline_name: '网页渠道', scope_id: replacement, configs: [], config_count: 0 },
  ] }], total: 1 }))
  mock.overrides.set('freeze/status', route => ok(route, { ...initialActive, scope_id: replacement, state: 'unlocked', revision: '0' }))
  mock.overrides.set('release/active', route => ok(route, { active: null, active_revision: '0' }))
  mock.overrides.set('release/list', route => ok(route, { items: [], active: null, has_more: false }))
  await page.reload()
  await page.getByRole('button', { name: '发布配置', exact: true }).click()
  await expect(dialog(page).getByLabel('版本号', { exact: true })).toHaveCount(0)
  await expect(dialog(page).getByText('创建结果尚未确认', { exact: true })).toHaveCount(0)
  expect(mock.count('release/create')).toBe(1)
  expect(mock.count('release/activate')).toBe(0)
})

test('compare 继续使用管理员鉴权，401 后不自动切换', async ({ page }) => {
  const { mock } = await setup(page)
  mock.overrides.set('release/compare', route => fail(route, 401, 'AUTH_REQUIRED'))
  await page.goto('releases' + scopeQuery)
  await page.getByRole('button', { name: '查看差异', exact: true }).click()
  await expect(page.getByRole('heading', { name: '登录工作台', exact: true })).toBeVisible()
  expect(mock.count('release/activate')).toBe(0)
})

test('维护期间可以审阅，确认生效503不伪报成功且保留原请求', async ({ page }) => {
  const { mock } = await setup(page)
  await review(page)
  mock.overrides.set('release/activate', route => fail(route, 503, 'BACKUP_MAINTENANCE'))
  await dialog(page).getByRole('button', { name: '确认生效 v2', exact: true }).click()
  await expect(dialog(page).getByText('切换结果尚未确认', { exact: true })).toBeVisible()
  await expect(dialog(page).getByRole('heading', { name: '版本 v2 已生效', exact: true })).toHaveCount(0)
  await expect(dialog(page).getByRole('alert')).toContainText('暂停业务写入')
  expect(mock.count('release/activate')).toBe(1)
})

test('分页超时原参数重试，分页 CAS 失效清空全部旧差异', async ({ page }) => {
  const { mock } = await setup(page)
  let attempt = 0
  mock.overrides.set('release/compare', route => {
    const p = route.request().postDataJSON()
    if (p.offset === 20 && ++attempt === 1) return fail(route, 503, 'RELEASE_COMPARE_TIMEOUT')
    if (p.offset === 20 && attempt === 3) return fail(route, 409, 'RELEASE_COMPARE_BASELINE_MISMATCH')
    return ok(route, { comparison_mode: 'active', scope_id: scopeId, target: candidate, baseline: initialActive, active_revision: initialActive.revision,
      target_is_active: false, has_changes: true, summary: { added: 40, modified: 0, removed: 0, unchanged: 0 },
      items: [{ resource_key: String(p.offset), change_type: 'added', changed_fields: [], before: null, after: config }],
      total_changes: 40, offset: p.offset, limit: 20, has_more: true, next_offset: 20 })
  })
  await review(page)
  await dialog(page).getByRole('button', { name: '加载更多差异', exact: true }).click()
  await expect(dialog(page).getByRole('button', { name: '确认生效 v2', exact: true })).toBeDisabled()
  // A failed page can be retried without changing the baseline or offset.
  mock.overrides.set('release/compare', route => fail(route, 409, 'RELEASE_COMPARE_BASELINE_MISMATCH'))
  await dialog(page).getByRole('button', { name: '加载更多差异', exact: true }).click()
  await expect(dialog(page).getByLabel('差异统计')).toBeHidden()
  const requests = mock.writes('release/compare')
  expect(requests[1].postDataJSON()).toEqual(requests[2].postDataJSON())
  expect(mock.count('release/activate')).toBe(0)
})

for (const status of [410, 503]) test(`比较失败 ${status} 不伪装零差异`, async ({ page }) => {
  const { mock } = await setup(page)
  mock.overrides.set('release/compare', route => fail(route, status, status === 410 ? 'RELEASE_VERSION_DELETED' : 'RELEASE_INCOMPLETE'))
  await page.goto('releases' + scopeQuery)
  await page.getByRole('button', { name: '查看差异', exact: true }).click()
  await expect(dialog(page).getByRole('alert')).toBeVisible()
  await expect(dialog(page).getByLabel('差异统计')).toHaveCount(0)
  await expect(dialog(page).getByRole('button', { name: '确认生效 v2', exact: true })).toBeDisabled()
  expect(mock.count('release/activate')).toBe(0)
})
