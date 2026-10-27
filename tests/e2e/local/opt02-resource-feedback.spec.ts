import { expect, test } from '@playwright/test'
import * as fs from 'fs'
import * as path from 'path'
import { targetB, uploadsDir, writeEvidence } from './support/env'
import { buildTransferZip } from './support/archives'
import { setupSentinelScopes, scopeDevMain, TINY_PNG } from './support/scopes'
import { gotoPage } from './support/pages'
import { configsByAlias } from './support/db'

test('D8 页面保留部分完成结果，磁盘复位后原包重试 @opt02', async ({ page }) => {
  const target = targetB()
  await setupSentinelScopes(target, 'opt02')
  const id = 'opt02-retry-image'
  const archive = await buildTransferZip({
    configs: [{ alias: 'OPT02_RETRY', name: '资源恢复重试', environment_key: scopeDevMain.env, pipeline_key: scopeDevMain.pipeline, type: 'image', content: `asset://${id}` }],
    files: [{ fileId: id, fileName: '重试 图片.png', bytes: TINY_PNG }],
    meta: { assets: [{ file_id: id, file_name: '重试 图片.png', environment_key: scopeDevMain.env, pipeline_key: scopeDevMain.pipeline, content_type: 'image/png' }] },
  })
  const blocker = path.join(uploadsDir(target), id)
  fs.mkdirSync(path.dirname(blocker), { recursive: true })
  fs.writeFileSync(blocker, 'block resource directory')
  const responses: unknown[] = []
  page.on('response', async response => {
    if (response.url().includes('/transfer/import-selective')) responses.push(await response.json())
  })
  try {
    await gotoPage(page, target, '/import-export')
    await page.getByRole('tab', { name: '数据导入' }).click()
    await page.locator('input[type=file]').setInputFiles({ name: 'retry.zip', mimeType: 'application/zip', buffer: archive })
    await page.getByRole('button', { name: '开始导入', exact: true }).click()
    await expect(page.getByText('配置已提交，部分资源未恢复', { exact: true })).toBeVisible()
    await expect(page.getByText(/重试 图片.png.*file_.*failed/)).toBeVisible()
    await page.waitForTimeout(3200)
    await expect(page.getByText('配置已提交，部分资源未恢复', { exact: true })).toBeVisible()
    fs.rmSync(blocker)
    await page.getByRole('button', { name: '开始导入', exact: true }).click()
    await expect(page.getByText('导入完成，资源恢复完整', { exact: true })).toBeVisible()
    expect(configsByAlias(target, scopeDevMain.env, scopeDevMain.pipeline, 'OPT02_RETRY')).toHaveLength(1)
    expect(fs.readFileSync(path.join(blocker, '重试 图片.png')).equals(TINY_PNG)).toBe(true)
    expect((responses[0] as any)?.data?.resource_import).toMatchObject({ status: 'partial', expected: 1, failed: 1 })
    expect((responses[1] as any)?.data).toMatchObject({ total: 0, items: [], resource_import: { status: 'complete', expected: 1, restored: 1, failed: 0 } })
    writeEvidence('opt02-resource-feedback', { responses })
  } finally {
    if (fs.existsSync(blocker) && fs.statSync(blocker).isFile()) fs.rmSync(blocker)
  }
})
