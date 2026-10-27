import { test, expect } from '@playwright/test'
import { writeFile } from 'node:fs/promises'
import { join } from 'node:path'

test('真实前端发布审阅：取消保留候选、差异预览、并发冲突后重新确认', async ({ page, baseURL }) => {
  const endpoint = (path: string) => new URL('api/v1/' + path, baseURL).href
  await page.goto('releases')
  await page.getByLabel('账号', { exact: true }).fill(process.env.RB_QA_ADMIN_USERNAME!)
  await page.getByLabel('密码', { exact: true }).fill(process.env.RB_QA_ADMIN_PASSWORD!)
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await expect(page.getByRole('heading', { name: '版本发布', exact: true })).toBeVisible()
  const me = (await (await page.request.get(endpoint('auth/me'))).json()).data
  const headers = { Origin: new URL(baseURL!).origin, 'X-CSRF-Token': me.csrf_token }
  async function get(path: string) {
    const response = await page.request.get(endpoint(path))
    expect(response.status(), path).toBe(200)
    return (await response.json()).data
  }
  async function post(path: string, data: unknown) {
    const response = await page.request.post(endpoint(path), { data, headers })
    expect(response.status(), path).toBe(200)
    return (await response.json()).data
  }
  await post('environment/create', { environment: { environment_key: 'review_ui', environment_name: '发布审阅联调', is_active: true } })
  const env = (await get('runtime/overview')).list.find((item: { environment_key: string }) => item.environment_key === 'review_ui')
  const scopeId = env.pipelines.find((item: { pipeline_key: string }) => item.pipeline_key === 'default').scope_id
  const scope = { environment_key: 'review_ui', pipeline_key: 'default', expected_scope_id: scopeId }
  const query = new URLSearchParams({ environment_key: 'review_ui', pipeline_key: 'default', scope_id: scopeId }).toString()
  const upload = await page.request.post(endpoint('asset/upload'), { headers, multipart: { ...scope,
    file: { name: 'review.png', mimeType: 'image/png', buffer: Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aD1sAAAAASUVORK5CYII=', 'base64') } } })
  expect(upload.status()).toBe(200)
  const asset = (await upload.json()).data.asset.file_id
  async function create(alias: string, type: string, content: string) {
    return (await post('config/create', { expected_scope_id: scopeId, config: { ...scope, alias, name: alias, type, content } })).config
  }
  let title = await create('title', 'text', 'A')
  let color = await create('brand', 'color', '#3B82F6')
  const file = await create('guide', 'file', 'asset://' + asset)
  await create('cover', 'image', 'asset://' + asset)
  await create('intro', 'richtext', '<h2>版本审阅</h2>')
  const panel = page.getByRole('dialog', { name: '发布审阅', exact: true })
  await page.goto('releases?env=review_ui&pipeline=default')
  await page.getByRole('button', { name: '发布配置', exact: true }).click()
  await expect(panel.getByLabel('版本号', { exact: true })).toHaveCount(0)
  await panel.getByRole('button', { name: '锁版并创建候选版本', exact: true }).click()
  await expect(panel.getByLabel('差异统计')).toBeVisible()
  expect((await get('release/active?' + query)).active).toBeNull()
  expect((await get('freeze/status?' + query)).state).toBe('locked')
  await panel.getByRole('button', { name: '暂不发布', exact: true }).click()
  const v1 = (await get('release/detail?' + query + '&version=v1')).meta
  expect(v1.config_count).toBe(5)
  await page.getByRole('button', { name: '查看差异', exact: true }).click()
  for (const alias of ['brand', 'cover', 'guide', 'intro']) await panel.locator('.comparison-row').filter({ hasText: alias }).click()
  await expect(panel.getByRole('img', { name: '颜色预览 #3B82F6' })).toBeVisible()
  await expect(panel.locator('img[alt="cover"]')).toBeVisible()
  await expect(panel.getByRole('link', { name: '打开 guide' })).toBeVisible()
  await expect(panel.locator('iframe[title="intro 富文本预览"]')).toHaveAttribute('sandbox', '')
  await panel.getByRole('button', { name: '确认生效 v1', exact: true }).click()
  await expect(panel.getByRole('heading', { name: '版本 v1 已生效', exact: true })).toBeVisible()
  await panel.getByRole('button', { name: '完成', exact: true }).click()
  const firstActive = (await get('release/active?' + query)).active

  const status = await get('freeze/status?' + query)
  await post('freeze/unlock', { ...scope, expected_revision: status.revision, reason: '准备下次发布' })
  title = (await post('config/update', { expected_scope_id: scopeId, expected_edit_revision: title.edit_revision, config: { ...title, content: 'B' } })).config
  color = (await post('config/update', { expected_scope_id: scopeId, expected_edit_revision: color.edit_revision, config: { ...color, content: '#F43BF7' } })).config
  await post('config/delete', { ...scope, resource_key: file.resource_key, expected_edit_revision: file.edit_revision })
  await create('added', 'text', '新增内容')
  await page.getByRole('button', { name: '发布配置', exact: true }).click()
  await expect(panel.getByLabel('版本号', { exact: true })).toHaveCount(0)
  await panel.getByRole('button', { name: '锁版并创建候选版本', exact: true }).click()
  await expect(panel.getByLabel('差异统计')).toBeVisible()
  const summary = await panel.locator('.review-summary > div').allTextContents()
  expect(summary.map(value => value.replace(/\s/g, ''))).toEqual(['1新增', '2修改', '1删除', '2未变'])
  await panel.locator('.comparison-row').filter({ hasText: 'brand' }).click()
  await expect(panel.getByRole('img', { name: '颜色预览 #F43BF7' })).toBeVisible()
  expect((await get('release/active?' + query)).active.snapshot_id).toBe(firstActive.snapshot_id)
  // Another administrator advances the head to v3 between review and confirm.
  const locked = await get('freeze/status?' + query)
  const concurrent = await post('release/create', { ...scope, expected_freeze_revision: locked.revision, request_id: crypto.randomUUID(), reason: '' })
  expect(concurrent.meta.version).toBe('v3')
  await post('release/activate', { ...scope, version: 'v3', expected_active_revision: firstActive.revision, request_id: crypto.randomUUID(), reason: '' })
  await panel.getByRole('button', { name: '确认生效 v2', exact: true }).click()
  await expect(panel.getByLabel('差异统计')).toHaveCount(0)
  await expect(panel.getByRole('button', { name: '确认生效 v2', exact: true })).toBeDisabled()
  await panel.getByRole('button', { name: '重新比较', exact: true }).click()
  await expect(panel.getByText('配置内容相同，仍可切换到 v2。', { exact: true })).toBeVisible()
  expect((await get('release/active?' + query)).active.version).toBe('v3')
  await panel.getByRole('button', { name: '确认生效 v2', exact: true }).click()
  await expect(panel.getByRole('heading', { name: '版本 v2 已生效', exact: true })).toBeVisible()
  const finalActive = (await get('release/active?' + query)).active
  expect(finalActive.version).toBe('v2')
  expect(BigInt(finalActive.revision)).toBeGreaterThan(BigInt(firstActive.revision))
  const runtime = await page.request.get(endpoint('runtime/release?scope_id=' + scopeId), {
    headers: { 'x-environment': 'review_ui', 'x-pipeline': 'default' },
  })
  expect(runtime.status()).toBe(200)
  expect((await runtime.json()).data.configs.find((cfg: { alias: string }) => cfg.alias === 'title').content).toBe('B')
  await panel.getByRole('button', { name: '完成', exact: true }).click()
  await page.getByRole('button', { name: '版本比较', exact: true }).click()
  const fixed = page.getByRole('dialog', { name: '版本比较', exact: true })
  async function choose(label: string, version: string) {
    await fixed.getByRole('combobox', { name: label, exact: true }).click()
    await page.getByRole('option', { name: version, exact: true }).click()
  }
  await choose('基准版本', 'v1')
  const fixedResponse = page.waitForResponse(response => response.url().endsWith('/release/compare') && response.request().postDataJSON().version === 'v2')
  await choose('目标版本', 'v2')
  const fixedData = (await (await fixedResponse).json()).data
  expect(fixedData.comparison_mode).toBe('fixed')
  expect(fixedData.active_revision).toBeNull()
  expect(fixedData.target_is_active).toBeNull()
  expect(fixedData.summary).toEqual({ added: 1, modified: 2, removed: 1, unchanged: 2 })
  await expect(fixed.getByLabel('差异统计')).toBeVisible()
  const reverseResponse = page.waitForResponse(response => response.url().endsWith('/release/compare') && response.request().postDataJSON().baseline_version === 'v2' && response.request().postDataJSON().version === 'v1')
  await fixed.getByRole('button', { name: '交换比较方向', exact: true }).click()
  const reverseData = (await (await reverseResponse).json()).data
  expect(reverseData.baseline.version).toBe('v2')
  expect(reverseData.target.version).toBe('v1')
  await choose('目标版本', 'v2')
  await expect(fixed.getByText('无差异', { exact: true })).toBeVisible()
  const beforeReview = page.waitForResponse(response => response.url().endsWith('/release/compare') && response.request().postDataJSON().comparison_mode === 'active')
  await fixed.getByRole('button', { name: '审阅目标版本发布', exact: true }).click()
  expect((await (await beforeReview).json()).data.active_revision).toBe(finalActive.revision)
  await expect(panel.getByText('此版本当前已生效，无需再次切换。', { exact: true })).toBeVisible()
  await writeFile(join(process.env.RB_SCOPE_UI_INSTANCE!, 'release-review-evidence.json'), JSON.stringify({
    scope_id: scopeId, candidate_preserved_on_cancel: v1.snapshot_id, first_active: firstActive,
    second_comparison: { added: 1, modified: 2, removed: 1, unchanged: 2 },
    concurrent_version: 'v3', reconfirmed_same_content_switch: finalActive, runtime_title: 'B',
    fixed_comparison: { mode: fixedData.comparison_mode, summary: fixedData.summary, active_revision: fixedData.active_revision, target_is_active: fixedData.target_is_active },
    reverse_comparison: { baseline: reverseData.baseline.version, target: reverseData.target.version },
  }, null, 2))
})
