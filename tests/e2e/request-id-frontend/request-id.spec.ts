import { test, expect, type Page } from '@playwright/test'
import { mockApi, ok, fail, meta, scopeQuery, fixtureIdentity } from '../auth/mock'
import { backupFixture, backupItem, accepted, job, packageFile } from '../auth/backup-fixture'

const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/
const scopeId = fixtureIdentity('qa', 'web')
const candidate = { ...meta, scope_id: scopeId, snapshot_id: fixtureIdentity('uuid-candidate') }
const panel = (page: Page) => page.getByRole('dialog', { name: '发布审阅', exact: true })

async function capabilities(page: Page, mode: 'native' | 'fallback' | 'none') {
  await page.addInitScript(mode => {
    const source = globalThis.crypto
    const counts = { native: 0, random: 0 }
    Object.assign(window, { requestIdCounts: counts })
    const native = source.randomUUID?.bind(source), random = source.getRandomValues.bind(source)
    Object.defineProperty(source, 'randomUUID', { configurable: true, value: mode === 'native' ? function () { counts.native++; return native!() } : undefined })
    Object.defineProperty(source, 'getRandomValues', { configurable: true, value: mode === 'none' ? undefined : function (bytes: Uint8Array) { counts.random++; return random(bytes) } })
  }, mode)
}
async function releaseFixture(page: Page) {
  const mock = await mockApi(page, true)
  const state = { active: null as Record<string, unknown> | null, deleted: false }
  mock.overrides.set('release/active', route => ok(route, { active: state.active, active_revision: state.active?.revision ?? '0' }))
  mock.overrides.set('release/list', route => ok(route, { items: state.deleted ? [] : [{ meta: candidate, is_active: !!state.active }], has_more: false, active: state.active }))
  mock.overrides.set('release/create', route => ok(route, { meta: candidate, replayed: false }))
  mock.overrides.set('release/compare', route => {
    const p = route.request().postDataJSON()
    return ok(route, { comparison_mode: 'active', scope_id: scopeId, baseline: null, target: candidate, active_revision: p.expected_active_revision,
      target_is_active: false, has_changes: false, summary: { added: 0, removed: 0, modified: 0, unchanged: 0 },
      items: [], total_changes: 0, offset: 0, limit: 20, has_more: false, next_offset: null })
  })
  mock.overrides.set('release/activate', route => {
    const p = route.request().postDataJSON()
    state.active = { scope_id: scopeId, snapshot_id: candidate.snapshot_id, version: candidate.version, revision: '1' }
    return ok(route, { operation: { request_id: p.request_id, replayed: false, applied_revision: '1' }, version: p.version, active: state.active })
  })
  mock.overrides.set('release/delete', route => {
    const p = route.request().postDataJSON(); state.deleted = true
    return ok(route, { ...p, snapshot_id: p.expected_snapshot_id, deleted: true, replayed: false, deleted_at: '2026-10-29T00:00:00Z', deleted_by: 'admin' })
  })
  return { mock, state }
}

for (const mode of ['native', 'fallback'] as const) test(`${mode}：创建和生效各生成一次合法编号，副作用前已取得编号`, async ({ page }) => {
  await capabilities(page, mode)
  const { mock } = await releaseFixture(page)
  let countAtLock = 0
  mock.overrides.set('freeze/lock', async route => {
    countAtLock = await page.evaluate(() => {
      const c = (window as unknown as { requestIdCounts: { native: number; random: number } }).requestIdCounts
      return c.native + c.random
    })
    mock.locked = true
    await ok(route, { scope_id: scopeId, environment_key: 'qa', pipeline_key: 'web', state: 'locked', revision: '2', admin_configured: true })
  })
  await page.goto('releases' + scopeQuery)
  await page.getByRole('button', { name: '发布配置', exact: true }).click()
  await panel(page).getByLabel('备注（选填）', { exact: true }).fill('UUID兼容验证')
  await panel(page).getByRole('button', { name: '锁版并创建候选版本', exact: true }).click()
  await expect(panel(page).getByLabel('差异统计')).toBeVisible()
  expect(countAtLock).toBe(1)
  const create = mock.writes('release/create')[0].postDataJSON()
  expect(create.request_id).toMatch(uuid)
  expect(create).toMatchObject({ reason: 'UUID兼容验证', expected_scope_id: scopeId, expected_freeze_revision: '2' })
  expect(mock.count('release/activate')).toBe(0)
  await panel(page).getByRole('button', { name: '确认生效 v1', exact: true }).click()
  await expect(panel(page).getByRole('heading', { name: '版本 v1 已生效', exact: true })).toBeVisible()
  const activate = mock.writes('release/activate')[0].postDataJSON()
  expect(activate.request_id).toMatch(uuid)
  expect(activate.request_id).not.toBe(create.request_id)
  expect(activate.expected_active_revision).toBe('0')
})

for (const locked of [false, true]) test(`没有随机源保留备注、不发锁版或创建，原冻结=${locked}`, async ({ page }) => {
  await capabilities(page, 'none')
  const { mock } = await releaseFixture(page); mock.locked = locked
  await page.goto('releases' + scopeQuery)
  await page.getByRole('button', { name: '发布配置', exact: true }).click()
  await panel(page).getByLabel('备注（选填）', { exact: true }).fill('保留输入')
  await panel(page).getByRole('button', { name: locked ? '创建候选版本' : '锁版并创建候选版本', exact: true }).click()
  await expect(panel(page).getByRole('alert')).toContainText('当前浏览器无法生成操作编号')
  await expect(panel(page).getByLabel('备注（选填）', { exact: true })).toHaveValue('保留输入')
  expect(mock.count('freeze/lock')).toBe(0)
  expect(mock.count('release/create')).toBe(0)
  expect(mock.locked).toBe(locked)
})

test('fallback网络结果不明后重开/刷新/确认原样重发；新意图才换编号', async ({ page }) => {
  await capabilities(page, 'fallback')
  const { mock } = await releaseFixture(page)
  mock.overrides.set('release/create', route => route.abort('failed'))
  await page.goto('releases' + scopeQuery)
  await page.getByRole('button', { name: '发布配置', exact: true }).click()
  await panel(page).getByRole('button', { name: '锁版并创建候选版本', exact: true }).click()
  await expect(panel(page).getByText('创建结果尚未确认', { exact: true })).toBeVisible()
  const first = mock.writes('release/create')[0].postDataJSON()
  await panel(page).getByRole('button', { name: '稍后确认', exact: true }).click()
  await page.reload()
  await page.getByRole('button', { name: '发布配置', exact: true }).click()
  mock.overrides.set('release/create', route => ok(route, { meta: candidate, replayed: true }))
  await panel(page).getByRole('button', { name: '确认原请求结果', exact: true }).click()
  await expect(panel(page).getByLabel('差异统计')).toBeVisible()
  expect(mock.writes('release/create')[1].postDataJSON()).toEqual(first)
  expect(await page.evaluate(() => (window as unknown as { requestIdCounts: { random: number } }).requestIdCounts.random)).toBe(0)
  await panel(page).getByRole('button', { name: '暂不发布', exact: true }).click()
  await page.getByRole('button', { name: '创建版本', exact: true }).click()
  await page.getByRole('dialog').getByRole('button', { name: '保存版本', exact: true }).click()
  await expect(page.getByRole('dialog')).toBeHidden()
  expect(mock.writes('release/create')[2].postDataJSON().request_id).toMatch(uuid)
  expect(mock.writes('release/create')[2].postDataJSON().request_id).not.toBe(first.request_id)
})

test('删除版本缺少随机源不发送请求且保留备注', async ({ page }) => {
  await capabilities(page, 'fallback')
  const { mock } = await releaseFixture(page)
  await page.goto('releases' + scopeQuery)
  await page.getByRole('button', { name: '删除版本', exact: true }).click()
  const dialog = page.getByRole('dialog')
  await dialog.getByLabel('备注（选填）', { exact: true }).fill('仅删除候选')
  await page.evaluate(() => Object.defineProperty(crypto, 'getRandomValues', { configurable: true, value: undefined }))
  await dialog.getByRole('button', { name: '删除版本', exact: true }).click()
  await expect(dialog.getByRole('alert')).toContainText('当前浏览器无法生成操作编号')
  await expect(dialog.getByLabel('备注（选填）', { exact: true })).toHaveValue('仅删除候选')
  expect(mock.count('release/delete')).toBe(0)
})

test('fallback删除请求可送达，网络不明后确认沿用原编号与snapshot', async ({ page }) => {
  await capabilities(page, 'fallback')
  const { mock } = await releaseFixture(page)
  const success = mock.overrides.get('release/delete')!
  mock.overrides.set('release/delete', route => route.abort('failed'))
  await page.goto('releases' + scopeQuery)
  await page.getByRole('button', { name: '删除版本', exact: true }).click()
  await page.getByRole('dialog').getByRole('button', { name: '删除版本', exact: true }).click()
  await expect(page.getByRole('button', { name: '确认结果', exact: true })).toBeVisible()
  const first = mock.writes('release/delete')[0].postDataJSON()
  expect(first.request_id).toMatch(uuid)
  expect(first.expected_snapshot_id).toBe(candidate.snapshot_id)
  mock.overrides.set('release/delete', success)
  await page.getByRole('button', { name: '确认结果', exact: true }).click()
  await expect(page.getByRole('dialog')).toBeHidden()
  expect(mock.writes('release/delete')[1].postDataJSON()).toEqual(first)
})

test('生效时没有随机源保留审阅和备注，不发activate', async ({ page }) => {
  await capabilities(page, 'none')
  const { mock } = await releaseFixture(page)
  await page.goto('releases' + scopeQuery)
  await page.getByRole('button', { name: '查看差异', exact: true }).click()
  await panel(page).getByLabel('生效备注（选填）', { exact: true }).fill('保留审阅备注')
  await panel(page).getByRole('button', { name: '确认生效 v1', exact: true }).click()
  await expect(panel(page).getByRole('alert')).toContainText('当前浏览器无法生成操作编号')
  await expect(panel(page).getByLabel('生效备注（选填）', { exact: true })).toHaveValue('保留审阅备注')
  await expect(panel(page).getByLabel('差异统计')).toBeVisible()
  expect(mock.count('release/activate')).toBe(0)
})

test('独立创建无随机源显示可读错误并保留备注，不出现假确认结果', async ({ page }) => {
  await capabilities(page, 'none')
  const { mock } = await releaseFixture(page); mock.locked = true
  await page.goto('releases' + scopeQuery)
  await page.getByRole('button', { name: '创建版本', exact: true }).click()
  await page.locator('#release-reason').fill('独立创建保留')
  await page.getByRole('dialog').getByRole('button', { name: '保存版本', exact: true }).click()
  await expect(page.getByRole('dialog')).toContainText('当前浏览器无法生成操作编号')
  await expect(page.locator('#release-reason')).toHaveValue('独立创建保留')
  await expect(page.getByRole('button', { name: '确认结果', exact: true })).toHaveCount(0)
  expect(mock.count('release/create')).toBe(0)
})

test('fallback备份创建/删除意图正常生成编号', async ({ page }) => {
  await capabilities(page, 'fallback')
  const { state, mock } = await backupFixture(page); state.items = [backupItem]
  mock.overrides.set('backup/create', route => accepted(route, job('create', 'succeeded', { phase: 'completed' })))
  await page.goto('backups')
  await page.getByRole('button', { name: '创建备份', exact: true }).click()
  await page.getByRole('button', { name: '开始备份', exact: true }).click()
  await expect(page.getByRole('heading', { name: '备份完成', exact: true })).toBeVisible()
  expect(mock.writes('backup/create')[0].postDataJSON().request_id).toMatch(uuid)
  await page.getByRole('button', { name: /^删除备份 / }).click()
  await page.getByRole('dialog').getByRole('button', { name: '确认删除备份', exact: true }).click()
  await expect.poll(() => mock.count('backup/delete')).toBe(1)
  expect(mock.writes('backup/delete')[0].postDataJSON().request_id).toMatch(uuid)
})

test('fallback恢复上传/确认/取消意图均有合法编号', async ({ page }) => {
  await capabilities(page, 'fallback')
  const { mock } = await backupFixture(page)
  await page.goto('backups')
  await page.getByLabel('业务备份包', { exact: true }).setInputFiles(packageFile)
  await page.getByRole('button', { name: '上传并校验', exact: true }).click()
  await expect(page.getByRole('heading', { name: '恢复预览', exact: true })).toBeVisible()
  expect(mock.writes('backup/restore/preview')[0].postData()).toMatch(/[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}/)
  await page.getByRole('button', { name: '核对并恢复', exact: true }).click()
  await page.getByRole('checkbox', { name: '我已核对预览，确认覆盖全部业务数据' }).check()
  await page.getByRole('button', { name: '确认覆盖并准备恢复', exact: true }).click()
  await expect(page.getByRole('heading', { name: '等待服务重启', exact: true })).toBeVisible()
  await page.getByRole('button', { name: '取消待恢复计划', exact: true }).click()
  await page.getByRole('dialog').getByRole('button', { name: '取消恢复计划', exact: true }).click()
  await expect.poll(() => mock.count('backup/restore/cancel')).toBe(1)
  expect(mock.writes('backup/restore/execute')[0].postDataJSON().request_id).toMatch(uuid)
  expect(mock.writes('backup/restore/cancel')[0].postDataJSON().request_id).toMatch(uuid)
})

test('备份没有随机源时显示可读错误，备注保留且不发送请求', async ({ page }) => {
  await capabilities(page, 'none')
  const { mock } = await backupFixture(page)
  await page.goto('backups')
  await page.getByRole('button', { name: '创建备份', exact: true }).click()
  await page.getByRole('dialog').getByLabel('备注（选填）').fill('备份备注保留')
  await page.getByRole('button', { name: '开始备份', exact: true }).click()
  await expect(page.getByRole('dialog').getByRole('alert')).toContainText('当前浏览器无法生成操作编号')
  await expect(page.getByRole('dialog').getByLabel('备注（选填）')).toHaveValue('备份备注保留')
  expect(mock.count('backup/create')).toBe(0)
})

test('取消恢复时随机源失效，不发取消、不丢弃既有待重启计划', async ({ page }) => {
  await capabilities(page, 'fallback')
  const { mock } = await backupFixture(page)
  await page.goto('backups')
  await page.getByLabel('业务备份包', { exact: true }).setInputFiles(packageFile)
  await page.getByRole('button', { name: '上传并校验', exact: true }).click()
  await page.getByRole('button', { name: '核对并恢复', exact: true }).click()
  await page.getByRole('checkbox', { name: '我已核对预览，确认覆盖全部业务数据' }).check()
  await page.getByRole('button', { name: '确认覆盖并准备恢复', exact: true }).click()
  await expect(page.getByRole('heading', { name: '等待服务重启', exact: true })).toBeVisible()
  await page.evaluate(() => Object.defineProperty(crypto, 'getRandomValues', { configurable: true, value: undefined }))
  await page.getByRole('button', { name: '取消待恢复计划', exact: true }).click()
  await page.getByRole('dialog').getByRole('button', { name: '取消恢复计划', exact: true }).click()
  await expect(page.getByRole('dialog').getByRole('alert')).toContainText('当前浏览器无法生成操作编号')
  expect(mock.count('backup/restore/cancel')).toBe(0)
})

test('fallback批量结果均为v4/variant UUID且不重复；原生被拒绝时仍可用安全字节', async ({ page, baseURL }) => {
  await capabilities(page, 'fallback')
  await releaseFixture(page)
  await page.goto('releases' + scopeQuery)
  const result = await page.evaluate(async url => {
    const { createRequestId } = await import(/* @vite-ignore */ url)
    const ids = Array.from({ length: 512 }, () => createRequestId())
    Object.defineProperty(crypto, 'randomUUID', { configurable: true, value() { throw new Error('native denied') } })
    return { ids, deniedNative: createRequestId() }
  }, new URL('src/lib/request-id.ts', baseURL!).href)
  expect(result.ids.every(id => uuid.test(id))).toBe(true)
  expect(new Set(result.ids).size).toBe(result.ids.length)
  expect(result.deniedNative).toMatch(uuid)
})

test('整个crypto对象缺失或安全字节接口抛错时给出可读失败', async ({ page, baseURL }) => {
  await releaseFixture(page)
  await page.goto('releases' + scopeQuery)
  const errors = await page.evaluate(async url => {
    const { createRequestId } = await import(/* @vite-ignore */ url)
    const source = crypto
    const errors = []
    Object.defineProperty(globalThis, 'crypto', { configurable: true, value: undefined })
    try { createRequestId() } catch (error) { errors.push((error as Error).message) }
    Object.defineProperty(globalThis, 'crypto', { configurable: true, value: source })
    Object.defineProperty(source, 'randomUUID', { configurable: true, value: undefined })
    Object.defineProperty(source, 'getRandomValues', { configurable: true, value() { throw new Error('bytes denied') } })
    try { createRequestId() } catch (error) { errors.push((error as Error).message) }
    return errors
  }, new URL('src/lib/request-id.ts', baseURL!).href)
  expect(errors).toEqual(Array(2).fill('当前浏览器无法生成操作编号，请更换浏览器后重试。'))
})
