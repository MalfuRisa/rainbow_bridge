import { test, expect } from '@playwright/test'
import { targetA, url } from './support/env'
import { ensureScope, createConfig, listConfigs, type Scope } from './support/scopes'
import { gotoPage, openEditDialog } from './support/pages'

const scope: Scope = { env: 'qa_spectrum', pipeline: 'main', label: 'qa_spectrum/main', sentinel: 'SPECTRUM' }
const original = '{"id":900719925474099312345,"enabled":true,"nested":{"value":"原文"}}'

test.describe('光谱工作台 @spectrum @smoke', () => {
  test('首页介绍可收起并记住选择，API 说明仍可展开', async ({ page }) => {
    await gotoPage(page, targetA(), '/')
    const toggle = page.getByRole('button', { name: '收起介绍' })
    await toggle.click()
    await expect(page.getByRole('heading', { name: /每一次配置/ })).toBeHidden()
    await page.reload()
    await expect(page.getByRole('button', { name: '展开产品介绍' })).toHaveAttribute('aria-expanded', 'false')
    await expect(page.getByRole('heading', { name: '当前生效配置', exact: true })).toBeVisible()
    await page.getByText('前端对接与 API 说明', { exact: true }).click()
    await expect(page.getByText('/rainbow-bridge/api/v1/runtime/release', { exact: true })).toBeVisible()
  })

  test('目录搜索、原文详情和单层编辑对话框保留配置精度', async ({ page }) => {
    const target = targetA()
    await ensureScope(target, scope)
    await createConfig(target, scope, { alias: 'SPECTRUM_ORIGINAL', name: '原文精度', type: 'json', content: original })
    const stored = (await listConfigs(target, scope)).find(item => item.alias === 'SPECTRUM_ORIGINAL')!.content
    expect(stored).toContain('900719925474099312345')
    await gotoPage(page, target, '/config?env=qa_spectrum&pipeline=main')
    const search = page.getByRole('textbox', { name: '搜索配置名称或别名' })
    await search.fill('spectrum_original')
    const row = page.locator('tr').filter({ hasText: 'SPECTRUM_ORIGINAL' })
    await expect(row).toBeVisible()
    await expect(page.locator('.config-detail pre')).toHaveText(stored)
    const dialog = await openEditDialog(page, 'SPECTRUM_ORIGINAL')
    await expect(page.getByRole('dialog')).toHaveCount(1)
    await expect(dialog.locator('#alias')).toBeDisabled()
    await expect(dialog.locator('#content')).toHaveValue(stored)
    await dialog.getByRole('button', { name: '取消', exact: true }).click()
    await search.fill('不存在的名称')
    await expect(page.getByText('未找到匹配的配置', { exact: true })).toBeVisible()
    await page.getByRole('button', { name: '清空搜索' }).click()
    await expect(row).toBeVisible()
  })

  test('删除须确认，取消保留数据，确认后同步目录和详情', async ({ page }) => {
    const target = targetA()
    await ensureScope(target, scope)
    await createConfig(target, scope, { alias: 'SPECTRUM_DELETE', name: '删除确认样本', type: 'text', content: 'disposable QA fixture' })
    await gotoPage(page, target, '/config?env=qa_spectrum&pipeline=main')
    const row = page.locator('tr').filter({ hasText: 'SPECTRUM_DELETE' })
    await row.getByRole('button', { name: '删除 删除确认样本', exact: true }).click()
    await page.getByRole('dialog').getByRole('button', { name: '取消', exact: true }).click()
    expect((await listConfigs(target, scope)).some(item => item.alias === 'SPECTRUM_DELETE')).toBe(true)
    await row.getByRole('button', { name: '删除 删除确认样本', exact: true }).click()
    await page.getByRole('dialog').getByRole('button', { name: '确认删除', exact: true }).click()
    await expect(row).toHaveCount(0)
    expect((await listConfigs(target, scope)).some(item => item.alias === 'SPECTRUM_DELETE')).toBe(false)
  })

  test('375px 导航与配置详情不溢出，全站使用统一主题', async ({ page }) => {
    const target = targetA()
    await gotoPage(page, target, '/environments')
    await expect(page.getByRole('heading', { name: '环境管理', exact: true })).toBeVisible()
    const originalPrimary = await page.evaluate(() => getComputedStyle(document.documentElement).getPropertyValue('--primary').trim())
    await page.setViewportSize({ width: 375, height: 844 })
    await gotoPage(page, target, '/')
    await expect(page.getByRole('heading', { name: '项目概览', exact: true })).toBeVisible()
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
    await page.getByRole('button', { name: '展开导航' }).click()
    const nav = page.getByRole('navigation', { name: '主要导航' })
    await expect(nav.getByRole('link')).toHaveCount(7)
    await nav.getByRole('link', { name: '配置管理', exact: true }).click()
    await expect(page.getByRole('heading', { name: '配置管理', exact: true })).toBeVisible()
    await page.goto(url(target, '/config?env=qa_spectrum&pipeline=main'))
    await expect(page.getByRole('button', { name: '新增配置' })).toBeEnabled()
    await expect(page.locator('.config-detail')).toBeVisible()
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
    await page.getByRole('button', { name: '展开导航' }).click()
    await nav.getByRole('link', { name: '环境管理', exact: true }).click()
    await expect(page.getByRole('heading', { name: '环境管理', exact: true })).toBeVisible()
    await expect(page.locator('.spectrum-shell')).toHaveCount(1)
    await page.getByRole('button', { name: '展开导航', exact: true }).click()
    await expect(nav.getByRole('link', { name: '环境管理', exact: true })).toHaveAttribute('aria-current', 'page')
    expect(await page.evaluate(() => getComputedStyle(document.documentElement).getPropertyValue('--primary').trim())).toBe(originalPrimary)
  })

  test('切换范围后迟到的旧列表不能覆盖当前目录', async ({ page }) => {
    const target = targetA()
    const first: Scope = { env: 'qa_spectrum_requests', pipeline: 'main', label: 'qa_spectrum_requests/main', sentinel: 'FIRST_SCOPE' }
    const second: Scope = { ...first, pipeline: 'beta', label: 'qa_spectrum_requests/beta', sentinel: 'SECOND_SCOPE' }
    await ensureScope(target, first)
    await ensureScope(target, second)
    await createConfig(target, first, { alias: 'SPECTRUM_OLD', name: '旧范围配置', type: 'text', content: 'old scope' })
    await createConfig(target, second, { alias: 'SPECTRUM_NEW', name: '新范围配置', type: 'text', content: 'new scope' })
    let releaseOld!: () => void
    const gate = new Promise<void>(resolve => { releaseOld = resolve })
    let held = false
    let delivered = false
    await page.route('**/api/v1/config/list?**', async route => {
      const query = new URL(route.request().url()).searchParams
      if (query.get('environment_key') !== first.env || query.get('pipeline_key') !== first.pipeline) return route.continue()
      const response = await route.fetch()
      held = true
      await gate
      await route.fulfill({ response })
      delivered = true
    })
    await gotoPage(page, target, `/config?env=${first.env}&pipeline=main`)
    await expect.poll(() => held).toBe(true)
    await page.getByRole('combobox', { name: '渠道', exact: true }).click()
    await page.getByRole('option', { name: 'QA beta (beta)', exact: true }).click()
    const rows = page.locator('.config-catalog-table')
    await expect(rows).toContainText('SPECTRUM_NEW')
    releaseOld()
    await expect.poll(() => delivered).toBe(true)
    await expect(rows).toContainText('SPECTRUM_NEW')
    await expect(rows).not.toContainText('SPECTRUM_OLD')
    await expect(page.locator('.config-detail pre')).toHaveText('new scope')
  })

  test('列表加载失败可重试恢复，失败时不显示旧内容', async ({ page }) => {
    const target = targetA()
    await ensureScope(target, scope)
    await createConfig(target, scope, { alias: 'SPECTRUM_RETRY', name: '加载恢复样本', type: 'text', content: 'retry recovered' })
    await page.route('**/api/v1/config/list?**', route => route.fulfill({ status: 503, contentType: 'application/json', body: JSON.stringify({ code: 503, message: 'QA temporary unavailable' }) }), { times: 1 })
    await gotoPage(page, target, '/config?env=qa_spectrum&pipeline=main')
    await expect(page.getByRole('heading', { name: '配置加载失败', exact: true })).toBeVisible()
    await expect(page.locator('.config-detail')).toHaveCount(0)
    await page.getByRole('button', { name: '重试', exact: true }).click()
    await expect(page.locator('tr').filter({ hasText: 'SPECTRUM_RETRY' })).toBeVisible()
    await expect(page.getByRole('heading', { name: '配置加载失败', exact: true })).toHaveCount(0)
  })
})
