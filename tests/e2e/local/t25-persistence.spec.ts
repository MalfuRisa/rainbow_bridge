import { expect, test } from '@playwright/test'
import * as fs from 'fs'
import { expectedStatePath, targetA, writeEvidence } from './support/env'
import { TINY_PNG, createConfig, ensureScope, listConfigs, resolveUrl, scopeDevMain, setupSentinelScopes, snapshotScope, uploadAsset } from './support/scopes'
import { request, sha256Hex } from './support/net'
import { createConfigViaPage, gotoPage, selectEnvPipelineOnConfigPage } from './support/pages'

const TEXT_ALIAS = 'PERSIST_CFG'
const IMAGE_ALIAS = 'PERSIST_IMG'

test.describe('T25 持久化(记录期望状态) @t25', () => {
  test('页面正常操作后状态落盘记录,供重启后核对 @t25', async ({ page }) => {
    const target = targetA()
    await ensureScope(target, scopeDevMain)
    await setupSentinelScopes(target, 't25')

    // 通过页面创建一条配置(真实页面操作)
    const current = await listConfigs(target, scopeDevMain)
    if (!current.find((item) => item.alias === TEXT_ALIAS)) {
      await gotoPage(page, target, '/config')
      await selectEnvPipelineOnConfigPage(page, 'QA qa_dev (qa_dev)', 'QA main (main)')
      await createConfigViaPage(page, {
        name: '持久化文本', alias: TEXT_ALIAS, typeLabel: '文本', content: 'persist-content', description: 'persist-desc',
      })
    }

    // 资源 + 图片配置
    const upload = await uploadAsset(target, scopeDevMain, {
      fileName: 'persist.png', contentType: 'image/png', bytes: TINY_PNG, remark: 'T25',
    })
    const afterText = await listConfigs(target, scopeDevMain)
    if (!afterText.find((item) => item.alias === IMAGE_ALIAS)) {
      await createConfig(target, scopeDevMain, {
        alias: IMAGE_ALIAS, name: '持久化图片', type: 'image', content: upload.reference, description: 'persist-image',
      })
    }

    const scopeSnapshot = await snapshotScope(target, scopeDevMain)
    const textRow = scopeSnapshot.configs.find((item) => item.alias === TEXT_ALIAS) as any
    const imageUrl = String((scopeSnapshot.configs.find((item) => item.alias === IMAGE_ALIAS) as any)?.content ?? '')
    const resolvedImageUrl = imageUrl ? resolveUrl(target, imageUrl) : ''
    const download = resolvedImageUrl ? await request(target, resolvedImageUrl, { method: 'GET' }) : null
    const downloadSha = download && download.status === 200 ? sha256Hex(download.bytes) : null

    const expected = {
      recordedAt: new Date().toISOString(),
      scope: scopeDevMain.label,
      sentinelContent: (scopeSnapshot.configs.find((item) => item.alias === scopeDevMain.sentinel) as any)?.content ?? null,
      textConfig: { alias: textRow?.alias, content: textRow?.content, description: textRow?.description },
      imageConfig: {
        alias: IMAGE_ALIAS,
        content: imageUrl,
        resolvedUrl: resolvedImageUrl,
        downloadStatus: download?.status ?? null,
        contentType: download?.contentType ?? null,
        bodyPreview: download && download.contentType.includes('json') ? download.text.slice(0, 160) : '',
      },
      imageSha: downloadSha,
      configCount: scopeSnapshot.configs.length,
      allConfigs: scopeSnapshot.configs,
      expectedUploadSha: upload.sha256,
    }
    fs.writeFileSync(expectedStatePath(), JSON.stringify(expected, null, 2))
    writeEvidence('t25-persistence-record', expected)

    expect(textRow, '持久化文本配置应在库').toBeTruthy()
    expect(textRow?.description, '描述应被保留').toBe('persist-desc')
    // 资源字节核验:HTTP 状态 + 内容类型 + 哈希三者同时成立才算成功
    expect.soft(download?.contentType ?? '', `资源返回内容类型异常(HTTP=${download?.status} url=${resolvedImageUrl} body=${expected.imageConfig.bodyPreview})`).toContain('image/')
    expect.soft(downloadSha, `资源字节与上传不一致(HTTP=${download?.status})`).toBe(upload.sha256)
  })
})