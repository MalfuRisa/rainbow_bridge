import { expect, test } from '@playwright/test'
import { configsByAlias } from './support/db'
import { expectedStatePath, readJsonFile, targetA, writeEvidence } from './support/env'
import { listConfigs, resolveUrl, scopeDevMain, snapshotScope, updateConfig } from './support/scopes'
import { request, sha256Hex } from './support/net'
import { expectPageTitle, gotoPage } from './support/pages'

interface ExpectedState {
  sentinelContent: string | null
  textConfig: { alias: string; content: string; description: string }
  imageConfig: { alias: string; content: string }
  imageSha: string | null
  configCount: number
  allConfigs: Record<string, unknown>[]
}

test.describe('T25 持久化(重启后核对) @postrestart', () => {
  test('同一配置重启后:环境/渠道/内容/description 与资源字节保留 @postrestart', async ({ page }) => {
    const target = targetA()
    const expected = readJsonFile<ExpectedState>(expectedStatePath())

    const after = await snapshotScope(target, scopeDevMain)
    const sentinel = after.configs.find((item) => item.alias === scopeDevMain.sentinel) as any
    const textRow = after.configs.find((item) => item.alias === expected.textConfig.alias) as any
    const imageRow = after.configs.find((item) => item.alias === expected.imageConfig.alias) as any

    const download = imageRow ? await request(target, resolveUrl(target, String(imageRow.content)), { method: 'GET' }) : null
    const downloadSha = download && download.status === 200 ? sha256Hex(download.bytes) : null

    // 页面可用性(前缀正确)
    await gotoPage(page, target, '/')
    await expectPageTitle(page, '项目概览')

    const observed = {
      expectedAt: expected.recordedAt,
      sentinel: { expected: expected.sentinelContent, actual: sentinel?.content },
      text: { expected: expected.textConfig, actual: { alias: textRow?.alias, content: textRow?.content, description: textRow?.description } },
      image: { expectedSha: expected.imageSha, actualSha: downloadSha, downloadStatus: download?.status },
      configCount: { expected: expected.configCount, actual: after.configs.length },
      environmentScope: scopeDevMain.label,
    }
    writeEvidence('t25-postrestart', observed)

    expect(sentinel?.content, '重启后哨兵内容变化').toBe(expected.sentinelContent)
    expect(textRow?.content, '重启后文本内容变化').toBe(expected.textConfig.content)
    expect(textRow?.description, '重启后描述未保留').toBe(expected.textConfig.description)
    expect(downloadSha, '重启后资源字节变化').toBe(expected.imageSha)
    const stored = configsByAlias(target, scopeDevMain.env, scopeDevMain.pipeline, expected.imageConfig.alias)[0]
    expect(String(stored?.content)).toMatch(/^\/api\/v1\/asset\/file\//)
    expect(String(stored?.content)).not.toContain('/rainbow-bridge')
    expect(after.configs.length, '重启后配置数量变化').toBe(expected.configCount)
    await listConfigs(target, scopeDevMain)
  })
})