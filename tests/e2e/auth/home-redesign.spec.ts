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
  await expect(page.locator('.spectrum-bridge-lighting')).toBeHidden()
  await expect(page.locator('.spectrum-bridge-artwork')).toHaveCSS('filter', 'brightness(0.9)')
  await expect(page.locator('.spectrum-bridge-artwork')).toHaveCSS('opacity', '1')
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
  await page.getByText('API 对接能力', { exact: true }).click()
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

test('桥脚沿拱向上点亮、倒影同步，900ms 内完成且入口无需等待', async ({ page }) => {
  await mockApi(page, true)
  await page.goto('./')
  const scenery = page.locator('.spectrum-bridge-scenery')
  await expect(scenery).toHaveClass(/is-ready/)
  const entrance = page.getByRole('link', { name: '进入配置工作区', exact: false })
  const originalBounds = await entrance.boundingBox()
  for (const width of [1280, 1440]) {
    await page.setViewportSize({ width, height: 900 })
    for (const dark of [false, true]) {
      await page.evaluate(dark => document.documentElement.classList.toggle('dark', dark), dark)
      let previousOffset = 1
      for (const time of [0, 180, 360, 600, 900]) {
        const light = await scenery.evaluate((element, time) => {
          const animations = element.getAnimations({ subtree: true })
          for (const animation of animations) { animation.pause(); animation.currentTime = time }
          const paths = [...element.querySelectorAll<SVGPathElement>('.spectrum-bridge-light-path')]
          const offsets = paths.map(path => parseFloat(getComputedStyle(path).strokeDashoffset))
          const tip = paths[0].getPointAtLength(paths[0].getTotalLength() * (1 - offsets[0]))
          return { offsets, tipY: tip.y,
            ambient: Number(getComputedStyle(element.querySelector('.spectrum-bridge-ambient-light')!).opacity),
            durations: animations.map(animation => animation.effect!.getTiming().duration) }
        }, time)
        expect(light.durations).toHaveLength(5)
        expect(light.durations.every(duration => duration === 900)).toBe(true)
        expect(light.offsets).toHaveLength(4)
        expect(new Set(light.offsets).size).toBe(1) // Two arches and two water reflections share progress.
        expect(light.offsets[0]).toBeLessThanOrEqual(previousOffset)
        previousOffset = light.offsets[0]
        if (time === 0) { expect(light.offsets[0]).toBe(1); expect(light.tipY).toBeCloseTo(342); expect(light.ambient).toBe(0) }
        if (time === 180 || time === 360) {
          expect(light.tipY).toBeGreaterThan(140)
          expect(light.tipY).toBeLessThan(342)
          expect(light.ambient).toBe(0) // No overall fade before the upward light has progressed.
        }
        if (time === 900) { expect(light.offsets[0]).toBe(0); expect(light.tipY).toBeCloseTo(140); expect(light.ambient).toBe(1) }
        await expect(entrance).toBeVisible()
        if (process.env.RB_QA_VISUAL_DIR) {
          await mkdir(process.env.RB_QA_VISUAL_DIR, { recursive: true })
          await page.locator('.spectrum-hero').screenshot({ path: join(process.env.RB_QA_VISUAL_DIR, `bridge-light-${width}-${dark ? 'dark' : 'light'}-${time}.png`), animations: 'allow' })
        }
      }
    }
  }
  await page.setViewportSize({ width: 1440, height: 900 })
  expect(await entrance.boundingBox()).toEqual(originalBounds)
  // Rewind to the fully dimmed frame: navigation is already interactive.
  await scenery.evaluate(element => { for (const animation of element.getAnimations({ subtree: true })) animation.currentTime = 0 })
  await entrance.click()
  await expect(page.getByRole('heading', { name: '配置管理', exact: true })).toBeVisible()
})

test('API 能力在未登录未选范围时可读，完整路径可复制且无调用副作用', async ({ page, context, baseURL }) => {
  await context.grantPermissions(['clipboard-read', 'clipboard-write'])
  const mock = await mockApi(page)
  await page.emulateMedia({ reducedMotion: 'reduce' })
  await page.goto('./')
  const summary = page.locator('summary').filter({ hasText: 'API 对接能力' })
  const panel = page.locator('.spectrum-api-panel')
  const documentLink = panel.getByRole('link', { name: '查看完整 API' })
  await expect(panel).toBeHidden()
  await summary.focus()
  await page.keyboard.press('Enter')
  await expect(panel).toBeVisible()
  await expect(panel.getByText('业务系统可通过 API 获取配置、指定版本或下载资源包。', { exact: true })).toBeVisible()
  const prefix = new URL(baseURL!).pathname.replace(/\/$/, '')
  const capabilities = [
    ['读取生效配置', '获取当前环境与渠道的生效配置，跟随版本切换更新。', '/api/v1/runtime/release'],
    ['读取指定版本', '按版本号获取对应配置，其他版本的生效切换不会改变读取目标。', '/api/v1/runtime/release?version=<版本号>'],
    ['导出版本资源包', '下载指定版本的配置与配套资源，打包为 ZIP 文件。', '/api/v1/runtime/release/static?version=<版本号>&expected_snapshot_id=<UUID>'],
  ]
  const rows = panel.locator('.spectrum-api-row')
  await expect(rows).toHaveCount(3)
  await expect(panel).not.toContainText('content_format=raw')
  await expect(panel).not.toContainText('x-environment')
  await expect(panel.getByRole('textbox')).toHaveCount(0)
  await expect(documentLink).toHaveAttribute('href', 'https://github.com/MalfuRisa/rainbow_bridge/blob/main_v1/docs/docs/api/index.md')
  await expect(documentLink).toHaveAttribute('target', '_blank')
  await expect(documentLink).toHaveAttribute('rel', 'noopener noreferrer')
  await page.keyboard.press('Tab')
  await expect(documentLink).toBeFocused()
  for (const [index, [title, description, path]] of capabilities.entries()) {
    const row = rows.nth(index)
    await expect(row.getByRole('heading')).toHaveText(title)
    await expect(row.getByText(description, { exact: true })).toBeVisible()
    await expect(row.locator('code')).toHaveText(prefix + path)
    await page.keyboard.press('Tab')
    await expect(row.getByRole('button', { name: `复制接口路径 ${title}`, exact: true })).toBeFocused()
    await page.keyboard.press('Enter')
    await expect(page.getByText('已复制接口路径', { exact: true }).first()).toBeVisible()
    expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(prefix + path)
  }
  for (const width of [1280, 1440]) {
    await page.setViewportSize({ width, height: 900 })
    for (const dark of [false, true]) {
      await page.evaluate(dark => document.documentElement.classList.toggle('dark', dark), dark)
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
      await expect(documentLink).toBeVisible()
      await expect(rows.last().getByRole('button')).toBeVisible()
      if (process.env.RB_QA_VISUAL_DIR) {
        await mkdir(process.env.RB_QA_VISUAL_DIR, { recursive: true })
        await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur())
        await page.mouse.move(0, 0)
        await summary.locator('..').screenshot({ path: join(process.env.RB_QA_VISUAL_DIR, `api-capabilities-${prefix ? 'prefix' : 'root'}-${width}-${dark ? 'dark' : 'light'}.png`), animations: 'disabled' })
      }
    }
  }
  await summary.focus()
  await page.keyboard.press('Space')
  await expect(panel).toBeHidden()
  await page.keyboard.press('Tab')
  await expect(page.locator('summary').filter({ hasText: '关于虹桥计划' })).toBeFocused()
  const allowedReads = new Set(['auth/me', 'runtime/overview', 'version', 'version/latest'])
  expect(mock.requests.every(request => request.method() === 'GET' && allowedReads.has(new URL(request.url()).pathname.split('/api/v1/')[1]))).toBe(true)
})

test('API 能力复制被拒绝时提示手动复制，完整路径仍可选择', async ({ page }) => {
  await mockApi(page)
  await page.addInitScript(() => {
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: {
      writeText: async () => { throw new DOMException('Clipboard blocked', 'NotAllowedError') },
    } })
  })
  await page.goto('./')
  await page.locator('summary').filter({ hasText: 'API 对接能力' }).click()
  const row = page.locator('.spectrum-api-row').last()
  await row.getByRole('button', { name: '复制接口路径 导出版本资源包', exact: true }).click()
  await expect(page.getByText('复制失败，请手动选择路径复制', { exact: true })).toBeVisible()
  expect(await row.locator('code').evaluate(element => {
    const range = document.createRange()
    range.selectNodeContents(element)
    const selection = window.getSelection()!
    selection.removeAllRanges()
    selection.addRange(range)
    return selection.toString()
  })).toContain('?version=<版本号>&expected_snapshot_id=<UUID>')
  await expect(row.getByRole('button')).toBeEnabled()
})
