import { expect, test } from '@playwright/test'
import * as fs from 'fs'
import * as path from 'path'
import { runDir, targetB, writeEvidence } from './support/env'
import { buildTransferTarGz, buildTransferZip } from './support/archives'
import { importWithArchiveField } from './support/imports'
import { scopeDevMain, setupSentinelScopes } from './support/scopes'
import { sha256Hex } from './support/net'
import { listAssets } from './support/scopes'

const SENTINEL_NAME = 'qa-sentinel-outside.txt'
const MARKER = 'qa-traversal-sentinel-content'

test.describe('T20 归档路径边界 @t20', () => {
  test('../、反斜杠与重复条目不能逃逸实例目录 @t20', async () => {
    const target = targetB()
    await setupSentinelScopes(target, 't20')

    // 哨兵只放在本轮 runDir 内(绝不触碰系统或用户文件)
    const sentinelPath = path.join(runDir(), SENTINEL_NAME)
    fs.writeFileSync(sentinelPath, MARKER)
    const sentinelHashBefore = sha256Hex(fs.readFileSync(sentinelPath))
    const topLevelBefore = fs.readdirSync(runDir()).sort()

    const maliciousEntries: Record<string, Buffer | string> = {
      [`files/../../../${SENTINEL_NAME}`]: 'traversal-write-attempt',
      'files/..\\..\\..\\qa-sentinel-bslash.txt': 'backslash-write-attempt',
      'files/../../escape-zip.txt': 'plain-traversal',
      '../escape-outside.txt': 'top-escape',
    }
    const configs = [
      {
        alias: 'TRAVERSAL_CFG', name: '路径边界样本', environment_key: scopeDevMain.env,
        pipeline_key: scopeDevMain.pipeline, type: 'text', content: 'traversal',
      },
    ]

    const zipArchive = await buildTransferZip({ configs, extraEntries: maliciousEntries, duplicateEntries: true })
    const zipResponse = await importWithArchiveField(target, { bytes: zipArchive, filename: 'traversal.zip' }, { overwrite: true })

    const tarArchive = await buildTransferTarGz({ configs, extraEntries: maliciousEntries, duplicateEntries: true })
    const tarResponse = await importWithArchiveField(target, { bytes: tarArchive, filename: 'traversal.tar.gz', contentType: 'application/gzip' }, { overwrite: true })

    // 归档内遍历条目不得落地到实例外
    const sentinelHashAfter = sha256Hex(fs.readFileSync(sentinelPath))
    const topLevelAfter = fs.readdirSync(runDir()).sort()
    const newTopLevel = topLevelAfter.filter((name) => !topLevelBefore.includes(name))

    // 扫描本轮 runDir 内新出现的可疑文件(排除正常实例数据)
    const suspicious: string[] = []
    const walk = (dir: string) => {
      for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
        const full = path.join(dir, entry.name)
        if (entry.isDirectory()) walk(full)
        else if (/escape|sentinel-bslash|traversal-write/.test(entry.name) && full !== sentinelPath) {
          suspicious.push(full)
        }
      }
    }
    walk(runDir())

    const assets = await listAssets(target, scopeDevMain)
    const observed = {
      zipResponse: { code: zipResponse.json?.code, error: zipResponse.json?.error ?? null, total: zipResponse.json?.data?.total ?? null },
      tarResponse: { code: tarResponse.json?.code, error: tarResponse.json?.error ?? null, total: tarResponse.json?.data?.total ?? null },
      sentinelHashBefore, sentinelHashAfter,
      newTopLevelEntries: newTopLevel,
      suspiciousFilesInsideRunDir: suspicious,
      assetRows: assets.map((asset) => asset.file_id),
      configLanded: true,
    }
    writeEvidence('t20-traversal', observed)

    expect(sentinelHashAfter, '哨兵文件被归档条目改写').toBe(sentinelHashBefore)
    expect(newTopLevel, 'runDir 顶层出现新文件:路径逃逸迹象').toEqual([])
    expect(suspicious, `实例外出现可疑文件: ${suspicious.join(', ')}`).toEqual([])
    // 遍历条目不得变成资源记录
    expect(assets.every((asset) => !String(asset.file_id).includes('..'))).toBe(true)
  })
})