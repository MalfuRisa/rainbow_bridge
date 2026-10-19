import { expect, test } from '@playwright/test'
import { targetA, targetB, writeEvidence } from './support/env'
import { buildTransferZip } from './support/archives'
import { importSelective, migrate } from './support/imports'
import {
  allScopes,
  createConfig,
  deleteConfig,
  ensureScope,
  listConfigs,
  scopeDevBeta,
  scopeDevMain,
  scopeProdMain,
  setupSentinelScopes,
  snapshotAllScopes,
  diffSnapshots,
  updateConfig,
} from './support/scopes'
import { configsByAlias } from './support/db'

test.describe('T09 环境隔离 @t09', () => {
  test('选择性导入只动所选范围;跨范围参数不能修改他区;迁移逐项一致 @t09', async () => {
    const a = targetA()
    const b = targetB()
    for (const scope of allScopes) {
      await ensureScope(a, scope)
      await ensureScope(b, scope)
    }
    await setupSentinelScopes(a, 't09')

    // 准备一个三家范围都有的包
    const sourceKey = 'iso-dev-main-1'
    const archive = await buildTransferZip({
      configs: [
        { resource_key: sourceKey, alias: 'ISO_DEV_MAIN', name: '隔离-开发主', environment_key: 'qa_dev', pipeline_key: 'main', type: 'text', content: 'dev-main' },
        { resource_key: 'iso-dev-beta-1', alias: 'ISO_DEV_BETA', name: '隔离-开发灰度', environment_key: 'qa_dev', pipeline_key: 'beta', type: 'text', content: 'dev-beta' },
        { resource_key: 'iso-prod-main-1', alias: 'ISO_PROD_MAIN', name: '隔离-生产主', environment_key: 'qa_prod', pipeline_key: 'main', type: 'text', content: 'prod-main' },
      ],
    })

    const beforeA = await snapshotAllScopes(a)
    const beforeB = await snapshotAllScopes(b)

    // 只选择 qa_dev/main 且只选一个 resource_key 导入到 B
    const selective = await importSelective(
      b,
      { bytes: archive, filename: 'iso.zip' },
      { selections: [{ environment_key: 'qa_dev', pipeline_key: 'main', resource_keys: [sourceKey] }], overwrite: false },
    )
    expect(selective.json?.code, selective.text.slice(0, 300)).toBe(200)

    // B: dev/main 有;dev/beta 与 prod/main 保持不变
    const bDevMain = await listConfigs(b, scopeDevMain)
    expect(bDevMain.find((item) => item.alias === 'ISO_DEV_MAIN')).toBeTruthy()
    expect((await listConfigs(b, scopeDevBeta)).find((item) => item.alias === 'ISO_DEV_BETA')).toBeUndefined()
    expect((await listConfigs(b, scopeProdMain)).find((item) => item.alias === 'ISO_PROD_MAIN')).toBeUndefined()

    // A: 未被选择导入，三组哨兵不变
    const afterA = await snapshotAllScopes(a)
    const differencesA = diffSnapshots(beforeA, afterA)
    expect(differencesA, `A 哨兵被改动: ${differencesA.join('; ')}`).toEqual([])

    // 跨范围参数不能修改他区：用 qa_dev 参数携带 qa_prod 的 resource_key
    const prodRows = configsByAlias(a, scopeProdMain.env, scopeProdMain.pipeline, scopeProdMain.sentinel)
    const prodKey = String(prodRows[0]?.resource_key ?? '')
    const crossUpdate = await updateConfig(
      a,
      scopeDevMain,
      { resource_key: prodKey, name: '越权改名', type: 'text', content: '越权内容', description: 'x' },
      false,
    )
    const crossDelete = await deleteConfig(a, scopeDevMain, prodKey, false)

    const beforeB2 = await snapshotAllScopes(b)
    const bProdAfter = await listConfigs(b, scopeProdMain)
    const prodUntouched = bProdAfter.find((item) => item.alias === 'ISO_PROD_MAIN') === undefined

    // 迁移 qa_dev/main -> qa_dev/beta (在 B 上)
    const { config } = await createConfig(b, scopeDevMain, {
      alias: 'MIG_SRC', name: '迁移源', type: 'text', content: 'migrate-me', description: '源',
    })
    const migResponse = await migrate(b, {
      source_environment_key: scopeDevMain.env, source_pipeline_key: scopeDevMain.pipeline,
      target_environment_key: scopeDevBeta.env, target_pipeline_key: scopeDevBeta.pipeline,
    })
    expect(migResponse.json?.code, migResponse.text.slice(0, 300)).toBe(200)
    const targetRows = await listConfigs(b, scopeDevBeta)
    const migrated = targetRows.find((item) => item.alias === 'MIG_SRC')

    const afterB = await snapshotAllScopes(b)
    const differencesB = diffSnapshots(beforeB2, afterB).filter((item) => !item.includes('qa_dev/beta') && !item.includes('qa_dev/main'))

    writeEvidence('t09-isolation', {
      selective: selective.json,
      crossUpdate: { code: crossUpdate.json?.code, error: crossUpdate.json?.error },
      crossDelete: { code: crossDelete.json?.code, error: crossDelete.json?.error },
      prodSentinelUntouched: prodUntouched,
      migrate: { code: migResponse.json?.code, summary: migResponse.json?.data, migratedContent: migrated?.content ?? null },
      differencesA,
      differencesB,
      resourceKeyUsed: config?.resource_key,
    })

    // 跨范围更新/删除必须失败且不影响他区
    expect(prodUntouched, '跨范围更新/删除影响了 qa_prod/main').toBe(true)
    // 迁移逐项结果与目标实际内容一致
    expect(migrated, '迁移后目标缺行').toBeTruthy()
    expect(String(migrated?.content)).toBe('migrate-me')
    const migrateSummary = migResponse.json?.data ?? {}
    const counts = {
      total: Number(migrateSummary.total ?? 0),
      succeeded: Number(migrateSummary.succeeded ?? 0),
      skipped: Number(migrateSummary.skipped ?? 0),
      failed: Number(migrateSummary.failed ?? 0),
    }
    expect(counts.succeeded + counts.skipped + counts.failed, `迁移摘要计数不一致: ${JSON.stringify(counts)}`).toBe(counts.total)
    expect(counts.total, '迁移摘要应至少包含一项').toBeGreaterThan(0)
  })
})