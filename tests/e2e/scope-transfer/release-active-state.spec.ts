import { test, expect } from '@playwright/test'
import { readFile, writeFile } from 'node:fs/promises'
import { join } from 'node:path'

test('真实前端：停服临时库null+7夹具→审阅→明确生效；API404不清会话或下载', async ({ page, baseURL }) => {
  const endpoint = (path: string) => new URL('api/v1/' + path, baseURL).href
  const fixture = JSON.parse(await readFile(join(process.env.RB_SCOPE_UI_INSTANCE!, 'state-fixture.json'), 'utf8'))
  expect(fixture.fixture_only).toBe(true)
  const writes: string[] = []
  page.on('request', request => {
    if (request.method() === 'POST' && request.url().endsWith('/release/activate')) writes.push(request.postData()!)
  })
  await page.goto('releases?env=default&pipeline=default')
  await page.getByLabel('账号', { exact: true }).fill(process.env.RB_QA_ADMIN_USERNAME!)
  await page.getByLabel('密码', { exact: true }).fill(process.env.RB_QA_ADMIN_PASSWORD!)
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await expect(page.getByRole('heading', { name: '版本发布', exact: true })).toBeVisible()
  const me = (await (await page.request.get(endpoint('auth/me'))).json()).data
  const query = new URLSearchParams({ environment_key: 'default', pipeline_key: 'default', scope_id: fixture.scope_id })
  const getState = async () => (await (await page.request.get(endpoint('release/active?' + query))).json()).data
  expect(await getState()).toEqual({ active: null, active_revision: '7' })
  const config = await page.request.post(endpoint('config/create'), {
    headers: { Origin: new URL(baseURL!).origin, 'X-CSRF-Token': me.csrf_token },
    data: { expected_scope_id: fixture.scope_id, config: { environment_key: 'default', pipeline_key: 'default',
      alias: 'STATE_UI_VALUE', name: '状态接入核对', type: 'text', content: 'state-ui' } },
  })
  expect(config.status()).toBe(200)
  await page.getByRole('button', { name: '发布配置', exact: true }).click()
  const panel = page.getByRole('dialog', { name: '发布审阅', exact: true })
  const created = page.waitForResponse(response => response.url().endsWith('/release/create'))
  const compared = page.waitForResponse(response => response.url().endsWith('/release/compare'))
  await panel.getByRole('button', { name: '锁版并创建候选版本', exact: true }).click()
  const candidate = (await (await created).json()).data.meta
  const comparison = await compared
  expect(comparison.status()).toBe(200)
  expect(comparison.request().postDataJSON()).toMatchObject({ expected_active_revision: '7', expected_active_snapshot_id: '', expected_scope_id: fixture.scope_id })
  expect((await comparison.json()).data.active_revision).toBe('7')
  await expect(panel.getByLabel('差异统计')).toBeVisible()
  expect(writes).toHaveLength(0)
  expect(await getState()).toEqual({ active: null, active_revision: '7' })
  await panel.getByRole('button', { name: `确认生效 ${candidate.version}`, exact: true }).click()
  await expect(panel.getByRole('heading', { name: `版本 ${candidate.version} 已生效`, exact: true })).toBeVisible()
  expect(writes).toHaveLength(1)
  expect(JSON.parse(writes[0]).expected_active_revision).toBe('7')
  const after = await getState()
  expect(after.active_revision).toBe('8')
  expect(after.active.revision).toBe(after.active_revision)
  expect(after.active.snapshot_id).toBe(candidate.snapshot_id)
  await panel.getByRole('button', { name: '完成', exact: true }).click()

  await page.route('**/api/v1/release/active?*', route => route.continue({ url: endpoint('release/no-such-state') }))
  await page.getByRole('button', { name: '刷新', exact: true }).click()
  await expect(page.getByText('接口地址不存在或服务版本不匹配，请确认服务地址与版本。', { exact: true })).toBeVisible()
  expect((await page.request.get(endpoint('auth/me'))).status()).toBe(200)
  await page.unroute('**/api/v1/release/active?*')

  let downloads = 0
  page.on('download', () => downloads++)
  await page.route('**/api/v1/transfer/export', route => route.continue({ url: endpoint('transfer/no-such-export') }))
  await page.goto('import-export')
  await page.getByRole('tab', { name: '数据导出', exact: true }).click()
  await page.getByRole('checkbox', { name: '选择环境 默认环境', exact: true }).check()
  await page.getByRole('button', { name: '导出数据', exact: true }).click()
  await expect(page.getByText('接口地址不存在或服务版本不匹配，请确认服务地址与版本。', { exact: true })).toBeVisible()
  expect(downloads).toBe(0)
  expect((await page.request.get(endpoint('auth/me'))).status()).toBe(200)
  await writeFile(join(process.env.RB_SCOPE_UI_INSTANCE!, 'release-active-state-evidence.json'), JSON.stringify({
    fixture: { active: null, active_revision: '7', offline_temporary_database_only: true },
    compared_revision: '7', activate_requests_after_explicit_confirmation: writes.length,
    actual_snapshot: candidate.snapshot_id, actual_version: candidate.version, after_revision: after.active_revision,
    api_not_found_kept_session: true, api_not_found_downloads: downloads,
  }, null, 2))
})
