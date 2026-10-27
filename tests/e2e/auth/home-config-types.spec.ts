import { test, expect, type Page } from '@playwright/test'
import { mkdir } from 'node:fs/promises'
import { join } from 'node:path'
import { mockApi, ok, meta, config } from './mock'

type Value = { type: string; content: string; alias: string }
const filePath = '/rainbow-bridge/api/v1/release/asset/published-snapshot/manual'
const richText = '<h2>发布说明</h2><p><strong>格式保留</strong>与正文</p><script>parent.document.body.dataset.previewExecuted="yes"</script>'

async function showValues(page: Page, values: Value[]) {
  const mock = await mockApi(page)
  mock.overrides.set('runtime/release', route => ok(route, {
    ...meta, selection_mode: 'active', environment: { environment_key: 'qa', pipeline_key: 'web' },
    configs: values.map(value => ({ ...config, ...value, resource_key: value.alias, name: value.alias })),
  }))
  await page.goto('./')
  await page.getByRole('combobox', { name: '环境', exact: true }).click()
  await page.getByRole('option', { name: '演示环境', exact: true }).click()
  await page.getByRole('combobox', { name: '渠道', exact: true }).click()
  await page.getByRole('option', { name: '网页渠道', exact: true }).click()
  await expect(page.locator('.runtime-summary')).toBeVisible()
  return mock
}

const row = (page: Page, alias: string) => page.locator('.runtime-panel').getByRole('row').filter({ has: page.getByRole('cell', { name: alias, exact: true }).first() })

test('颜色显示真实色块，原文切换和复制保持值不变', async ({ page, context }) => {
  await context.grantPermissions(['clipboard-read', 'clipboard-write'])
  await showValues(page, [
    { type: 'color', content: '#3B82F6', alias: 'lanse' },
    { type: 'color', content: '#F43BF7', alias: 'zise' },
    { type: 'color', content: '#ffffff', alias: 'white' },
    { type: 'file', content: filePath, alias: 'manual' },
    { type: 'richtext', content: richText, alias: 'release_notes' },
  ])
  await expect(row(page, 'lanse').getByRole('img', { name: '颜色预览 #3B82F6' })).toHaveCSS('background-color', 'rgb(59, 130, 246)')
  await expect(row(page, 'zise').getByRole('img')).toHaveCSS('background-color', 'rgb(244, 59, 247)')
  await expect(row(page, 'white').getByRole('img')).toHaveCSS('border-top-width', '1px')
  const blue = row(page, 'lanse')
  await blue.getByRole('button', { name: '查看 lanse 原文', exact: true }).click()
  expect(await blue.locator('pre').textContent()).toBe('#3B82F6')
  await blue.getByRole('button', { name: '复制 lanse 原文', exact: true }).click()
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe('#3B82F6')
  await blue.getByRole('button', { name: '查看 lanse 预览', exact: true }).click()
  await expect(blue.getByRole('img')).toHaveCSS('background-color', 'rgb(59, 130, 246)')
  if (process.env.RB_QA_VISUAL_DIR) {
    await mkdir(process.env.RB_QA_VISUAL_DIR, { recursive: true })
    for (const width of [1280, 1440]) {
      await page.setViewportSize({ width, height: 1000 })
      await page.evaluate(dark => document.documentElement.classList.toggle('dark', dark), width === 1280)
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
      await page.locator('.runtime-panel').screenshot({ path: join(process.env.RB_QA_VISUAL_DIR, 'config-types-' + width + '.png'), animations: 'disabled' })
    }
  }
})

test('文件打开当前发布资源，原文与复制保留完整部署路径', async ({ page, context }) => {
  await context.grantPermissions(['clipboard-read', 'clipboard-write'])
  await context.route('**/api/v1/release/asset/published-snapshot/manual', route => route.fulfill({ contentType: 'text/plain', body: 'published file' }))
  await showValues(page, [{ type: 'file', content: filePath, alias: 'manual' }])
  const file = row(page, 'manual')
  const link = file.getByRole('link', { name: '打开 manual', exact: true })
  await expect(link).toHaveAttribute('href', filePath)
  await expect(link).toHaveAttribute('rel', 'noopener noreferrer')
  const [popup] = await Promise.all([page.waitForEvent('popup'), link.click()])
  await expect(popup.locator('body')).toContainText('published file')
  expect(new URL(popup.url()).pathname).toBe(filePath)
  await popup.close()
  await file.getByRole('button', { name: '查看 manual 原文', exact: true }).click()
  expect(await file.locator('pre').textContent()).toBe(filePath)
  await file.getByRole('button', { name: '复制 manual 原文', exact: true }).click()
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(filePath)
})

test('富文本保留排版且脚本受隔离，查看和复制仍是HTML原文', async ({ page, context }) => {
  await context.grantPermissions(['clipboard-read', 'clipboard-write'])
  await showValues(page, [{ type: 'richtext', content: richText, alias: 'notes' }])
  const notes = row(page, 'notes')
  const preview = notes.locator('iframe')
  await expect(preview).toHaveAttribute('sandbox', '')
  await expect(preview).toHaveAttribute('srcdoc', richText)
  await expect(preview.contentFrame().getByRole('heading', { name: '发布说明' })).toBeVisible()
  await expect(preview.contentFrame().locator('strong')).toHaveText('格式保留')
  expect(await page.evaluate(() => document.body.dataset.previewExecuted)).toBeUndefined()
  await notes.getByRole('button', { name: '查看 notes 原文', exact: true }).click()
  expect(await notes.locator('pre').textContent()).toBe(richText)
  await expect(notes.locator('iframe')).toHaveCount(0)
  await notes.getByRole('button', { name: '复制 notes 原文', exact: true }).click()
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(richText)
  await notes.getByRole('button', { name: '查看 notes 预览', exact: true }).click()
  await expect(notes.locator('iframe').contentFrame().getByRole('heading')).toBeVisible()
})

test('其他类型保留换行、数值精度与原文；无效颜色和文件不会伪装为可用预览', async ({ page, context }) => {
  await context.grantPermissions(['clipboard-read', 'clipboard-write'])
  const values = [
    { type: 'text', content: '标题', alias: 'text' },
    { type: 'textarea', content: '第一行\n第二行\n  保留空格', alias: 'textarea' },
    { type: 'number', content: '900719925474099312345', alias: 'number' },
    { type: 'decimal', content: '0.123456789012345678901', alias: 'decimal' },
    { type: 'boolean', content: 'false', alias: 'boolean' },
    { type: 'keyvalue', content: '{"key":"value"}', alias: 'keyvalue' },
    { type: 'object', content: '{\n  "id": 900719925474099312345\n}', alias: 'object' },
    { type: 'color', content: 'invalid color', alias: 'invalid_color' },
    { type: 'file', content: 'javascript:alert(1)', alias: 'invalid_file' },
  ]
  await showValues(page, values)
  for (const value of values.slice(0, 7)) {
    const current = row(page, value.alias)
    expect(await current.locator('pre').textContent()).toBe(value.content)
    await current.getByRole('button', { name: `复制 ${value.alias} 原文`, exact: true }).click()
    // Windows clipboard converts LF to CRLF. Check visible line breaks while
    // the DOM assertion above still requires the exact response string.
    expect((await page.evaluate(() => navigator.clipboard.readText())).replace(/\r\n/g, '\n')).toBe(value.content)
  }
  await expect(row(page, 'invalid_color')).toContainText('颜色值无效')
  await expect(row(page, 'invalid_color').getByRole('img')).toHaveCount(0)
  await expect(row(page, 'invalid_file')).toContainText('文件地址无效')
  await expect(row(page, 'invalid_file').getByRole('link')).toHaveCount(0)
})
