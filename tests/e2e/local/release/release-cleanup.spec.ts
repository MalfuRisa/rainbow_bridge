import { test, expect, type Page } from '@playwright/test'
import { targetA, url } from '../support/env'
import { ensureScope, createConfig, uploadAsset, TINY_PNG, type Scope } from '../support/scopes'
import { getJson, postJson } from '../support/net'

test('真实UI：空备注、删除/确认、历史、固定读取410与并发生效保护 @release', async ({ page, browser }) => {
  test.setTimeout(120000)
  const server = targetA()
  const scope: Scope = { env: 'qa_cleanup_' + Date.now().toString(36), pipeline: 'web', label: 'cleanup', sentinel: 'cleanup' }
  await ensureScope(server, scope)
  const asset = await uploadAsset(server, scope, { fileName: 'cleanup.png', contentType: 'image/png', bytes: TINY_PNG })
  await createConfig(server, scope, { alias: 'CLEANUP_TITLE', name: '清理标题', type: 'text', content: '保留的生效内容' })
  await createConfig(server, scope, { alias: 'CLEANUP_IMAGE', name: '清理图片', type: 'image', content: asset.reference })
  const query = '?env=' + scope.env + '&pipeline=' + scope.pipeline
  await page.goto(url(server, '/config' + query))
  await expect(page.getByRole('heading', { name: '登录工作台', exact: true })).toBeVisible()
  await page.getByLabel('账号', { exact: true }).fill(process.env.RB_QA_ADMIN_USERNAME || 'admin')
  try { await page.getByLabel('密码', { exact: true }).fill(process.env.RB_QA_ADMIN_PASSWORD || '') }
  catch { throw new Error('测试凭据输入失败，已隐藏内容') }
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await expect(page.getByRole('heading', { name: '配置管理', exact: true })).toBeVisible()
  async function freeze(lock: boolean) {
    await page.goto(url(server, '/config' + query))
    await page.getByRole('button', { name: lock ? '锁版' : '解除冻结', exact: true }).click()
    await expect(page.getByLabel('备注（选填）')).toHaveValue('')
    await page.getByLabel('备注（选填）').fill(' \n ')
    await page.getByRole('button', { name: lock ? '确认锁定' : '确认解除', exact: true }).click()
    await expect(page.getByRole('dialog')).toBeHidden()
  }
  async function create(version: string) {
    await page.goto(url(server, '/releases' + query))
    await page.getByRole('button', { name: '创建版本', exact: true }).click()
    await page.locator('#release-version').fill(version)
    const result = page.waitForResponse(response => response.url().endsWith('/api/v1/release/create') && response.request().method() === 'POST')
    await page.getByRole('button', { name: '保存版本', exact: true }).click()
    const response = await result
    expect(response.status(), '真实创建版本失败：' + await response.text()).toBe(200)
    await expect(page.getByRole('dialog')).toBeHidden()
  }
  const row = (version: string) => page.getByRole('row').filter({ has: page.getByRole('cell', { name: version, exact: true }) })
  async function activate(version: string) {
    await row(version).getByRole('button', { name: '切换生效版本', exact: true }).click()
    await page.getByRole('dialog').getByRole('button', { name: '切换生效版本', exact: true }).click()
    await expect(page.getByRole('dialog')).toBeHidden()
  }
  await freeze(true)
  await create('v1')
  await activate('v1')
  await create('v2')
  await expect(row('v1').getByRole('button', { name: '删除版本', exact: true })).toHaveCount(0)
  const before = (await getJson(server, '/api/v1/release/active?environment_key=' + scope.env + '&pipeline_key=web')).json.data.active
  const freezeURL = '/api/v1/freeze/status?environment_key=' + scope.env + '&pipeline_key=web'
  const freezeBefore = (await getJson(server, freezeURL)).json.data
  const fixed = await getJson(server, '/api/v1/runtime/release?version=v2&content_format=raw', { 'x-environment': scope.env, 'x-pipeline': 'web' })
  expect(fixed.status).toBe(200)
  const imageURL = fixed.json.data.configs.find((item: { alias: string }) => item.alias === 'CLEANUP_IMAGE').content
  // The server really commits. Only the first response is lost in transport;
  // confirmation must replay that same operation against the real backend.
  const deletionPayloads: unknown[] = []
  let committedStatus = 0
  await page.route('**/api/v1/release/delete', async route => {
    deletionPayloads.push(route.request().postDataJSON())
    if (deletionPayloads.length === 1) {
      committedStatus = (await route.fetch()).status()
      await route.abort('failed')
    } else await route.continue()
  })
  await row('v2').getByRole('button', { name: '删除版本', exact: true }).click()
  await expect(page.getByRole('dialog')).toContainText(scope.env)
  await page.getByRole('dialog').getByRole('button', { name: '删除版本', exact: true }).click()
  await expect(page.getByRole('button', { name: '确认结果', exact: true })).toBeVisible()
  expect(committedStatus).toBe(200)
  await page.getByRole('button', { name: '确认结果', exact: true }).click()
  await expect(page.getByRole('dialog')).toBeHidden()
  expect(deletionPayloads).toHaveLength(2)
  expect(deletionPayloads[1]).toEqual(deletionPayloads[0])
  await page.unroute('**/api/v1/release/delete')
  await expect(row('v2')).toHaveCount(0)
  await expect(row('v1')).toBeVisible()
  const after = (await getJson(server, '/api/v1/release/active?environment_key=' + scope.env + '&pipeline_key=web')).json.data.active
  expect(after).toEqual(before)
  expect((await getJson(server, freezeURL)).json.data).toEqual(freezeBefore)
  const history = (await getJson(server, '/api/v1/release/history?environment_key=' + scope.env + '&pipeline_key=web')).json.data.items
  expect(history.filter((event: { event_type: string; version: string }) => event.event_type === 'delete' && event.version === 'v2')).toHaveLength(1)
  await page.getByRole('button', { name: '操作记录', exact: true }).click()
  await expect(page.getByRole('dialog')).toContainText('删除版本')
  await expect(page.getByRole('dialog')).toContainText('未填写备注')
  await page.keyboard.press('Escape')
  await expect(page.getByRole('dialog')).toBeHidden()
  const anonymous = await browser.newContext()
  try {
    const base = server.origin + server.prefix
    for (const endpoint of [
      '/api/v1/runtime/release?version=v2&content_format=raw',
      '/api/v1/runtime/release?version=v2',
      '/api/v1/runtime/release/static?version=v2&expected_snapshot_id=' + fixed.json.data.snapshot_id,
    ]) {
      const response = await anonymous.request.get(base + endpoint, { headers: { 'x-environment': scope.env, 'x-pipeline': 'web' } })
      expect(response.status()).toBe(410)
    }
    expect((await anonymous.request.get(new URL(imageURL, server.origin).toString())).status()).toBe(410)
    const current = await anonymous.request.get(base + '/api/v1/runtime/release?content_format=raw', { headers: { 'x-environment': scope.env, 'x-pipeline': 'web' } })
    expect(current.status()).toBe(200)
    expect((await current.json()).data.version).toBe('v1')
  } finally { await anonymous.close() }
  // Name reuse stays forbidden after logical deletion.
  await page.getByRole('button', { name: '创建版本', exact: true }).click()
  await page.locator('#release-version').fill('v2')
  await page.getByRole('button', { name: '保存版本', exact: true }).click()
  await expect(page.getByRole('dialog')).toContainText('版本号已使用，已删版本号也不能复用')
  await page.getByRole('button', { name: '取消', exact: true }).click()
  await expect(page.getByRole('dialog')).toBeHidden()
  // A second authenticated HTTP client changes the target while the UI holds it.
  await create('v3')
  await row('v3').getByRole('button', { name: '删除版本', exact: true }).click()
  await expect(page.getByRole('dialog', { name: '删除版本 v3', exact: true })).toBeVisible()
  const switched = await postJson(server, '/api/v1/release/activate', { environment_key: scope.env, pipeline_key: 'web',
    version: 'v3', reason: '', request_id: crypto.randomUUID(), expected_active_revision: after.revision })
  expect(switched.status).toBe(200)
  await page.getByRole('dialog').getByRole('button', { name: '删除版本', exact: true }).click()
  await expect(page.getByRole('dialog')).toContainText('该版本已生效，请先切换到其他版本')
  await page.getByRole('button', { name: '取消', exact: true }).click()
  await expect(page.getByRole('dialog')).toBeHidden()
  await expect(row('v3')).toContainText('生效中')
  await freeze(false)
  expect((await getJson(server, freezeURL)).json.data.state).toBe('unlocked')
})
