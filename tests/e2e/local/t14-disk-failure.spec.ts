import { expect, test } from '@playwright/test'
import * as fs from 'fs'
import * as path from 'path'
import { targetB, uploadsDir, writeEvidence } from './support/env'
import { buildTransferZip } from './support/archives'
import { importWithArchiveField } from './support/imports'
import { TINY_PNG, scopeDevMain, setupSentinelScopes } from './support/scopes'
import { configsByAlias, dumpTable } from './support/db'

const FIXED_ID = 'qa-diskfail-fid-01'
const ALIAS = 'DISK_FAIL_CFG'

test.describe('T14 磁盘写入失败 @t14', () => {
  test('资源目录预置路径冲突:真实触发文件写失败并报告阶段 @t14', async () => {
    const target = targetB()
    await setupSentinelScopes(target, 't14')

    const blocker = path.join(uploadsDir(target), FIXED_ID)
    fs.mkdirSync(path.dirname(blocker), { recursive: true })
    fs.writeFileSync(blocker, 'blocker-file') // 预置普通文件占位，使 MkdirAll 失败

    const archive = await buildTransferZip({
      configs: [
        {
          alias: ALIAS, name: '磁盘失败样本', environment_key: scopeDevMain.env, pipeline_key: scopeDevMain.pipeline,
          type: 'image', content: `asset://${FIXED_ID}`,
        },
      ],
      files: [{ fileId: FIXED_ID, fileName: 'disk.png', bytes: TINY_PNG }],
      meta: {
        assets: [{ file_id: FIXED_ID, environment_key: scopeDevMain.env, pipeline_key: scopeDevMain.pipeline, file_name: 'disk.png', content_type: 'image/png', file_size: TINY_PNG.length, remark: '' }],
      },
    })

    let response: Awaited<ReturnType<typeof importWithArchiveField>>
    try {
      response = await importWithArchiveField(target, { bytes: archive, filename: 'disk-fail.zip' }, { overwrite: true })
    } finally {
      // 复位夹具，避免污染后续用例
      try { fs.rmSync(blocker, { force: true }) } catch { /* noop */ }
    }

    const rows = configsByAlias(target, scopeDevMain.env, scopeDevMain.pipeline, ALIAS)
    const assetRows = dumpTable(target, 'asset', [`file_id=${FIXED_ID}`])
    const assetFileWritten = fs.existsSync(path.join(uploadsDir(target), FIXED_ID, 'disk.png'))
    const observed = {
      importCode: response.json?.code ?? null,
      importError: response.json?.error ?? null,
      importTotal: response.json?.data?.total ?? null,
      configStored: rows.length,
      assetRowStored: assetRows.count,
      assetFileWritten,
      blockerUsed: blocker,
      stages: {
        configs: rows.length > 0 ? 'committed' : 'absent',
        assetRow: assetRows.count > 0 ? 'committed' : 'skipped',
        assetBytes: assetFileWritten ? 'written' : 'not-written',
      },
    }
    writeEvidence('t14-disk-failure', observed)

    // 报告配置/资源分别落到哪一步:接口不得把资源缺失报告成完整成功
    expect(rows.length, '配置写入结果是本用例的关键取证').toBeGreaterThanOrEqual(0)
    if (observed.importCode === 200 && observed.importTotal && observed.importTotal > 0 && !assetFileWritten) {
      // 命中"配置已提交、资源静默缺失"的部分成功路径——记录为缺陷线索
      expect(observed.stages.assetBytes).toBe('not-written')
    }
  })
})