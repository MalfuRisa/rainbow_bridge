import * as fs from 'fs'
import * as path from 'path'
import { expect, test } from '@playwright/test'
import { targetB, uploadsDir, writeEvidence } from './support/env'
import { buildTransferZip } from './support/archives'
import { importWithArchiveField } from './support/imports'
import { TINY_PNG, scopeDevMain, setupSentinelScopes } from './support/scopes'
import { configsByAlias, dumpTable } from './support/db'

const FIXED_ID = 'qa-retry-fid-01'
const ALIAS = 'RETRY_CFG'

function archive() {
  return buildTransferZip({
    configs: [
      {
        alias: ALIAS, name: '重试样本', environment_key: scopeDevMain.env, pipeline_key: scopeDevMain.pipeline,
        type: 'image', content: `asset://${FIXED_ID}`,
      },
    ],
    files: [{ fileId: FIXED_ID, fileName: 'retry.png', bytes: TINY_PNG }],
    meta: { assets: [{ file_id: FIXED_ID, environment_key: scopeDevMain.env, pipeline_key: scopeDevMain.pipeline, file_name: 'retry.png', content_type: 'image/png', file_size: TINY_PNG.length, remark: '' }] },
  })
}

test.describe('T24 部分状态重试 @t24', () => {
  test('磁盘失败造成的部分状态:重试同包是否可恢复、是否产生孤儿 @t24', async () => {
    const target = targetB()
    await setupSentinelScopes(target, 't24')

    const blocker = path.join(uploadsDir(target), FIXED_ID)
    fs.mkdirSync(path.dirname(blocker), { recursive: true })
    fs.writeFileSync(blocker, 'blocker')
    let firstImport: Awaited<ReturnType<typeof importWithArchiveField>> | null = null
    try {
      firstImport = await importWithArchiveField(target, { bytes: await archive(), filename: 'retry-1.zip' }, { overwrite: true })
    } finally {
      fs.rmSync(blocker, { force: true })
    }
    const afterFirst = {
      configRows: configsByAlias(target, scopeDevMain.env, scopeDevMain.pipeline, ALIAS).length,
      assetRows: dumpTable(target, 'asset', [`file_id=${FIXED_ID}`]).count,
      assetFile: fs.existsSync(path.join(uploadsDir(target), FIXED_ID, 'retry.png')),
      importCode: firstImport?.json?.code ?? null,
      importTotal: firstImport?.json?.data?.total ?? null,
    }

    // 重试同包(故障已复位)
    expect(firstImport?.json?.msg).toBe('partial')
    expect(firstImport?.json?.data?.resource_import).toMatchObject({status:'partial',expected:1,failed:1,restored:0,reused:0})
    const secondImport = await importWithArchiveField(target, { bytes: await archive(), filename: 'retry-2.zip' }, { overwrite: true })
    const afterSecond = {
      configRows: configsByAlias(target, scopeDevMain.env, scopeDevMain.pipeline, ALIAS).length,
      assetRows: dumpTable(target, 'asset', [`file_id=${FIXED_ID}`]).count,
      assetFile: fs.existsSync(path.join(uploadsDir(target), FIXED_ID, 'retry.png')),
      importCode: secondImport.json?.code ?? null,
      importTotal: secondImport.json?.data?.total ?? null,
    }

    expect(secondImport.json?.data?.resource_import).toMatchObject({status:'complete',expected:1,restored:1,failed:0})
    // 孤儿扫描:该 file_id 目录与库中资产记录的对应关系
    const orphanDetails = {
      dirExists: fs.existsSync(path.join(uploadsDir(target), FIXED_ID)),
      dbRowExists: afterSecond.assetRows > 0,
    }
    const orphan = orphanDetails.dirExists && !orphanDetails.dbRowExists

    const observed = {
      afterFirst,
      afterSecond,
      orphan,
      recoverable: afterSecond.assetFile && afterSecond.configRows === 1,
      noDuplicateRows: afterSecond.configRows === 1,
      verdict: afterSecond.assetFile
        ? '部分状态可通过重试恢复'
        : '部分状态未能恢复:需要独立核查(记录为缺陷/能力缺口)',
    }
    writeEvidence('t24-retry-partial', observed)

    if (afterFirst.configRows > 0 && firstImport?.json?.code === 200) {
      test.info().annotations.push({ type: 'observed', description: '首次配置提交、资源部分失败；已检查 resource_import 字段' })
    }
    expect(observed.noDuplicateRows, `重试产生重复配置行: ${afterSecond.configRows}`).toBe(true)
  })
})