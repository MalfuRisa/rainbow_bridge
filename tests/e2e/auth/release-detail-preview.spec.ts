import { test, expect, type Page } from '@playwright/test'
import { mkdir } from 'node:fs/promises'
import { join } from 'node:path'
import { fixture, row, ok, fail } from '../local/release/cleanup-fixture'
import { config, meta } from './mock'

const imagePath = '/rainbow-bridge/api/v1/release/asset/published-snapshot/cover'
const filePath = '/rainbow-bridge/api/v1/release/asset/published-snapshot/manual'
const richText = '<h2>版本说明</h2><p><strong>已验收</strong></p><script>parent.document.body.dataset.previewExecuted="yes"</script>'
const artwork = '<svg xmlns="http://www.w3.org/2000/svg" width="320" height="160"><rect width="320" height="160" fill="#1c1917"/><path d="M80 140V100a80 80 0 0 1 160 0v40" fill="none" stroke="#ff8500" stroke-width="20"/></svg>'
const values = [
  { alias: 'blue', name: '品牌色', type: 'color', content: '#3B82F6' },
  { alias: 'pic', name: '封面', type: 'image', content: imagePath },
  { alias: 'manual', name: '使用指南', type: 'file', content: filePath },
  { alias: 'notes', name: '发布说明', type: 'richtext', content: richText },
  { alias: 'object', name: '原始对象', type: 'object', content: '{\n  "id": 900719925474099312345\n}' },
]
const card = (page: Page, alias: string) => page.locator('.spectrum-release-config').filter({ has: page.getByRole('button', { name: `复制 ${alias} 原文`, exact: true }) })

async function openDetail(page: Page) {
  const { mock } = await fixture(page)
  mock.overrides.set('release/detail', route => ok(route, {
    meta: { ...meta, version: 'v2', snapshot_id: 'published-snapshot', config_count: values.length, asset_count: 2 },
    configs: values.map(value => ({ ...config, ...value, resource_key: value.alias })), assets: [],
  }))
  mock.overrides.set('release/asset/published-snapshot/cover', route => route.fulfill({ contentType: 'image/svg+xml', body: artwork }))
  await row(page).getByRole('button', { name: '查看', exact: true }).click()
  await expect(card(page, 'blue')).toBeVisible()
  return mock
}

test('版本详情复用类型预览与原文复制，图片大图关闭后返回详情', async ({ page, context }) => {
  await context.grantPermissions(['clipboard-read', 'clipboard-write'])
  const mock = await openDetail(page)
  await expect(card(page, 'blue').getByRole('img')).toHaveCSS('background-color', 'rgb(59, 130, 246)')
  await expect.poll(() => card(page, 'pic').getByRole('img').evaluate((image: HTMLImageElement) => image.naturalWidth)).toBe(320)
  await expect(card(page, 'manual').getByRole('link', { name: '打开 使用指南' })).toHaveAttribute('href', filePath)
  await expect(card(page, 'notes').locator('iframe')).toHaveAttribute('sandbox', '')
  await expect(card(page, 'notes').locator('iframe').contentFrame().getByRole('heading', { name: '版本说明' })).toBeVisible()
  expect(await page.evaluate(() => document.body.dataset.previewExecuted)).toBeUndefined()
  if (process.env.RB_QA_VISUAL_DIR) {
    await mkdir(process.env.RB_QA_VISUAL_DIR, { recursive: true })
    for (const width of [1280, 1440]) {
      await page.setViewportSize({ width, height: 1100 })
      await page.evaluate(dark => document.documentElement.classList.toggle('dark', dark), width === 1280)
      const dialog = page.locator('.spectrum-release-detail')
      expect(await dialog.evaluate(element => element.scrollWidth <= element.clientWidth)).toBe(true)
      await dialog.screenshot({ path: join(process.env.RB_QA_VISUAL_DIR, 'release-detail-' + width + '.png'), animations: 'disabled' })
    }
  }
  for (const value of values) {
    const current = card(page, value.alias)
    if (value.type !== 'object') await current.getByRole('button', { name: `查看 ${value.name} 原文`, exact: true }).click()
    expect(await current.locator('pre').textContent()).toBe(value.content)
    await current.getByRole('button', { name: `复制 ${value.alias} 原文`, exact: true }).click()
    expect((await page.evaluate(() => navigator.clipboard.readText())).replace(/\r\n/g, '\n')).toBe(value.content)
    if (value.type !== 'object') await current.getByRole('button', { name: `查看 ${value.name} 预览`, exact: true }).click()
  }
  await card(page, 'pic').getByRole('button', { name: '查看 封面 大图', exact: true }).click()
  const imageDialog = page.locator('.spectrum-image-dialog')
  await expect(imageDialog).toBeVisible()
  await expect(imageDialog.getByRole('img')).toHaveAttribute('src', imagePath)
  await expect.poll(() => imageDialog.getByRole('img').evaluate((image: HTMLImageElement) => image.naturalWidth)).toBe(320)
  await page.keyboard.press('Escape')
  await expect(imageDialog).toBeHidden()
  await expect(page.locator('.spectrum-release-detail')).toBeVisible()
  await expect(card(page, 'pic').getByRole('button', { name: '查看 封面 大图', exact: true })).toBeFocused()
  expect(mock.count('config/list')).toBe(0)
  expect(mock.requests.some(request => request.url().includes('/asset/file/'))).toBe(false)
})

test('详情刷新后重置原文状态，版本删除后不保留预览和复制入口', async ({ page }) => {
  const mock = await openDetail(page)
  await card(page, 'blue').getByRole('button', { name: '查看 品牌色 原文', exact: true }).click()
  await page.getByRole('button', { name: '刷新详情', exact: true }).click()
  await expect(card(page, 'blue').getByRole('img')).toHaveCSS('background-color', 'rgb(59, 130, 246)')
  await expect(card(page, 'blue').locator('pre')).toHaveCount(0)
  mock.overrides.set('release/detail', route => fail(route, 410, 'RELEASE_VERSION_DELETED'))
  await page.getByRole('button', { name: '刷新详情', exact: true }).click()
  await expect(page.locator('.spectrum-release-detail').getByRole('alert')).toHaveText('该版本已被删除')
  await expect(page.locator('.spectrum-release-config')).toHaveCount(0)
  await expect(page.locator('.spectrum-image-dialog')).toHaveCount(0)
})
