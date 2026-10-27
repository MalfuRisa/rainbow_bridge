import { test, expect, type Page } from '@playwright/test'
import { mkdir, writeFile } from 'node:fs/promises'
import { join } from 'node:path'
import { mockApi } from './mock'

type Dot = { x: number; y: number; radius: number }
declare global {
  interface Window { __topologyFrames: Array<{ surface: HTMLCanvasElement; frames: number; dots: Dot[] }> }
}

async function observeCanvas(page: Page) {
  await page.addInitScript(() => {
    window.__topologyFrames = []
    const clear = CanvasRenderingContext2D.prototype.clearRect
    const arc = CanvasRenderingContext2D.prototype.arc
    CanvasRenderingContext2D.prototype.clearRect = function (...args) {
      if (this.canvas.classList.contains('spectrum-topology')) {
        let record = window.__topologyFrames.find(item => item.surface === this.canvas)
        if (!record) { record = { surface: this.canvas, frames: 0, dots: [] }; window.__topologyFrames.push(record) }
        record.frames++
        record.dots = []
      }
      return clear.apply(this, args)
    }
    CanvasRenderingContext2D.prototype.arc = function (...args) {
      const record = window.__topologyFrames.find(item => item.surface === this.canvas)
      record?.dots.push({ x: args[0], y: args[1], radius: args[2] })
      return arc.apply(this, args)
    }
  })
}

const frames = (page: Page) => page.evaluate(() => window.__topologyFrames.map(item => ({
  frames: item.frames, dots: item.dots, attached: item.surface.isConnected,
})))

test('拓扑节点漂移并被指针吸引，装饰层不遮挡卡片和操作', async ({ page }) => {
  await observeCanvas(page)
  await mockApi(page, true)
  await page.clock.install()
  await page.goto('./')
  await expect(page.locator('.spectrum-topology')).toHaveCount(1)
  await page.clock.runFor(200)
  const initial = (await frames(page))[0]
  expect(initial.dots.length).toBeGreaterThanOrEqual(28)
  const node = initial.dots.find(dot => dot.x > 220 && dot.x < 1000 && dot.y > 150 && dot.y < 650)!
  expect(node).toBeTruthy()
  const pointer = { x: node.x + 64, y: node.y }
  await page.mouse.move(pointer.x, pointer.y)
  await page.clock.runFor(1800)
  const attracted = (await frames(page))[0]
  const moved = attracted.dots.find(dot => dot.radius === node.radius)!
  expect(Math.hypot(moved.x - pointer.x, moved.y - pointer.y)).toBeLessThan(50)
  expect(attracted.frames - initial.frames).toBeGreaterThan(100)
  expect(attracted.dots.some(dot => Math.hypot(dot.x - pointer.x, dot.y - pointer.y) < 1 && dot.radius === 2.2)).toBe(true)
  const canvas = page.locator('.spectrum-topology')
  await expect(canvas).toHaveAttribute('aria-hidden', 'true')
  await expect(canvas).toHaveCSS('pointer-events', 'none')
  await expect(canvas).toHaveCSS('position', 'fixed')
  await page.getByRole('link', { name: '配置管理', exact: true }).click()
  await expect(page.getByRole('heading', { name: '配置管理', exact: true })).toBeVisible()
  await page.getByRole('combobox', { name: '环境', exact: true }).click()
  await page.getByRole('option', { name: '演示环境 (qa)', exact: true }).click()
  await page.getByRole('combobox', { name: '渠道', exact: true }).click()
  await page.getByRole('option', { name: '网页渠道 (web)', exact: true }).click()
  await page.getByRole('button', { name: '新增配置', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: '新增配置', exact: true })
  await expect(dialog.getByLabel('名称 *', { exact: true })).toBeFocused()
  await dialog.getByLabel('名称 *', { exact: true }).fill('背景不影响输入')
  await expect(dialog.getByLabel('名称 *', { exact: true })).toHaveValue('背景不影响输入')
  await page.keyboard.press('Escape')
  await page.clock.runFor(300)
  await expect(dialog).toBeHidden()
  await expect(page.getByRole('button', { name: '新增配置', exact: true })).toBeFocused()
})

test('拓扑背景随动态偏好和可见性暂停，页面切换清理旧动画', async ({ page }) => {
  await observeCanvas(page)
  await mockApi(page)
  await page.emulateMedia({ reducedMotion: 'reduce' })
  await page.clock.install()
  await page.goto('./')
  await expect(page.locator('.spectrum-topology')).toHaveCount(1)
  await expect.poll(async () => (await frames(page)).length).toBe(1)
  await page.clock.runFor(100)
  const staticFrame = (await frames(page))[0]
  await page.mouse.move(24, 200)
  await page.clock.runFor(1000)
  expect((await frames(page))[0]).toEqual(staticFrame)
  await page.emulateMedia({ reducedMotion: 'no-preference' })
  await expect.poll(async () => {
    await page.clock.runFor(100)
    return (await frames(page))[0].frames
  }).toBeGreaterThan(staticFrame.frames)
  await page.evaluate(() => {
    Object.defineProperty(document, 'hidden', { configurable: true, value: true })
    document.dispatchEvent(new Event('visibilitychange'))
  })
  const hiddenFrame = (await frames(page))[0].frames
  await page.clock.runFor(1000)
  expect((await frames(page))[0].frames).toBe(hiddenFrame)
  await page.evaluate(() => {
    delete (document as unknown as { hidden?: boolean }).hidden
    document.dispatchEvent(new Event('visibilitychange'))
  })
  await page.clock.runFor(300)
  expect((await frames(page))[0].frames).toBeGreaterThan(hiddenFrame)
  await page.getByRole('link', { name: '登录', exact: true }).click()
  await expect(page.getByRole('heading', { name: '登录工作台', exact: true })).toBeVisible()
  await page.clock.runFor(100)
  const previousFrame = (await frames(page))[0].frames
  await page.clock.runFor(1000)
  const afterNavigation = await frames(page)
  expect(afterNavigation[0]).toMatchObject({ attached: false, frames: previousFrame })
  expect(afterNavigation.filter(item => item.attached)).toHaveLength(1)
  await expect(page.locator('.spectrum-topology')).toHaveCount(1)
})

test('拓扑背景适配桌面主题与尺寸，卡片文字区域保持不透明', async ({ page }) => {
  await mockApi(page)
  await page.goto('./')
  await expect(page.getByTestId('idle-row')).toBeVisible()
  await expect(page.getByRole('combobox', { name: '环境', exact: true })).toBeEnabled()
  for (const width of [1280, 1440]) {
    await page.setViewportSize({ width, height: 900 })
    for (const dark of [false, true]) {
      await page.emulateMedia({ reducedMotion: 'reduce' })
      await page.evaluate(dark => document.documentElement.classList.toggle('dark', dark), dark)
      await page.mouse.move(24, 160)
      await expect.poll(() => page.locator('.spectrum-topology').evaluate((element: HTMLCanvasElement) => element.width / Math.min(devicePixelRatio, 2))).toBe(width)
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
      // Compare the reading area inside the rounded panel, excluding transparent corners/shadow.
      const panel = page.locator('.runtime-content')
      const withBackground = await panel.screenshot({ animations: 'disabled' })
      // Keep the same compositor layer: hiding a canvas changes Chromium font antialiasing.
      // A solid contrasting canvas must still be fully hidden behind the reading surface.
      await page.locator('.spectrum-topology').evaluate((element: HTMLCanvasElement) => {
        const context = element.getContext('2d')!
        context.fillStyle = '#000000'
        context.globalAlpha = 1
        context.fillRect(0, 0, innerWidth, innerHeight)
      })
      const withoutBackground = await panel.screenshot({ animations: 'disabled' })
      if (!withoutBackground.equals(withBackground) && process.env.RB_QA_VISUAL_DIR) {
        await mkdir(process.env.RB_QA_VISUAL_DIR, { recursive: true })
        await writeFile(join(process.env.RB_QA_VISUAL_DIR, 'reading-with-background.png'), withBackground)
        await writeFile(join(process.env.RB_QA_VISUAL_DIR, 'reading-without-background.png'), withoutBackground)
      }
      expect(withoutBackground.equals(withBackground)).toBe(true)
      await page.evaluate(() => window.dispatchEvent(new Event('resize')))
      await page.emulateMedia({ reducedMotion: 'no-preference' })
      if (process.env.RB_QA_VISUAL_DIR) {
        await mkdir(process.env.RB_QA_VISUAL_DIR, { recursive: true })
        await page.evaluate(() => window.scrollTo({ top: 0, behavior: 'instant' }))
        await page.mouse.move(24, 160)
        await expect(page.locator('.spectrum-bridge-ambient-light')).toHaveCSS('opacity', '1')
        await page.waitForTimeout(800) // Capture the aggregation after its visible approach, not its initial frame.
        await page.screenshot({ path: join(process.env.RB_QA_VISUAL_DIR, `topology-home-${width}-${dark ? 'dark' : 'light'}.png`), animations: 'disabled' })
      }
    }
  }
})

test('拓扑绘制跟随浏览器刷新节奏，连续移动鼠标不跳帧', async ({ page }) => {
  await observeCanvas(page)
  await mockApi(page)
  await page.goto('./')
  await expect(page.locator('.spectrum-topology')).toHaveCount(1)
  await expect.poll(async () => (await frames(page))[0]?.frames || 0).toBeGreaterThan(3)
  const sample = page.evaluate(() => new Promise<{ refreshes: number; paints: number; elapsedMs: number; maxGap: number }>(resolve => {
    let refreshes = 0
    let paints = 0
    let gap = 0
    let maxGap = 0
    let lastPaint = window.__topologyFrames[0].frames
    const start = performance.now()
    function sampleFrame() {
      const current = window.__topologyFrames[0].frames
      refreshes++
      if (current > lastPaint) {
        paints++
        maxGap = Math.max(maxGap, gap)
        gap = 0
      } else gap++
      lastPaint = current
      if (refreshes < 90) requestAnimationFrame(sampleFrame)
      else resolve({ refreshes, paints, elapsedMs: performance.now() - start, maxGap })
    }
    requestAnimationFrame(sampleFrame)
  }))
  for (let index = 0; index < 36; index++) {
    await page.mouse.move(24 + Math.sin(index / 6) * 16, 160 + index * 8)
  }
  const result = await sample
  console.log('Topology refresh sample:', JSON.stringify(result))
  await test.info().attach('topology-refresh-sample', { body: JSON.stringify(result, null, 2), contentType: 'application/json' })
  // Compare with actual browser callbacks, not a promised hardware frame rate.
  expect(result.paints / result.refreshes).toBeGreaterThan(.95)
  expect(result.maxGap).toBeLessThanOrEqual(1)
})
