import { test, expect } from '@playwright/test'
import { targetA, url, writeEvidence } from './support/env'
import { ensureScope, createConfig, listConfigs, uploadAsset, TINY_PNG, type Scope } from './support/scopes'
import { getJson } from './support/net'
import { buildTransferZip } from './support/archives'
import JSZip from 'jszip'
import { gotoPage } from './support/pages'

const source: Scope = { env: 'qa_spectrum_workflows', pipeline: 'source', label: 'qa_spectrum_workflows/source', sentinel: 'SPECTRUM_SOURCE' }
const target: Scope = { ...source, pipeline: 'target', label: 'qa_spectrum_workflows/target', sentinel: 'SPECTRUM_TARGET' }

test.describe('光谱工作台全站流程 @spectrum @smoke', () => {
  test('环境与渠道创建、带范围进入配置，以及删除确认', async ({ page }) => {
    const server = targetA()
    await gotoPage(page, server, '/environments')
    await page.getByRole('button', { name: '新增环境', exact: true }).click()
    const envForm = page.getByRole('dialog').filter({ has: page.locator('#env-key') })
    const pipelineForm = page.getByRole('dialog').filter({ has: page.locator('#pipeline-key') })
    const form = page.getByRole('dialog').filter({ has: page.getByRole('button', { name: '确认删除', exact: true }) })
    await envForm.locator('#env-key').fill('qa_spectrum_created')
    await envForm.locator('#env-name').fill('光谱创建样本')
    await envForm.getByRole('button', { name: '保存', exact: true }).click()
    await expect(envForm).toBeHidden()
    await page.getByRole('button', { name: '光谱创建样本 qa_spectrum_created', exact: true }).click()
    await page.getByRole('button', { name: '新增渠道', exact: true }).click()
    await pipelineForm.locator('#pipeline-key').fill('stage')
    await pipelineForm.locator('#pipeline-name').fill('预发布样本')
    await pipelineForm.getByRole('button', { name: '保存', exact: true }).click()
    await expect(pipelineForm).toBeHidden()
    await page.getByRole('link', { name: '管理 预发布样本 配置', exact: true }).click()
    await expect(page.getByRole('combobox', { name: '环境', exact: true })).toContainText('光谱创建样本')
    await expect(page.getByRole('combobox', { name: '渠道', exact: true })).toContainText('预发布样本')
    await gotoPage(page, server, '/environments')
    await page.getByRole('button', { name: '光谱创建样本 qa_spectrum_created', exact: true }).click()
    await page.getByRole('button', { name: '删除渠道 预发布样本', exact: true }).click()
    await form.getByRole('button', { name: '取消', exact: true }).click()
    await expect(page.getByRole('link', { name: '管理 预发布样本 配置', exact: true })).toBeVisible()
    await page.getByRole('button', { name: '删除渠道 预发布样本', exact: true }).click()
    await form.getByRole('button', { name: '确认删除', exact: true }).click()
    await expect(page.getByRole('link', { name: '管理 预发布样本 配置', exact: true })).toHaveCount(0)
    await page.getByRole('button', { name: '删除环境 光谱创建样本', exact: true }).click()
    await form.getByRole('button', { name: '确认删除', exact: true }).click()
    await expect(page.getByRole('button', { name: '光谱创建样本 qa_spectrum_created', exact: true })).toHaveCount(0)
    const environments = await getJson(server, '/api/v1/environment/list')
    expect(environments.json.data.list.some((env: any) => env.environment_key === 'qa_spectrum_created')).toBe(false)
  })

  test('资源真实上传、搜索、图片加载和复制链接', async ({ page, context }) => {
    const server = targetA()
    await ensureScope(server, source)
    await context.grantPermissions(['clipboard-read', 'clipboard-write'])
    await gotoPage(page, server, `/resources?env=${source.env}&pipeline=${source.pipeline}`)
    await expect(page.getByRole('button', { name: '上传资源', exact: true })).toBeEnabled()
    await page.locator('input[type=file]').setInputFiles({ name: 'spectrum-workflow.png', mimeType: 'image/png', buffer: TINY_PNG })
    const row = page.locator('tr').filter({ hasText: 'spectrum-workflow.png' })
    await expect(row).toBeVisible()
    await page.getByRole('textbox', { name: '搜索资源名称' }).fill('not-a-resource')
    await expect(page.getByText('未找到匹配的资源', { exact: true })).toBeVisible()
    await page.getByRole('textbox', { name: '搜索资源名称' }).fill('spectrum-workflow')
    await row.getByRole('button', { name: '查看 spectrum-workflow.png 大图', exact: true }).click()
    const image = page.getByRole('dialog').getByRole('img', { name: 'spectrum-workflow.png', exact: true })
    await expect(image).toBeVisible()
    await expect.poll(() => image.evaluate((element: HTMLImageElement) => element.complete && element.naturalWidth > 0)).toBe(true)
    await page.getByRole('dialog').getByRole('button', { name: 'Close', exact: true }).click()
    await row.getByRole('button', { name: '复制 spectrum-workflow.png 链接', exact: true }).click()
    expect(await page.evaluate(() => navigator.clipboard.readText())).toMatch(new RegExp(`^${server.origin}${server.prefix}/api/v1/asset/file/`))
  })

  test('资源范围切换丢弃迟到响应', async ({ page }) => {
    const server = targetA()
    await ensureScope(server, source)
    await ensureScope(server, target)
    await uploadAsset(server, target, { fileName: 'target-only.txt', contentType: 'text/plain', bytes: Buffer.from('target scope') })
    let release!: () => void
    const gate = new Promise<void>(resolve => { release = resolve })
    let held = false
    let delivered = false
    await page.route('**/api/v1/asset/list?**', async route => {
      if (new URL(route.request().url()).searchParams.get('pipeline_key') !== source.pipeline) return route.continue()
      const response = await route.fetch()
      held = true
      await gate
      await route.fulfill({ response })
      delivered = true
    })
    await gotoPage(page, server, `/resources?env=${source.env}&pipeline=${source.pipeline}`)
    await expect.poll(() => held).toBe(true)
    await page.getByRole('combobox', { name: '渠道', exact: true }).click()
    await page.getByRole('option', { name: 'QA target (target)', exact: true }).click()
    await expect(page.locator('tr').filter({ hasText: 'target-only.txt' })).toBeVisible()
    release()
    await expect.poll(() => delivered).toBe(true)
    await expect(page.locator('tr').filter({ hasText: 'target-only.txt' })).toBeVisible()
    await expect(page.locator('tr').filter({ hasText: 'spectrum-workflow.png' })).toHaveCount(0)
  })

  test('迁移预览、冲突选择与真实写入，改变范围清除旧预览', async ({ page }) => {
    const server = targetA()
    await ensureScope(server, source)
    await ensureScope(server, target)
    await createConfig(server, source, { alias: 'SPECTRUM_MOVE', name: '迁移冲突样本', type: 'text', content: 'source value' })
    await createConfig(server, target, { alias: 'SPECTRUM_MOVE', name: '迁移冲突样本', type: 'text', content: 'target value' })
    await gotoPage(page, server, '/migration')
    for (const [label, option] of [['源环境', 'QA qa_spectrum_workflows'], ['源渠道', 'QA source'], ['目标环境', 'QA qa_spectrum_workflows'], ['目标渠道', 'QA target']]) {
      await page.getByRole('combobox', { name: label, exact: true }).click()
      await page.getByRole('option', { name: option, exact: true }).click()
    }
    await page.getByRole('button', { name: '预览变更', exact: true }).click()
    await expect(page.getByText('存在冲突', { exact: true })).toBeVisible()
    await expect(page.getByRole('button', { name: '迁移 1 个配置', exact: true })).toBeDisabled()
    await page.locator('#overwrite').click()
    await page.getByRole('button', { name: '迁移 1 个配置', exact: true }).click()
    await expect(page.getByRole('heading', { name: '迁移完成', exact: true })).toBeVisible()
    expect((await listConfigs(server, target)).find(item => item.alias === 'SPECTRUM_MOVE')!.content).toBe('source value')
    await page.getByRole('button', { name: '开始新的迁移', exact: true }).click()
    await expect(page.getByRole('button', { name: '预览变更', exact: true })).toBeDisabled()
    await expect(page.getByRole('heading', { name: '变更预览', exact: true })).toHaveCount(0)
  })

  test('桌面端五个页面的导航、表格与发布弹窗保持可达', async ({ page }) => {
    const server = targetA()
    await ensureScope(server, source)
    await page.setViewportSize({ width: 1440, height: 900 })
    for (const [pathname, heading] of [['/environments', '环境管理'], [`/resources?env=${source.env}&pipeline=${source.pipeline}`, '资源管理'], ['/migration', '配置迁移'], ['/import-export', '导入导出'], [`/releases?env=${source.env}&pipeline=${source.pipeline}`, '版本发布']]) {
      await gotoPage(page, server, pathname)
      await expect(page.getByRole('heading', { level: 1, name: heading, exact: true })).toBeVisible()
      await expect(page.getByRole('link', { name: '项目概览', exact: true })).toBeVisible()
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), `${heading} 页面横向溢出`).toBe(true)
    }
    await expect(page.getByRole('combobox', { name: '环境', exact: true })).toContainText('QA qa_spectrum_workflows')
    await expect(page.getByRole('combobox', { name: '渠道', exact: true })).toContainText('QA source')
    await page.getByRole('button', { name: '创建版本', exact: true }).click()
    await expect(page.getByRole('dialog').locator('#release-version')).toBeVisible()
    expect(await page.getByRole('dialog').evaluate(element => element.getBoundingClientRect().width <= window.innerWidth)).toBe(true)
    await page.getByRole('dialog').getByRole('button', { name: '取消', exact: true }).click()
    await expect(page.getByRole('dialog')).toBeHidden()
    await page.evaluate(() => window.scrollTo(0, document.documentElement.scrollHeight))
    await page.getByRole('link', { name: '环境管理', exact: true }).click()
    await expect(page.getByRole('heading', { level: 1, name: '环境管理', exact: true })).toBeVisible()
    await expect.poll(() => page.evaluate(() => window.scrollY)).toBe(0)
  })

  test('归档导入确认保留默认跳过语义，选择后返回真实 ZIP 并触发下载', async ({ page }) => {
    const server = targetA()
    await ensureScope(server, source)
    await createConfig(server, source, { alias: 'SPECTRUM_ARCHIVE', name: '归档冲突样本', type: 'text', content: 'keep existing' })
    const archive = await buildTransferZip({ configs: [
      { alias: 'SPECTRUM_ARCHIVE', name: '归档冲突样本', environment_key: source.env, pipeline_key: source.pipeline, type: 'text', content: 'archive replacement' },
      { alias: 'SPECTRUM_ARCHIVE_NEW', name: '归档新样本', environment_key: source.env, pipeline_key: source.pipeline, type: 'text', content: 'new from archive' },
    ] })
    await gotoPage(page, server, '/import-export')
    await page.getByRole('tab', { name: '数据导入', exact: true }).click()
    await page.locator('#import-file').setInputFiles({ name: 'spectrum-archive.zip', mimeType: 'application/zip', buffer: archive })
    await expect(page.getByRole('heading', { name: '选择导入内容', exact: true })).toBeVisible()
    await page.getByRole('button', { name: '开始导入', exact: true }).click()
    await expect(page.getByRole('dialog').getByRole('heading', { name: '确认导入', exact: true })).toBeVisible()
    expect((await listConfigs(server, source)).some(item => item.alias === 'SPECTRUM_ARCHIVE_NEW')).toBe(false)
    await page.getByRole('dialog').getByRole('button', { name: '取消', exact: true }).click()
    await page.getByRole('button', { name: '开始导入', exact: true }).click()
    await page.getByRole('dialog').getByRole('button', { name: '确认导入', exact: true }).click()
    await expect(page.getByText('导入完成，资源恢复完整', { exact: true })).toBeVisible()
    const configs = await listConfigs(server, source)
    expect(configs.find(item => item.alias === 'SPECTRUM_ARCHIVE')!.content).toBe('keep existing')
    expect(configs.find(item => item.alias === 'SPECTRUM_ARCHIVE_NEW')!.content).toBe('new from archive')
    await page.getByRole('tab', { name: '数据导出', exact: true }).click()
    await page.getByRole('checkbox', { name: '选择环境 QA qa_spectrum_workflows', exact: true }).check()
    let exportBytes: Buffer | undefined
    // Capture the real HTTP archive before Chromium hands the attachment to its
    // native download service, which is unavailable on some Windows test hosts.
    await page.route('**/api/v1/transfer/export', async route => {
      const response = await route.fetch()
      exportBytes = await response.body()
      await route.fulfill({ response, body: exportBytes })
    })
    const [download, exportResponse] = await Promise.all([
      page.waitForEvent('download'),
      page.waitForResponse(response => response.url().includes('/api/v1/transfer/export') && response.request().method() === 'POST'),
      page.getByRole('button', { name: '导出数据', exact: true }).click(),
    ])
    expect(exportResponse.ok()).toBe(true)
    expect(exportResponse.headers()['content-type']).toContain('application/zip')
    expect(download.suggestedFilename()).toMatch(/\.zip$/)
    expect(exportResponse.request().postDataJSON()).toEqual({ format: 'zip', selections: [{ environment_key: source.env, pipeline_key: '', resource_keys: [] }] })
    expect(exportBytes?.length).toBeGreaterThan(0)
    const zip = await JSZip.loadAsync(exportBytes!)
    expect(await zip.file('configs.json')!.async('text')).toContain('SPECTRUM_ARCHIVE_NEW')
    // File saving is recorded separately: this host also cancels an independent
    // data:text/plain download, so OS download completion is not an app assertion.
    writeEvidence('spectrum-archive-download', { filename: download.suggestedFilename(), nativeDownloadFailure: await download.failure(), zipValidated: true })
  })
})
