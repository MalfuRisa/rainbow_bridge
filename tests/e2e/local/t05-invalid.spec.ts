import { expect, test } from '@playwright/test'
import { targetA, writeEvidence } from './support/env'
import { postRawJson } from './support/net'
import { createConfig, deleteConfig, scopeDevMain, setupSentinelScopes, snapshotAllScopes, diffSnapshots } from './support/scopes'
import { configsByAlias } from './support/db'
import { gotoPage, selectEnvPipelineOnConfigPage } from './support/pages'

test.describe('T05 非法输入拒绝 @t05', () => {
  test('API 与页面非法样本:已定义契约拒绝、未定义边界据实记录 @t05', async ({ page }) => {
    const target = targetA()
    await setupSentinelScopes(target, 't05')
    const before = await snapshotAllScopes(target)

    const samples: { id: string; rawBody: string; judgement: string; shouldFail: boolean }[] = [
      {
        id: 'malformed-json',
        rawBody: '{"config": {"alias": "QA_BAD",',
        judgement: '非法 JSON 应被拒绝',
        shouldFail: true,
      },
      {
        id: 'object-top-array',
        rawBody: JSON.stringify({ config: { environment_key: 'qa_dev', pipeline_key: 'main', alias: 'QA_BAD_ARR', name: 'n', type: 'object', content: '[1,2,3]' } }),
        judgement: 'object 顶层数组应被拒绝(契约:需为 JSON 对象)',
        shouldFail: true,
      },
      {
        id: 'object-top-null',
        rawBody: JSON.stringify({ config: { environment_key: 'qa_dev', pipeline_key: 'main', alias: 'QA_BAD_NULL', name: 'n', type: 'object', content: 'null' } }),
        judgement: 'object 顶层 null 应被拒绝',
        shouldFail: true,
      },
      {
        id: 'number-decimal',
        rawBody: JSON.stringify({ config: { environment_key: 'qa_dev', pipeline_key: 'main', alias: 'QA_BAD_NUM', name: 'n', type: 'number', content: '1.2' } }),
        judgement: 'number=1.2 应被拒绝(整数契约)',
        shouldFail: true,
      },
      {
        id: 'decimal-nan',
        rawBody: JSON.stringify({ config: { environment_key: 'qa_dev', pipeline_key: 'main', alias: 'QA_BAD_NAN', name: 'n', type: 'decimal', content: 'NaN' } }),
        judgement: 'decimal=NaN 应被拒绝',
        shouldFail: true,
      },
      {
        id: 'boolean-upper',
        rawBody: JSON.stringify({ config: { environment_key: 'qa_dev', pipeline_key: 'main', alias: 'QA_BAD_BOOL', name: 'n', type: 'boolean', content: 'TRUE' } }),
        judgement: 'boolean=TRUE 应被拒绝(仅 true/false)',
        shouldFail: true,
      },
      {
        id: 'empty-name',
        rawBody: JSON.stringify({ config: { environment_key: 'qa_dev', pipeline_key: 'main', alias: 'QA_EMPTY_NAME', name: '', type: 'text', content: 'x' } }),
        judgement: '空 name 边界未定义，据实记录',
        shouldFail: false,
      },
      {
        id: 'empty-alias',
        rawBody: JSON.stringify({ config: { environment_key: 'qa_dev', pipeline_key: 'main', alias: '', name: '空别名样本', type: 'text', content: 'x' } }),
        judgement: '空 alias 边界未定义，据实记录',
        shouldFail: false,
      },
    ]

    const results: Record<string, unknown> = {}
    const undefinedBoundaryCreated: { alias: string; resourceKey: string }[] = []
    for (const sample of samples) {
      const response = await postRawJson(target, '/api/v1/config/create', sample.rawBody)
      const code = response.json?.code
      results[sample.id] = { code, error: response.json?.error ?? null, status: response.status, judgement: sample.judgement }
      if (sample.shouldFail) {
        expect(code, `${sample.id} 应被拒绝: ${sample.judgement}; body=${response.text.slice(0, 200)}`).not.toBe(200)
        continue
      }
      // 未定义边界:据实记录；若被接受，清理后继续，保证哨兵对比仍然严格
      if (code === 200) {
        undefinedBoundaryCreated.push({
          alias: sample.id,
          resourceKey: String(response.json?.data?.config?.resource_key ?? ''),
        })
      }
    }
    ;(results as any).undefinedBoundaryCreated = undefinedBoundaryCreated
    for (const created of undefinedBoundaryCreated) {
      if (created.resourceKey) {
        await deleteConfig(target, scopeDevMain, created.resourceKey)
      }
    }

    // 页面非法样本:整数填小数、空名称
    await gotoPage(page, target, '/config')
    await selectEnvPipelineOnConfigPage(page, 'QA qa_dev (qa_dev)', 'QA main (main)')
    await page.getByRole('button', { name: '新增配置' }).click()
    const dialog = page.getByRole('dialog').filter({ has: page.locator('#alias') })
    await dialog.locator('#name').fill('页面非法样本')
    await dialog.locator('#alias').fill('QA_PAGE_BAD')
    await dialog.getByRole('combobox').click()
    await page.getByRole('option', { name: /整数/ }).click()
    await dialog.locator('#content').fill('1.2')
    await dialog.getByRole('button', { name: '保存' }).click()
    const integerError = await page.getByText('请输入整数').first().isVisible()
    results['page-number-decimal'] = { blockedByUi: integerError }

    await dialog.locator('#name').fill('')
    await dialog.getByRole('button', { name: '保存' }).click()
    const nameError = await page.getByText('名称不能为空').first().isVisible()
    results['page-empty-name'] = { blockedByUi: nameError }
    await dialog.getByRole('button', { name: '取消' }).click()

    // 失败样本不得修改任何哨兵范围(未定义边界样本已清理)
    const after = await snapshotAllScopes(target)
    const differences = diffSnapshots(before, after)
    writeEvidence('t05-invalid', { results, differences })
    expect(differences, `非法输入影响了哨兵: ${differences.join('; ')}`).toEqual([])

    // 被拒绝的样本不得在库中留下记录
    for (const alias of ['QA_BAD_ARR', 'QA_BAD_NULL', 'QA_BAD_NUM', 'QA_BAD_NAN', 'QA_BAD_BOOL']) {
      expect(configsByAlias(target, scopeDevMain.env, scopeDevMain.pipeline, alias).length, `${alias} 不应存在`).toBe(0)
    }
  })
})