import { test, expect, type Page } from '@playwright/test'
import { mkdir } from 'node:fs/promises'
import { join } from 'node:path'
import { mockApi, ok, fail, meta, config } from './mock'

async function selectScope(page: Page) {
  await page.getByRole('combobox', { name: '环境', exact: true }).click()
  await page.getByRole('option', { name: '演示环境', exact: true }).click()
  await page.getByRole('combobox', { name: '渠道', exact: true }).click()
  await page.getByRole('option', { name: '网页渠道', exact: true }).click()
}

async function capture(page: Page, name: string) {
  if (!process.env.RB_QA_VISUAL_DIR) return
  await mkdir(process.env.RB_QA_VISUAL_DIR, { recursive: true })
  await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur())
  await page.getByRole('heading', { name: '项目概览', exact: true }).click()
  await expect(page.locator('.spectrum-skip')).not.toBeFocused()
  await page.mouse.move(0, 0)
  await page.screenshot({ path: join(process.env.RB_QA_VISUAL_DIR, name + '.png'), fullPage: true, animations: 'disabled' })
}

test('首页保留三栏介绍、真实版本和范围入口，桌面深浅主题无溢出', async ({ page }) => {
  test.setTimeout(60000)
  const mock = await mockApi(page, true)
  const imagePath = '/rainbow-bridge/api/v1/release/asset/snapshot/cover'
  mock.overrides.set('release/asset/snapshot/cover', route => route.fulfill({ contentType: 'image/svg+xml', body: '<svg xmlns="http://www.w3.org/2000/svg" width="160" height="80"><rect width="160" height="80" fill="#1b1614"/><path d="M40 70V50a40 40 0 0 1 80 0v20" fill="none" stroke="#ff7f00" stroke-width="8"/></svg>' }))
  mock.overrides.set('runtime/release', route => ok(route, {
    ...meta, version: 'v3', activated_at: '2026-10-20T05:30:09Z', selection_mode: 'active',
    environment: { environment_key: 'qa', pipeline_key: 'web' }, configs: [
      { ...config, name: '站点标题' },
      { ...config, resource_key: 'color', alias: 'brand_color', name: '品牌色', type: 'color', content: '#FF7F00' },
      { ...config, resource_key: 'object', alias: 'runtime_options', name: '服务配置', type: 'object', content: '{"enabled":true,"timeout":30}' },
      { ...config, resource_key: 'image', alias: 'cover', name: '封面图片', type: 'image', content: imagePath },
    ],
  }))
  await page.goto('./')
  await expect(page.locator('.spectrum-snapshot-label')).toHaveText('发布快照')
  await expect(page.locator('.spectrum-snapshot strong')).toHaveText('快照')
  await expect(page.locator('.runtime-summary')).toHaveCount(0)
  await expect.poll(() => page.locator('.spectrum-bridge-artwork').evaluate((img: HTMLImageElement) => img.naturalWidth)).toBeGreaterThan(0)
  for (const text of ['按范围，管理内容', '用快照，保留版本', '由你，决定何时生效']) {
    await expect(page.getByRole('heading', { name: text, exact: true })).toBeVisible()
  }
  await capture(page, 'home-idle-1440-light')
  await selectScope(page)
  await expect(page.locator('.spectrum-snapshot-label')).toHaveText('当前生效')
  await expect(page.locator('.spectrum-snapshot strong')).toHaveText('v3')
  await expect(page.getByRole('link', { name: '进入配置工作区', exact: false })).toHaveAttribute('href', '/rainbow-bridge/config?env=qa&pipeline=web')
  await expect(page.getByRole('link', { name: '前往版本发布', exact: true })).toHaveAttribute('href', '/rainbow-bridge/releases?env=qa&pipeline=web')
  await expect(page.locator('.runtime-summary')).toContainText('配置 4 项')
  await expect(page.locator('.runtime-config-row .runtime-config-value').first()).toHaveCSS('flex-direction', 'row')
  await expect(page.locator('.runtime-config-row .runtime-image-preview img')).toHaveCSS('max-height', '56px')
  const firstRow = page.locator('.runtime-config-row').first()
  for (const width of [1280, 1440]) {
    await page.setViewportSize({ width, height: 1000 })
    for (const dark of [false, true]) {
      await page.evaluate(dark => document.documentElement.classList.toggle('dark', dark), dark)
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
      const source = await page.locator('.spectrum-origin').boundingBox()
      const snapshot = await page.locator('.spectrum-snapshot').boundingBox()
      expect(source!.x + source!.width).toBeLessThan(snapshot!.x)
      const toolbarItems = await page.locator('.runtime-scope-field, .runtime-toolbar-actions').evaluateAll(elements => elements.map(element => Math.round(element.getBoundingClientRect().top)))
      expect(new Set(toolbarItems).size).toBe(1)
      await firstRow.hover()
      await expect.poll(() => firstRow.locator('td').first().evaluate(el => getComputedStyle(el).boxShadow)).toContain('inset')
      await page.mouse.move(0, 0)
      await capture(page, 'home-ready-' + width + (dark ? '-dark' : '-light'))
    }
  }
  expect(mock.count('config/list')).toBe(0)
  expect(mock.count('release/list')).toBe(0)
})

test('收起介绍可记住选择、移除隐藏区焦点，减少动态效果仍能展开', async ({ page }) => {
  await mockApi(page)
  await page.emulateMedia({ reducedMotion: 'reduce' })
  await page.goto('./')
  await expect(page.locator('.spectrum-bridge-artwork')).toHaveCSS('animation-name', 'none')
  await page.getByRole('button', { name: '收起介绍', exact: true }).click()
  await expect(page.locator('#spectrum-introduction')).toHaveAttribute('inert', '')
  await expect(page.getByRole('heading', { name: /每一次配置/ })).toBeHidden()
  await expect(page.getByRole('button', { name: '展开产品介绍', exact: true })).toBeFocused()
  await page.reload()
  await expect(page.getByRole('button', { name: '展开产品介绍', exact: true })).toHaveAttribute('aria-expanded', 'false')
  await page.getByRole('button', { name: '展开产品介绍', exact: true }).click()
  await expect(page.getByRole('heading', { name: /每一次配置/ })).toBeVisible()
  await expect(page.locator('#spectrum-introduction')).not.toHaveAttribute('inert')
  await expect(page.locator('#spectrum-introduction')).toHaveCSS('transition-duration', '0s')
  await page.getByText('前端对接与 API 说明', { exact: true }).click()
  await expect(page.getByText('/rainbow-bridge/api/v1/runtime/release', { exact: true })).toBeVisible()
})

test('封面加载失败不影响操作，读取失败清除封面和工作区中的旧生效版本', async ({ page }) => {
  const mock = await mockApi(page)
  await page.route('**/spectrum-bridge*.webp', route => route.abort())
  await page.goto('./')
  await expect(page.locator('.spectrum-bridge-artwork')).toHaveCount(0)
  await expect(page.getByRole('link', { name: '进入配置工作区', exact: false })).toBeVisible()
  await selectScope(page)
  await expect(page.locator('.spectrum-snapshot strong')).toHaveText('v1')
  mock.overrides.set('runtime/release', route => fail(route, 503, 'RELEASE_UNAVAILABLE'))
  await page.getByRole('button', { name: '刷新', exact: true }).click()
  await expect(page.getByTestId('error-row')).toBeVisible()
  await expect(page.locator('.runtime-summary')).toHaveCount(0)
  await expect(page.locator('.spectrum-snapshot-label')).toHaveText('发布快照')
  await expect(page.locator('.spectrum-snapshot strong')).toHaveText('快照')
  await expect(page.getByRole('button', { name: /导出此版本静态包/ })).toBeDisabled()
})
