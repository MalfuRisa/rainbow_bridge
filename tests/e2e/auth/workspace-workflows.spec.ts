import { test, expect, type Page } from '@playwright/test'
import { mkdir } from 'node:fs/promises'
import { join } from 'node:path'
import { mockApi, ok, config, fixtureIdentity } from './mock'

async function captureDesktop(page: Page, name: string) {
  for (const width of [1280, 1440]) {
    await page.setViewportSize({ width, height: 900 })
    for (const dark of [false, true]) {
      await page.evaluate(value => document.documentElement.classList.toggle('dark', value), dark)
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
      if (!process.env.RB_QA_VISUAL_DIR) continue
      await page.evaluate(() => {
        (document.activeElement as HTMLElement | null)?.blur()
        window.scrollTo({ top: 0, behavior: 'instant' })
        return new Promise<void>(resolve => requestAnimationFrame(() => requestAnimationFrame(() => resolve())))
      })
      await mkdir(process.env.RB_QA_VISUAL_DIR, { recursive: true })
      await page.screenshot({ path: join(process.env.RB_QA_VISUAL_DIR, `${name}-${width}-${dark ? 'dark' : 'light'}.png`), fullPage: true, animations: 'disabled' })
    }
  }
}

test('资源搜索、图片大图和完整链接复制在桌面工作区仍可操作', async ({ page, context }) => {
  await context.grantPermissions(['clipboard-read', 'clipboard-write'])
  const mock = await mockApi(page, true)
  const imageURL = '/rainbow-bridge/api/v1/asset/file/cover'
  mock.overrides.set('asset/list', route => ok(route, { list: [
    { file_id: 'cover', file_name: 'cover.svg', content_type: 'image/svg+xml', file_size: 512, url: imageURL },
    { file_id: 'manual', file_name: 'manual.txt', content_type: 'text/plain', file_size: 128, url: '/rainbow-bridge/api/v1/asset/file/manual' },
  ], total: 2 }))
  mock.overrides.set('asset/file/cover', route => route.fulfill({ contentType: 'image/svg+xml', body: '<svg xmlns="http://www.w3.org/2000/svg" width="320" height="160"><rect width="320" height="160" fill="#1b1614"/><path d="M80 140V100a80 80 0 0 1 160 0v40" fill="none" stroke="#ff7f00" stroke-width="16"/></svg>' }))
  await page.goto('resources?env=qa&pipeline=web')
  await expect(page.getByRole('button', { name: '预览 cover.svg', exact: true }).getByRole('img')).toBeVisible()
  await captureDesktop(page, 'resources-populated')
  const search = page.getByRole('textbox', { name: '搜索资源名称', exact: true })
  await search.fill('manual')
  await expect(page.getByText('cover.svg', { exact: true })).toHaveCount(0)
  await expect(page.getByText('manual.txt', { exact: true })).toBeVisible()
  await search.fill('')
  const preview = page.getByRole('button', { name: '查看 cover.svg 大图', exact: true })
  await preview.click()
  const dialog = page.getByRole('dialog', { name: 'cover.svg', exact: true })
  await expect.poll(() => dialog.getByRole('img').evaluate((img: HTMLImageElement) => img.naturalWidth)).toBe(320)
  await page.keyboard.press('Escape')
  await expect(dialog).toBeHidden()
  await expect(preview).toBeFocused()
  await page.getByRole('button', { name: '复制 cover.svg 链接', exact: true }).click()
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(new URL(imageURL, page.url()).href)
})

test('迁移预览保留冲突保护，确认覆盖后按所选范围提交并可重置', async ({ page }) => {
  const mock = await mockApi(page, true)
  mock.overrides.set('runtime/overview', route => ok(route, { list: [{
    environment_key: 'qa', environment_name: '演示环境', pipelines: [
      { pipeline_key: 'web', pipeline_name: '网页渠道' }, { pipeline_key: 'staging', pipeline_name: '预发渠道' },
    ],
  }], total: 1 }))
  mock.overrides.set('config/list', route => ok(route, { list: [{ ...config,
    content: new URL(route.request().url()).searchParams.get('pipeline_key') === 'staging' ? '旧标题' : '新标题',
  }], total: 1 }))
  mock.overrides.set('transfer/migrate', route => ok(route, { succeeded: 1, skipped: 0, items: [{ status: 'succeeded' }] }))
  await page.goto('migration')
  for (const [label, value] of [['源环境', '演示环境'], ['源渠道', '网页渠道'], ['目标环境', '演示环境'], ['目标渠道', '预发渠道']]) {
    await page.getByRole('combobox', { name: label, exact: true }).click()
    await page.getByRole('option', { name: value, exact: true }).click()
  }
  await page.getByRole('button', { name: '预览变更', exact: true }).click()
  await expect(page.getByText('存在冲突', { exact: true })).toBeVisible()
  const migrate = page.getByRole('button', { name: '迁移 1 个配置', exact: true })
  await expect(migrate).toBeDisabled()
  expect(mock.count('transfer/migrate')).toBe(0)
  await captureDesktop(page, 'migration-conflict')
  await page.getByRole('checkbox', { name: '覆盖目标中的冲突配置', exact: true }).check()
  await migrate.click()
  await expect(page.getByRole('heading', { name: '迁移完成', exact: true })).toBeVisible()
  expect(mock.writes('transfer/migrate')[0].postDataJSON()).toMatchObject({
    source_environment_key: 'qa', source_pipeline_key: 'web', target_environment_key: 'qa', target_pipeline_key: 'staging',
    resource_keys: ['r1'], overwrite: true,
    source_expected_environment_id: fixtureIdentity('qa'), source_expected_scope_id: fixtureIdentity('qa', 'web'),
    target_expected_environment_id: fixtureIdentity('qa'), target_expected_scope_id: fixtureIdentity('qa', 'staging'),
  })
  await page.getByRole('button', { name: '开始新的迁移', exact: true }).click()
  await expect(page.getByRole('button', { name: '预览变更', exact: true })).toBeDisabled()
  await expect(page.getByRole('heading', { name: '变更预览', exact: true })).toHaveCount(0)
})
