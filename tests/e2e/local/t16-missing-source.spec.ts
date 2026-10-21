import { expect, test } from '@playwright/test'
import { publishRuntimeSnapshot } from './support/runtime'
import * as fs from 'fs'
import * as path from 'path'
import { targetA, uploadsDir, writeEvidence } from './support/env'
import { readZipEntries } from './support/archives'
import { exportSelective, runtimeStatic } from './support/imports'
import { TINY_PNG, createConfig, ensureScope, getConfigDetail, resolveUrl, scopeDevMain, setupSentinelScopes, uploadAsset } from './support/scopes'
import { request, sha256Hex } from './support/net'

test.describe('T16 源文件消失 @t16', () => {
  test('删除实例内资源文件后:下载/静态导出/转移导出行为记录 @t16', async () => {
    const target = targetA()
    await ensureScope(target, scopeDevMain)
    await setupSentinelScopes(target, 't16')

    // 直接登记一条引用占位 file_id 的图片配置，再在磁盘上放入文件，最后删除该文件
    const fileName = 'ghost.png'
    const uploaded = await uploadAsset(target, scopeDevMain, {fileName,contentType:'image/png',bytes:TINY_PNG})
    const fileId = uploaded.fileId
    const diskDir = path.join(uploadsDir(target),fileId)

    const { config } = await createConfig(target, scopeDevMain, {
      alias: 'MISSING_SRC_CFG', name: '源文件消失样本', type: 'image', content: `asset://${fileId}`,
    })
    const resourceKey = config!.resource_key
    const detailBefore = await getConfigDetail(target, scopeDevMain, resourceKey)
    const contentUrl: string = detailBefore.json?.data?.config?.content ?? ''
    const downloadBefore = await request(target, resolveUrl(target, contentUrl), { method: 'GET' })
    expect(downloadBefore.status, '删除前资源应可下载').toBe(200)

    await publishRuntimeSnapshot(target, scopeDevMain)
    try {
    // 删除本轮实例内的资源文件(仅限本实例)
    fs.rmSync(path.join(diskDir, fileName), { force: true })

    const downloadAfter = await request(target, resolveUrl(target, contentUrl), { method: 'GET' })
    const staticExport = await runtimeStatic(target, scopeDevMain)
    const staticEntries = staticExport.status === 200 ? await readZipEntries(staticExport.bytes) : new Map()
    const staticHasAsset = [...staticEntries.entries()].some(([name, bytes]) => name.startsWith('assets/') && bytes.equals(TINY_PNG))

    const transferExport = await exportSelective(target, [
      { environment_key: scopeDevMain.env, pipeline_key: scopeDevMain.pipeline, resource_keys: [resourceKey] },
    ], 'zip')
    const transferEntries = transferExport.status === 200 ? await readZipEntries(transferExport.bytes) : new Map()
    const transferHasAsset = [...transferEntries.keys()].some((name) => name.includes(fileId))

    const observed = {
      fileId, fileName, contentUrl,
      downloadBefore: downloadBefore.status,
      downloadAfter: { status: downloadAfter.status, contentType: downloadAfter.contentType, body: downloadAfter.text.slice(0, 120) },
      staticExport: { status: staticExport.status, hasAssetEntry: staticHasAsset, entries: [...staticEntries.keys()].slice(0, 6) },
      transferExport: { status: transferExport.status, hasAssetEntry: transferHasAsset, entries: [...transferEntries.keys()].slice(0, 6) },
      gaps: {
        staticExportMissingAssetWithoutWarning: staticExport.status === 200 && !staticHasAsset,
        transferExportMissingAssetWithoutWarning: transferExport.status === 200 && !transferHasAsset,
      },
    }
    writeEvidence('t16-missing-source', observed)

    // 明确失败:HTTP 状态或业务 code 至少其一必须表达失败
    const downloadFailedClearly = downloadAfter.status !== 200 || downloadAfter.json?.code !== 200
    expect(downloadFailedClearly, `资源文件已删除但响应既不报错也不带业务错误: HTTP=${downloadAfter.status} code=${downloadAfter.json?.code} ct=${downloadAfter.contentType} body=${downloadAfter.text.slice(0, 120)}`).toBe(true)
    // 仅 HTTP 状态层面的语义(200 + 业务错误体)作为观察项记录,供报告评估
    if (downloadAfter.status === 200 && downloadAfter.json?.code !== 200) {
      test.info().annotations.push({ type: 'observed', description: `资源缺失返回 HTTP 200 + code=${downloadAfter.json?.code}(需检查响应体才能识别失败)` })
    }
    expect(downloadAfter.status).toBe(404)
    // The published snapshot owns its resource copy; transfer still detects missing editor files.
    expect(staticExport.status).toBe(200)
    expect(staticHasAsset).toBe(true)
    expect(transferExport.status).toBe(409)
    expect(transferExport.json?.code).toBe(409)
    } finally { fs.writeFileSync(path.join(diskDir,fileName),TINY_PNG) }
  })
})
