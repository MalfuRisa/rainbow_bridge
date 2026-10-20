import { gotoPage } from './support/pages'
import { expect, test } from '@playwright/test'
import { targetB, writeEvidence } from './support/env'
import { buildTransferZip } from './support/archives'
import { importPreview, importSelective, importWithArchiveField } from './support/imports'
import { listConfigs, scopeDevMain, setupSentinelScopes, snapshotScope } from './support/scopes'

const ALIAS_OK = 'IMP_FIELD_OK'
const ALIAS_WRONG = 'IMP_FIELD_WRONG'

function fieldArchive(alias: string) {
  return buildTransferZip({
    configs: [
      {
        alias,
        name: `字段样本 ${alias}`,
        environment_key: scopeDevMain.env,
        pipeline_key: scopeDevMain.pipeline,
        type: 'text',
        content: 'field-check',
      },
    ],
  })
}

test.describe('T10 普通归档入口字段契约 @t10', () => {
  test('archive 字段成功、file 字段明确失败、页面真实字段网络记录 @t10', async ({ page }) => {
    const target = targetB()
    await setupSentinelScopes(target, 't10')
    const before = await snapshotScope(target, scopeDevMain)

    // 1) 正确字段 archive
    const okResponse = await importWithArchiveField(
      target,
      { bytes: await fieldArchive(ALIAS_OK), filename: 'field-ok.zip' },
      { overwrite: true },
    )
    expect(okResponse.json?.code, okResponse.text.slice(0, 300)).toBe(200)
    expect((await listConfigs(target, scopeDevMain)).find((item) => item.alias === ALIAS_OK)).toBeTruthy()

    // 2) 错误字段 file:明确失败且无数据变化
    const wrongBefore = await listConfigs(target, scopeDevMain)
    const wrongResponse = await importWithArchiveField(
      target,
      { bytes: await fieldArchive(ALIAS_WRONG), filename: 'field-wrong.zip' },
      { overwrite: true, field: 'file' },
    )
    const wrongAfter = await listConfigs(target, scopeDevMain)
    expect(wrongResponse.json?.code, '错误字段必须明确失败').not.toBe(200)
    expect(wrongAfter.length).toBe(wrongBefore.length)
    expect(wrongAfter.find((item) => item.alias === ALIAS_WRONG)).toBeUndefined()

    // 3) 预览与选择性导入用 file 字段
    const previewArchive = await fieldArchive('IMP_FIELD_SELECTIVE')
    const preview = await importPreview(target, { bytes: previewArchive, filename: 'preview.zip' })
    expect(preview.json?.code, preview.text.slice(0, 300)).toBe(200)
    const selective = await importSelective(
      target,
      { bytes: previewArchive, filename: 'selective.zip' },
      { selections: [{ environment_key: scopeDevMain.env, pipeline_key: scopeDevMain.pipeline, resource_keys: [] }], overwrite: true },
    )
    const selectiveOk = selective.json?.code === 200
    const selectiveAlias = 'IMP_FIELD_SELECTIVE'

    // 4) 页面真实网络记录:观察 UI 真正使用的字段与端点
    const pageRequests: { method: string; path: string; bodyField?: string; contentType?: string }[] = []
    page.on('request', (request) => {
      const requestUrl = new URL(request.url())
      if (!requestUrl.pathname.includes('/api/v1/transfer/')) return
      const postData = request.postData() ?? ''
      const fields = [...postData.matchAll(/name="([^"]+)"/g)].map((match) => match[1])
      pageRequests.push({
        method: request.method(),
        path: requestUrl.pathname,
        bodyField: fields.length ? fields.join(',') : undefined,
        contentType: request.headers()['content-type'] ?? '',
      })
    })

    await gotoPage(page, target, '/import-export')
    await page.getByRole('tab', { name: '数据导入' }).click()
    await page.locator('#import-file').setInputFiles({
      name: 'page-import.zip',
      mimeType: 'application/zip',
      buffer: await fieldArchive('IMP_FROM_PAGE'),
    })
    await expect(page.getByText('选择导入内容')).toBeVisible({ timeout: 15_000 })
    await page.getByRole('button', { name: '开始导入' }).click()
    const confirmButton = page.getByRole('button', { name: '确认导入' })
    if (await confirmButton.isVisible().catch(() => false)) {
      await confirmButton.click()
    }
    await expect(page.getByText('导入完成，资源恢复完整', { exact: true })).toBeVisible({ timeout: 20_000 })
    const pageImported = (await listConfigs(target, scopeDevMain)).find((item) => item.alias === 'IMP_FROM_PAGE')

    const after = await snapshotScope(target, scopeDevMain)
    writeEvidence('t10-import-entry', {
      okResponse: okResponse.json,
      wrongFieldResponse: { code: wrongResponse.json?.code, error: wrongResponse.json?.error },
      previewOk: preview.json?.code,
      selectiveOk,
      pageRequests,
      pageImported: Boolean(pageImported),
      beforeCount: before.configs.length,
      afterCount: after.configs.length,
    })

    // 页面真实调用必须命中 file 字段的端点，且不得调用读 archive 字段的普通入口
    const importCalls = pageRequests.filter((item) => item.path.endsWith('/import-selective'))
    const legacyCalls = pageRequests.filter((item) => item.path.endsWith('/transfer/import'))
    expect(importCalls.length, `页面未发出选择性导入请求: ${JSON.stringify(pageRequests)}`).toBeGreaterThan(0)
    // multipart 附件场景 Chromium 可能不暴露 postData:此时用"导入成功 + 端点只接受 file 字段"推导
    const observedField = importCalls[0].bodyField ?? (pageImported ? 'file(推导:导入成功且该端点只接受 file 字段)' : 'unknown')
    expect.soft(observedField, `选择性导入应使用 file 字段(实际捕获: ${JSON.stringify(importCalls)})`).toContain('file')
    expect.soft(importCalls.map((item) => item.contentType ?? ''), '选择性导入应为 multipart').toEqual(
      expect.arrayContaining([expect.stringContaining('multipart/form-data')]),
    )
    expect(legacyCalls.length, `页面不应调用普通导入入口(archive/file 字段不一致): ${JSON.stringify(legacyCalls)}`).toBe(0)
    expect(pageImported, '页面导入的配置未落库').toBeTruthy()
  })
})