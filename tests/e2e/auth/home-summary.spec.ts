import { test, expect } from '@playwright/test'
import { mkdir } from 'node:fs/promises'
import { join } from 'node:path'
import { mockApi, ok, meta, config } from './mock'

for (const activatedAt of ['2024-01-02T03:04:05Z', undefined, 'invalid']) {
  test('首页仅显示版本、数量、范围和真实生效时间 ' + String(activatedAt), async ({ page }) => {
    const mock = await mockApi(page)
    mock.overrides.set('runtime/release', route => ok(route, {
      ...meta, version: '3', selection_mode: 'active', active_revision: '11', activated_at: activatedAt,
      environment: { environment_key: 'qa', pipeline_key: 'web' }, configs: [config],
    }))
    await page.goto('./')
    const panel = page.locator('.runtime-panel')
    await expect(panel).not.toContainText('/api/v1/runtime/config')
    await page.getByRole('combobox', { name: '环境', exact: true }).click()
    await page.getByRole('option', { name: '演示环境', exact: true }).click()
    await page.getByRole('combobox', { name: '渠道', exact: true }).click()
    await page.getByRole('option', { name: '网页渠道', exact: true }).click()
    const summary = page.locator('.runtime-summary')
    await expect(summary).toContainText('当前生效版本')
    await expect(summary.locator('strong')).toHaveText('3')
    await expect(summary).toContainText('配置 1 项')
    await expect(summary).toContainText('范围：演示环境 / 网页渠道')
    await expect(summary).not.toContainText(/revision|本次加载|按本次加载/)
    if (activatedAt === '2024-01-02T03:04:05Z') {
      await expect(summary.locator('time')).toHaveAttribute('datetime', activatedAt)
      await expect(summary.locator('time')).toContainText('2024/1/2')
      const before = await summary.locator('time').textContent()
      await page.getByRole('button', { name: '刷新', exact: true }).click()
      await expect(summary.locator('time')).toHaveText(before!)
      if (process.env.RB_QA_VISUAL_DIR) {
        await mkdir(process.env.RB_QA_VISUAL_DIR, { recursive: true })
        for (const width of [1280, 1440]) {
          await page.setViewportSize({ width, height: 900 })
          await panel.screenshot({ path: join(process.env.RB_QA_VISUAL_DIR, 'home-summary-' + width + '.png'), animations: 'disabled' })
        }
      }
    } else {
      await expect(summary).toContainText('生效时间：—')
      await expect(summary.locator('time')).toHaveCount(0)
    }
    expect(mock.count('release/active')).toBe(0)
    expect(mock.count('config/list')).toBe(0)
  })
}
