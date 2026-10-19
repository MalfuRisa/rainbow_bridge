import { expect, test } from '@playwright/test'
import { targetA, writeEvidence } from './support/env'
import { buildTransferZip } from './support/archives'
import { importSummary, importWithArchiveField } from './support/imports'
import { createConfig, scopeDevMain, setupSentinelScopes } from './support/scopes'
import { configsByAlias } from './support/db'

const ALIAS = 'CF_ALIAS'

test.describe('T08 冲突与幂等 @t08', () => {
  test('同 alias 不同源 key、重复导入、同批重复:不误报、不多建 @t08', async () => {
    const target = targetA()
    await setupSentinelScopes(target, 't08')

    const existing = configsByAlias(target, scopeDevMain.env, scopeDevMain.pipeline, ALIAS)[0]
    let targetKey = String(existing?.resource_key ?? '')
    if (!targetKey) {
      const { config } = await createConfig(target, scopeDevMain, {
        alias: ALIAS, name: '冲突目标', type: 'text', content: 'v1', description: '原始',
      })
      targetKey = config!.resource_key
    }

    const makeArchive = () =>
      buildTransferZip({
        configs: [
          {
            resource_key: 'source-different-key',
            alias: ALIAS,
            name: '冲突来源',
            environment_key: scopeDevMain.env,
            pipeline_key: scopeDevMain.pipeline,
            type: 'text',
            content: 'v2',
            description: '来自导入',
          },
        ],
      })
    const duplicateArchive = await buildTransferZip({
      configs: [
        {
          alias: 'CF_DUP_BATCH',
          name: '同批重复一',
          environment_key: scopeDevMain.env,
          pipeline_key: scopeDevMain.pipeline,
          type: 'text',
          content: 'first',
        },
        {
          alias: 'CF_DUP_BATCH',
          name: '同批重复二',
          environment_key: scopeDevMain.env,
          pipeline_key: scopeDevMain.pipeline,
          type: 'text',
          content: 'second',
        },
      ],
    })

    const observed: Record<string, unknown> = {}
    const rowsOf = (alias: string) => configsByAlias(target, scopeDevMain.env, scopeDevMain.pipeline, alias)

    // 1) overwrite=false:跳过，摘要只计实际写入
    const skipResponse = await importWithArchiveField(target, { bytes: await makeArchive(), filename: 'conflict-false.zip' }, { overwrite: false })
    observed['overwrite-false'] = { code: skipResponse.json?.code, summary: importSummary(skipResponse) }
    expect(skipResponse.json?.code).toBe(200)
    expect(importSummary(skipResponse)?.total, '跳过不能计入成功').toBe(0)
    expect(rowsOf(ALIAS).length).toBe(1)
    expect(String(rowsOf(ALIAS)[0]?.content)).toBe('v1')
    expect(String(rowsOf(ALIAS)[0]?.resource_key)).toBe(targetKey)

    // 2) overwrite=true:更新目标并保留身份
    const overwriteResponse = await importWithArchiveField(target, { bytes: await makeArchive(), filename: 'conflict-true.zip' }, { overwrite: true })
    observed['overwrite-true'] = { code: overwriteResponse.json?.code, summary: importSummary(overwriteResponse) }
    expect(overwriteResponse.json?.code).toBe(200)
    expect(importSummary(overwriteResponse)?.total).toBe(1)
    expect(rowsOf(ALIAS).length, '不得多建新行').toBe(1)
    expect(String(rowsOf(ALIAS)[0]?.resource_key), 'resource_key 身份必须保留').toBe(targetKey)
    expect(String(rowsOf(ALIAS)[0]?.content)).toBe('v2')
    expect(String(rowsOf(ALIAS)[0]?.alias)).toBe(ALIAS)

    // 3) 同包重复两次
    const again1 = await importWithArchiveField(target, { bytes: await makeArchive(), filename: 'conflict-again1.zip' }, { overwrite: true })
    const again2 = await importWithArchiveField(target, { bytes: await makeArchive(), filename: 'conflict-again2.zip' }, { overwrite: true })
    observed['same-package-twice'] = {
      first: importSummary(again1)?.total,
      second: importSummary(again2)?.total,
      rowCount: rowsOf(ALIAS).length,
    }
    expect(rowsOf(ALIAS).length).toBe(1)

    // 4) 同批重复 alias:去重，只落第一条
    const dupResponse = await importWithArchiveField(target, { bytes: duplicateArchive, filename: 'dup-batch.zip' }, { overwrite: true })
    const dupRows = rowsOf('CF_DUP_BATCH')
    observed['duplicate-in-batch'] = {
      code: dupResponse.json?.code ?? null,
      error: dupResponse.json?.error ?? null,
      summary: importSummary(dupResponse),
      rows: dupRows.map((row) => ({ alias: row.alias, content: row.content, resource_key: row.resource_key })),
    }
    writeEvidence('t08-conflict', observed)

    expect.soft(dupRows.length, `同批重复 alias 应只产生一行(实际 ${dupRows.length},摘要 ${JSON.stringify(observed['duplicate-in-batch'] as object)})`).toBe(1)
    if (dupRows.length > 0) {
      expect.soft(String(dupRows[0]?.content), '同批重复应保留第一条').toBe('first')
    }
  })
})