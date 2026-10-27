import { expect, test } from '@playwright/test'
import { targetA, writeEvidence } from './support/env'
import { buildTransferZip } from './support/archives'
import { importWithArchiveField } from './support/imports'
import { scopeDevMain, setupSentinelScopes, snapshotAllScopes, diffSnapshots } from './support/scopes'
import { configsByAlias } from './support/db'

const VALID_ALIAS = 'IMP_ATOMIC_VALID'

test.describe('T07 导入校验原子性 @t07 @smoke', () => {
  test('合法项+非法项混合包:业务失败且无任何部分写入 @t07 @smoke', async () => {
    const target = targetA()
    await setupSentinelScopes(target, 't07')
    const before = await snapshotAllScopes(target)

    const archive = await buildTransferZip({
      configs: [
        {
          alias: VALID_ALIAS,
          name: '合法第一项',
          environment_key: scopeDevMain.env,
          pipeline_key: scopeDevMain.pipeline,
          type: 'text',
          content: '第一项内容',
          description: 'atomic-valid',
        },
        {
          alias: 'IMP_ATOMIC_INVALID',
          name: '非法第二项',
          environment_key: scopeDevMain.env,
          pipeline_key: scopeDevMain.pipeline,
          type: 'object',
          // 顶层数组:已定义契约必须拒绝
          content: [1, 2, 3],
        },
      ],
    })

    const results: Record<string, unknown> = {}
    for (const overwrite of [false, true]) {
      const response = await importWithArchiveField(
        target,
        { bytes: archive, filename: `atomic-${overwrite}.zip` },
        { overwrite },
      )
      results[`overwrite-${overwrite}`] = {
        code: response.json?.code,
        error: response.json?.error ?? null,
        total: response.json?.data?.total ?? null,
      }
      // 业务失败(不是先写第一项再报错)
      expect(response.json?.code, `overwrite=${overwrite} 应整体失败: ${response.text.slice(0, 300)}`).not.toBe(200)
    }

    // 新项不存在、三组旧配置与资源完整
    const after = await snapshotAllScopes(target)
    const differences = diffSnapshots(before, after)
    const validRows = configsByAlias(target, scopeDevMain.env, scopeDevMain.pipeline, VALID_ALIAS)
    writeEvidence('t07-import-atomic', { results, validRows, differences })
    expect(validRows.length, '合法第一项不得被部分写入').toBe(0)
    expect(differences, `哨兵范围被修改: ${differences.join('; ')}`).toEqual([])
  })
})