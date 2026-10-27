import { test, expect, type Page } from '@playwright/test'
import { mkdir } from 'node:fs/promises'
import { join } from 'node:path'
import { mockApi, ok, meta, config } from './mock'

// Published resource URL includes the deployment prefix and snapshot identity.
const imagePath = '/rainbow-bridge/api/v1/release/asset/published-snapshot/banner'
const artwork = '<svg xmlns="http://www.w3.org/2000/svg" width="320" height="160" viewBox="0 0 320 160"><rect width="320" height="160" fill="#1c1917"/><path d="M80 140V100a80 80 0 0 1 160 0v40" fill="none" stroke="#ff8500" stroke-width="20"/></svg>'
const originalJSON = '{"id":900719925474099312345}'

async function selectScope(page: Page) {
  await page.goto('./')
  await page.getByRole('combobox', { name: '环境', exact: true }).click()
  await page.getByRole('option', { name: '演示环境', exact: true }).click()
  await page.getByRole('combobox', { name: '渠道', exact: true }).click()
  await page.getByRole('option', { name: '网页渠道', exact: true }).click()
}

async function setup(page: Page) {
  const mock = await mockApi(page)
  mock.overrides.set('runtime/release', route => ok(route, {
    ...meta, selection_mode: 'active', environment: { environment_key: 'qa', pipeline_key: 'web' },
    configs: [
      { ...config, resource_key: 'image', alias: 'pic', name: '发布图片', type: 'image', content: imagePath },
      { ...config, resource_key: 'json', alias: 'json', type: 'json', content: originalJSON },
    ],
  }))
  return mock
}

test('首页图片使用发布资源显示缩略图与大图，复制仍是原文且JSON精度不变', async ({ page, context }) => {
  await context.grantPermissions(['clipboard-read', 'clipboard-write'])
  const mock = await setup(page)
  mock.overrides.set('release/asset/published-snapshot/banner', route => route.fulfill({ contentType: 'image/svg+xml', body: artwork }))
  await selectScope(page)
  const panel = page.locator('.runtime-panel')
  const thumbnail = panel.getByRole('img', { name: '发布图片', exact: true })
  await expect(thumbnail).toHaveAttribute('src', imagePath)
  await expect.poll(() => thumbnail.evaluate((image: HTMLImageElement) => image.naturalWidth)).toBe(320)
  await expect(panel.locator('.runtime-raw-content').filter({ hasText: originalJSON })).toHaveText(originalJSON)
  await panel.getByRole('button', { name: '复制 pic 原文', exact: true }).click()
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(imagePath)
  await panel.getByRole('button', { name: '查看 发布图片 原文', exact: true }).click()
  expect(await panel.locator('pre').filter({ hasText: imagePath }).textContent()).toBe(imagePath)
  await expect(thumbnail).toHaveCount(0)
  await panel.getByRole('button', { name: '查看 发布图片 预览', exact: true }).click()
  await panel.getByRole('button', { name: '查看 发布图片 大图', exact: true }).click()
  const dialog = page.getByRole('dialog')
  await expect(dialog.getByRole('img')).toHaveAttribute('src', imagePath)
  await expect.poll(() => dialog.getByRole('img').evaluate((image: HTMLImageElement) => image.naturalWidth)).toBe(320)
  await page.keyboard.press('Escape')
  await expect(dialog).toBeHidden()
  if (process.env.RB_QA_VISUAL_DIR) {
    await mkdir(process.env.RB_QA_VISUAL_DIR, { recursive: true })
    for (const width of [1280, 1440]) {
      await page.setViewportSize({ width, height: 900 })
      await panel.screenshot({ path: join(process.env.RB_QA_VISUAL_DIR, 'home-image-' + width + '.png'), animations: 'disabled' })
    }
  }
  expect(mock.count('asset/list')).toBe(0)
  expect(mock.count('config/list')).toBe(0)
  expect(mock.requests.some(request => new URL(request.url()).pathname.includes('/asset/file/'))).toBe(false)
})

test('图片加载失败可重试，刷新后改用新版本资源', async ({ page }) => {
  const mock = await setup(page)
  let missing = true
  mock.overrides.set('release/asset/published-snapshot/banner', route => missing
    ? route.fulfill({ status: 404, body: 'missing' })
    : route.fulfill({ contentType: 'image/svg+xml', body: artwork }))
  await selectScope(page)
  const panel = page.locator('.runtime-panel')
  await expect(panel.getByText('图片加载失败，点击重试', { exact: true })).toBeVisible()
  missing = false
  await panel.getByRole('button', { name: '查看 发布图片 大图', exact: true }).click()
  await expect.poll(() => page.getByRole('dialog').getByRole('img').evaluate((image: HTMLImageElement) => image.naturalWidth)).toBe(320)
  await page.keyboard.press('Escape')
  await expect.poll(() => panel.getByRole('img').evaluate((image: HTMLImageElement) => image.naturalWidth)).toBe(320)
  const nextImagePath = imagePath.replace('published-snapshot', 'next-snapshot')
  mock.overrides.set('release/asset/next-snapshot/banner', route => route.fulfill({ contentType: 'image/svg+xml', body: artwork }))
  mock.overrides.set('runtime/release', route => ok(route, {
    ...meta, snapshot_id: 'next-snapshot', version: 'v2', selection_mode: 'active',
    environment: { environment_key: 'qa', pipeline_key: 'web' },
    configs: [{ ...config, resource_key: 'image', alias: 'pic', name: '发布图片', type: 'image', content: nextImagePath }],
  }))
  await panel.getByRole('button', { name: '刷新', exact: true }).click()
  await expect(panel.getByRole('img')).toHaveAttribute('src', nextImagePath)
  await expect.poll(() => panel.getByRole('img').evaluate((image: HTMLImageElement) => image.naturalWidth)).toBe(320)
  await expect(panel.locator('.runtime-summary-version strong')).toHaveText('v2')
})
