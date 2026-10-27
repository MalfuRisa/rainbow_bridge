import { expect, test } from '@playwright/test'
import * as path from 'path'
import { targetB, writeEvidence } from './support/env'
import { buildTransferZip, truncateMiddle } from './support/archives'
import { importPreview, importWithArchiveField } from './support/imports'
import { TINY_PNG, listAssets, listConfigs, scopeDevMain, setupSentinelScopes } from './support/scopes'
import { configsByAlias } from './support/db'

const FIXED_FILE_ID = 'qa-corrupt-fid-01'

function baseOptions() {
  return {
    configs: [
      {
        alias: 'CORRUPT_BASE',
        name: '损坏包基础项',
        environment_key: 'qa_dev',
        pipeline_key: 'main',
        type: 'image',
        content: `asset://${FIXED_FILE_ID}`,
      },
    ],
    files: [{ fileId: FIXED_FILE_ID, fileName: 'corrupt.png', bytes: TINY_PNG }],
    meta: {
      assets: [
        { file_id: FIXED_FILE_ID, environment_key: 'qa_dev', pipeline_key: 'main', file_name: 'corrupt.png', content_type: 'image/png', file_size: TINY_PNG.length, remark: '' },
      ],
    },
  }
}

test.describe('T13 缺失与损坏归档 @t13', () => {
  test('缺文件/缺元数据/截断/坏 JSON 四种包:无静默误报成功 @t13', async ({ page }) => {
    const target = targetB()
    await setupSentinelScopes(target, 't13')

    const valid = await buildTransferZip(baseOptions())
    const missingFile = await buildTransferZip({ ...baseOptions(), files: [] })
    const missingMeta = await buildTransferZip({ ...baseOptions(), omitConfigsJson: true })
    const truncated = truncateMiddle(valid, 0.6)
    const badJson = await buildTransferZip({ ...baseOptions(), rawConfigsJson: Buffer.from('{"business_configs": [') })

    const cases: { id: string; bytes: Buffer; alias: string }[] = [
      { id: 'missing-file', bytes: missingFile, alias: 'CORRUPT_BASE' },
      { id: 'missing-metadata', bytes: missingMeta, alias: 'CORRUPT_BASE' },
      { id: 'truncated-zip', bytes: truncated, alias: 'CORRUPT_BASE' },
      { id: 'bad-json', bytes: badJson, alias: 'CORRUPT_BASE' },
    ]

    const observed: Record<string, unknown> = {}
    for (const sample of cases) {
      const beforeConfigs = (await listConfigs(target, scopeDevMain)).length
      const beforeAssets = (await listAssets(target, scopeDevMain)).length
      const preview = await importPreview(target, { bytes: sample.bytes, filename: `${sample.id}.zip` })
      const importResponse = await importWithArchiveField(target, { bytes: sample.bytes, filename: `${sample.id}.zip` }, { overwrite: true })
      const afterConfigs = await listConfigs(target, scopeDevMain)
      const afterAssets = await listAssets(target, scopeDevMain)
      const rows = configsByAlias(target, scopeDevMain.env, scopeDevMain.pipeline, sample.alias)
      const assetRow = afterAssets.find((asset) => asset.file_id === FIXED_FILE_ID)
      observed[sample.id] = {
        previewCode: preview.json?.code ?? null,
        previewError: preview.json?.error ?? null,
        importCode: importResponse.json?.code ?? null,
        importError: importResponse.json?.error ?? null,
        importTotal: importResponse.json?.data?.total ?? null,
        resourceResult: importResponse.json?.data?.resource_import,
        message: importResponse.json?.msg,
        configRows: rows.length,
        configDelta: afterConfigs.length - beforeConfigs,
        assetDelta: afterAssets.length - beforeAssets,
        assetRowPresent: Boolean(assetRow),
        assetFileName: assetRow?.file_name ?? null,
      }
      if (sample.id === 'missing-file') {
        await page.screenshot({ path: path.join(process.env.RB_QA_ARTIFACTS_DIR!, 't13-missing-file.png'), fullPage: true })
      }
    }

    writeEvidence('t13-corrupt', observed)

    // 缺元数据/截断/坏 JSON:必须失败且无配置写入
    for (const id of ['missing-metadata', 'truncated-zip', 'bad-json']) {
      const item = observed[id] as any
      expect(item.importCode, `${id} 导入应失败`).not.toBe(200)
      expect(item.configDelta, `${id} 不应写入配置`).toBe(0)
    }
    // 缺文件:若配置已提交,必须显式表现为"配置成功但资源缺失"的部分成功，而不是完整成功
    const missing = observed['missing-file'] as any
    expect(missing.importCode).toBe(200)
    expect(missing.message).toBe('partial')
    expect(missing.resourceResult).toMatchObject({status:'partial',expected:1,restored:0,reused:0,failed:1})
    expect(missing.resourceResult.issues[0].reason).toBe('missing_file')
    expect(missing.assetRowPresent).toBe(false)
  })
})
