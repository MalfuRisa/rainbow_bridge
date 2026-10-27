import { expect, test } from '@playwright/test'
import * as fs from 'fs'
import { dbPath, expectedStatePath, readJsonFile, targetB, writeEvidence } from './support/env'
import { listConfigs, resolveUrl, scopeDevMain, snapshotScope } from './support/scopes'
import { request, sha256Hex } from './support/net'
import { expectPageTitle, gotoPage } from './support/pages'

interface ExpectedState {
  recordedAt: string
  sentinelContent: string | null
  textConfig: { alias: string; content: string; description: string }
  imageConfig: { alias: string; content: string }
  imageSha: string | null
  configCount: number
  allConfigs: Record<string, unknown>[]
}

test.describe('T26 备份恢复演练 @recovery', () => {
  test('停机一致性副本启动后:数据与页面一致,前缀正确 @recovery', async ({ page }) => {
    // 恢复阶段:脚本把复制出的恢复实例映射为 B
    const recovered = targetB()
    expect(recovered.prefix, '恢复实例应更换部署前缀').toBe('')
    const expected = readJsonFile<ExpectedState>(expectedStatePath())

    const after = await snapshotScope(recovered, scopeDevMain)
    const sentinel = after.configs.find((item) => item.alias === scopeDevMain.sentinel) as any
    const textRow = after.configs.find((item) => item.alias === expected.textConfig.alias) as any
    const imageRow = after.configs.find((item) => item.alias === expected.imageConfig.alias) as any
    const download = imageRow ? await request(recovered, resolveUrl(recovered, String(imageRow.content)), { method: 'GET' }) : null
    const downloadSha = download && download.status === 200 ? sha256Hex(download.bytes) : null

    await gotoPage(page, recovered, '/')
    await expectPageTitle(page, '项目概览')

    const observed = {
      expectedAt: expected.recordedAt,
      recoveredInstanceDir: recovered.instanceDir,
      dbFileExists: fs.existsSync(dbPath(recovered)),
      dbFileSize: fs.existsSync(dbPath(recovered)) ? fs.statSync(dbPath(recovered)).size : 0,
      sentinel: { expected: expected.sentinelContent, actual: sentinel?.content },
      text: { expected: expected.textConfig, actual: { alias: textRow?.alias, content: textRow?.content, description: textRow?.description } },
      image: { expectedSha: expected.imageSha, actualSha: downloadSha, downloadStatus: download?.status },
      configCount: { expected: expected.configCount, actual: after.configs.length },
      method: '停机一致性副本(复制 A 的 config.yaml 与 data 后启动为新的 B)；不代表运行中热备或转移包等价整库备份',
    }
    writeEvidence('t26-recovery', observed)

    expect(observed.dbFileExists, '恢复实例数据库缺失').toBe(true)
    expect(sentinel?.content, '恢复后哨兵内容不一致').toBe(expected.sentinelContent)
    expect(textRow?.content, '恢复后文本内容不一致').toBe(expected.textConfig.content)
    expect(textRow?.description, '恢复后描述不一致').toBe(expected.textConfig.description)
    expect(downloadSha, '恢复后资源字节不一致').toBe(expected.imageSha)
    expect(after.configs.length, '恢复后配置数量不一致').toBe(expected.configCount)
    await listConfigs(recovered, scopeDevMain)
  })
})
