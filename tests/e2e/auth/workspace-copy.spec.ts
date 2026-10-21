import { test, expect, type Page } from '@playwright/test'
import { mkdir } from 'node:fs/promises'
import { join } from 'node:path'
import { mockApi, ok, config, meta } from './mock'
import { fixture, row } from '../local/release/cleanup-fixture'

async function capture(page: Page, name: string) {
  if (!process.env.RB_QA_VISUAL_DIR) return
  await mkdir(process.env.RB_QA_VISUAL_DIR, { recursive: true })
  await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur())
  await page.screenshot({ path: join(process.env.RB_QA_VISUAL_DIR, name + '.png'), fullPage: true, animations: 'disabled' })
}

test('发布接入与校验信息按需展开，版本内容和操作仍可访问', async ({ page }) => {
  const { mock } = await fixture(page)
  mock.overrides.set('release/detail', route => ok(route, {
    meta: { ...meta, version: 'v2', payload_sha256: 'content-checksum' }, configs: [config], assets: [],
  }))
  const help = page.locator('.spectrum-help')
  await expect(help).not.toHaveAttribute('open', '')
  await expect(page.locator('.spectrum-release-current')).toContainText('v1')
  await capture(page, 'releases-1440-light')
  await help.locator('summary').click()
  await expect(help.getByText('跟随生效版本', { exact: true })).toBeVisible()
  await expect(help.getByText('固定某版本(不跟随切换)', { exact: true })).toBeVisible()
  await row(page).getByRole('button', { name: '查看', exact: true }).click()
  const dialog = page.getByRole('dialog')
  await expect(dialog).toContainText('光谱标题')
  await expect(dialog.getByText('content-checksum', { exact: true })).toBeHidden()
  await dialog.getByText('技术信息', { exact: true }).click()
  await expect(dialog.getByText('content-checksum', { exact: true })).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(row(page).getByRole('button', { name: '查看', exact: true })).toBeFocused()
  await help.locator('summary').click()
  await page.setViewportSize({ width: 1280, height: 900 })
  await page.evaluate(() => document.documentElement.classList.add('dark'))
  await capture(page, 'releases-1280-dark')
})

for (const overwrite of [false, true]) {
  test('导入冲突说明与实际提交保持一致：' + (overwrite ? '覆盖' : '跳过'), async ({ page }) => {
    const mock = await mockApi(page, true)
    mock.overrides.set('transfer/import-preview', route => ok(route, {
      format: 'zip', environments: [{ environment_key: 'qa', environment_name: '演示环境', status: 'exists', pipelines: [{
        pipeline_key: 'web', pipeline_name: '网页渠道', status: 'exists', configs: [{ ...config, status: 'conflict' }],
      }] }],
      summary: { total_environments: 1, total_pipelines: 1, total_configs: 1, total_assets: 0, new_count: 0, existing_count: 0, conflict_count: 1 },
    }))
    await page.goto('import-export')
    await page.getByRole('tab', { name: '数据导入', exact: true }).click()
    await page.locator('#import-file').setInputFiles({ name: 'fixture.zip', mimeType: 'application/zip', buffer: Buffer.from('mock preview') })
    await expect(page.getByText('文件内配置', { exact: true })).toBeVisible()
    if (overwrite) await page.getByRole('checkbox', { name: '覆盖现有冲突数据', exact: true }).check()
    await expect(page.getByText(overwrite ? '覆盖冲突' : '跳过冲突', { exact: true })).toBeVisible()
    await page.getByRole('button', { name: '开始导入', exact: true }).click()
    if (!overwrite) {
      await expect(page.getByRole('dialog')).toContainText('本次将跳过所选冲突项')
      expect(mock.count('transfer/import-selective')).toBe(0)
      await capture(page, 'import-conflict-confirmation')
      await page.getByRole('button', { name: '确认导入', exact: true }).click()
    }
    await expect.poll(() => mock.count('transfer/import-selective')).toBe(1)
    expect(mock.writes('transfer/import-selective')[0].postData()).toContain('name="overwrite"\r\n\r\n' + overwrite)
  })
}

test('桌面工作区保持可操作布局', async ({ page }) => {
  test.setTimeout(60000)
  const mock = await mockApi(page, true)
  for (const [path, title] of [
    ['config?env=qa&pipeline=web', '配置管理'], ['environments', '环境管理'],
    ['resources?env=qa&pipeline=web', '资源管理'], ['import-export', '导入导出'], ['migration', '配置迁移'],
  ]) {
    await page.goto(path)
    await expect(page.locator('h1')).toHaveText(title)
    for (const width of [1280, 1440]) {
      await page.setViewportSize({ width, height: 900 })
      await page.evaluate(dark => document.documentElement.classList.toggle('dark', dark), width === 1280)
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
      await capture(page, path.split('?')[0] + '-' + width + (width === 1280 ? '-dark' : '-light'))
    }
  }
  mock.authenticated = false
  await page.goto('login')
  await expect(page.getByRole('heading', { name: '登录工作台', exact: true })).toBeVisible()
  await capture(page, 'login-1440-light')
})
