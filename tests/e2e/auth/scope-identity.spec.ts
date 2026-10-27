import { test, expect, type Page } from '@playwright/test'
import { mockApi, ok, fail, config, fixtureIdentity, CSRF } from './mock'

async function setup(page: Page) {
  const mock = await mockApi(page, true)
  const state = { generation: 1, exists: true, locked: false }
  const environmentId = () => fixtureIdentity('qa-generation-' + state.generation)
  const scopeId = () => fixtureIdentity('qa-generation-' + state.generation, 'web')
  mock.overrides.set('runtime/overview', route => ok(route, { list: state.exists ? [{ environment_key: 'qa', environment_name: '演示环境', environment_id: environmentId(), pipelines: [
    { pipeline_key: 'web', pipeline_name: '网页渠道', environment_id: environmentId(), scope_id: scopeId() },
  ] }] : [], total: state.exists ? 1 : 0 }))
  mock.overrides.set('freeze/status', route => ok(route, { environment_key: 'qa', pipeline_key: 'web', scope_id: scopeId(), state: state.locked ? 'locked' : 'unlocked', revision: '2', admin_configured: true }))
  mock.overrides.set('environment/delete', route => {
    const body = route.request().postDataJSON()
    if (body.expected_environment_id !== environmentId()) return fail(route, 409, 'SCOPE_IDENTITY_CONFLICT')
    if (state.locked) return fail(route, 409, 'SCOPE_FROZEN')
    state.exists = false
    return ok(route, { deleted: true, environment_id: environmentId(), pipelines: 1, configs: 1, assets: 1, versions: 3 })
  })
  mock.overrides.set('environment/create', route => { state.generation++; state.exists = true; return ok(route, {}) })
  mock.overrides.set('config/list', route => ok(route, { list: [{ ...config, scope_id: scopeId(), content: '第' + state.generation + '代配置' }], total: 1 }))
  mock.overrides.set('config/detail', route => ok(route, { config: { ...config, scope_id: scopeId() } }))
  return { mock, state, environmentId, scopeId }
}
async function deleteEnvironment(page: Page) {
  await page.getByRole('button', { name: '删除环境 演示环境', exact: true }).click()
  const dialog = page.getByRole('dialog')
  await expect(dialog).toContainText('配置、资源和发布版本')
  await dialog.getByRole('button', { name: '确认删除', exact: true }).click()
}

test('解锁环境即使有历史版本也可删除；删除与同名重建使用各自身份', async ({ page }) => {
  const { mock, state, environmentId } = await setup(page)
  await page.goto('environments')
  const original = environmentId()
  await deleteEnvironment(page)
  await expect(page.getByRole('dialog')).toBeHidden()
  await expect(page.getByText('暂无环境，请添加', { exact: true })).toBeVisible()
  expect(mock.writes('environment/delete')[0].postDataJSON()).toEqual({ environment_key: 'qa', expected_environment_id: original })
  expect(mock.count('release/list')).toBe(0)
  await page.getByRole('button', { name: '新增环境', exact: true }).click()
  await page.locator('#env-key').fill('qa'); await page.locator('#env-name').fill('演示环境')
  await page.getByRole('dialog').getByRole('button', { name: '保存', exact: true }).click()
  await expect(page.getByRole('dialog')).toBeHidden()
  expect(state.generation).toBe(2)
  expect(environmentId()).not.toBe(original)
  await deleteEnvironment(page)
  await expect(page.getByRole('dialog')).toBeHidden()
  expect(mock.writes('environment/delete')[1].postDataJSON().expected_environment_id).toBe(environmentId())
})

for (const kind of ['SCOPE_FROZEN', 'SCOPE_INVALID_REQUEST', 'SCOPE_IDENTITY_CONFLICT', 'SCOPE_DELETED', 'DATABASE_UNAVAILABLE']) {
  test('删除明确展示错误并阻止危险重试：' + kind, async ({ page }) => {
    const { mock } = await setup(page)
    mock.overrides.set('environment/delete', route => fail(route, kind === 'SCOPE_INVALID_REQUEST' ? 400 : kind === 'SCOPE_DELETED' ? 410 : kind === 'DATABASE_UNAVAILABLE' ? 503 : 409, kind))
    await page.goto('environments')
    await deleteEnvironment(page)
    const dialog = page.getByRole('dialog')
    await expect(dialog).toBeVisible()
    if (kind === 'SCOPE_FROZEN') await expect(dialog).toContainText('请先解锁相关渠道')
    else if (kind === 'SCOPE_INVALID_REQUEST') await expect(dialog).toContainText('范围信息不完整')
    else if (kind === 'SCOPE_IDENTITY_CONFLICT') await expect(dialog).toContainText('重新创建')
    else if (kind === 'SCOPE_DELETED') await expect(dialog).toContainText('已删除')
    else { await expect(dialog).toContainText('删除结果尚未确认'); await expect(dialog).not.toContainText('检查冻结状态') }
    if (kind !== 'SCOPE_FROZEN') await expect(dialog.getByRole('button', { name: '确认删除', exact: true })).toBeDisabled()
    expect(mock.writes('environment/delete')).toHaveLength(1)
  })
}

test('旧删除确认不能删除同名新环境，核对也不会自动再删', async ({ page }) => {
  const { mock, state, environmentId } = await setup(page)
  await page.goto('environments')
  await page.getByRole('button', { name: '删除环境 演示环境', exact: true }).click()
  const old = environmentId()
  state.generation++
  await page.getByRole('dialog').getByRole('button', { name: '确认删除', exact: true }).click()
  await expect(page.getByRole('dialog')).toContainText('重新创建')
  expect(mock.writes('environment/delete')[0].postDataJSON().expected_environment_id).toBe(old)
  await page.getByRole('dialog').getByRole('button', { name: '刷新环境并核对', exact: true }).click()
  await expect(page.getByRole('dialog')).toContainText('原对象已删除或重新创建')
  expect(state.exists).toBe(true)
  expect(mock.writes('environment/delete')).toHaveLength(1)
})

test('配置编辑保留原 revision 和身份；410 后保留草稿并禁用旧保存', async ({ page }) => {
  const { mock, scopeId } = await setup(page)
  mock.overrides.set('config/update', route => fail(route, 410, 'SCOPE_DELETED'))
  await page.goto('config?env=qa&pipeline=web')
  await page.getByRole('button', { name: '编辑 站点标题', exact: true }).click()
  const dialog = page.getByRole('dialog')
  await expect(dialog.getByLabel('名称', { exact: false })).toHaveValue('站点标题')
  await dialog.getByLabel('名称', { exact: false }).fill('保留的草稿')
  await dialog.getByRole('button', { name: '保存', exact: true }).click()
  await expect(dialog).toContainText('该环境或渠道已删除')
  await expect(dialog.getByLabel('名称', { exact: false })).toHaveValue('保留的草稿')
  const payload = mock.writes('config/update')[0].postDataJSON()
  expect(payload.expected_scope_id).toBe(scopeId())
  expect(payload.expected_edit_revision).toBe(config.edit_revision)
  await expect(dialog.getByRole('button', { name: '保存', exact: true })).toBeDisabled()
  expect(mock.writes('config/update')[0].headers()['x-csrf-token']).toBe(CSRF)
})

test('旧范围的迟到配置响应不能污染同名新范围', async ({ page }) => {
  const { mock, state, scopeId } = await setup(page)
  let release!: () => void
  const gate = new Promise<void>(resolve => { release = resolve })
  const originalScope = scopeId()
  let calls = 0
  mock.overrides.set('config/list', async route => {
    if (++calls === 1) { await gate; return ok(route, { list: [{ ...config, scope_id: originalScope, content: '迟到的旧配置' }], total: 1 }) }
    return ok(route, { list: [{ ...config, scope_id: scopeId(), content: '同名新范围配置' }], total: 1 })
  })
  await page.goto('config?env=qa&pipeline=web')
  await expect.poll(() => mock.count('config/list')).toBe(1)
  const nav = page.getByRole('navigation', { name: '主要导航' })
  await nav.getByRole('link', { name: '环境管理', exact: true }).click()
  state.generation++
  await nav.getByRole('link', { name: '配置管理', exact: true }).click()
  await expect(page.getByText('同名新范围配置', { exact: true })).toBeVisible()
  release()
  await expect(page.getByText('迟到的旧配置', { exact: true })).toHaveCount(0)
  const queries = mock.requests.filter(request => new URL(request.url()).pathname.endsWith('/config/list')).map(request => new URL(request.url()).searchParams.get('scope_id'))
  expect(queries).toEqual([originalScope, scopeId()])
})
