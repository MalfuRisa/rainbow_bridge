import { expect, test } from '@playwright/test'
import { targetA, writeEvidence } from './support/env'
import { runtimeConfig } from './support/imports'
import {
  createConfigViaPage,
  expectConfigRow,
  gotoPage,
  openEditDialog,
  selectEnvPipelineOnConfigPage,
} from './support/pages'
import { TINY_PNG, TINY_TXT, ensureScope, getConfigDetail, resolveUrl, scopeDevMain } from './support/scopes'
import { request, sha256Hex } from './support/net'
import { configsByAlias } from './support/db'

interface TypeCase {
  typeLabel: string
  typeKey: string
  alias: string
  name: string
  expectedContent: string
  upload?: { name: string; mime: string; bytes: Buffer }
  expectJsonContent?: unknown
}

const TYPE_CASES: TypeCase[] = [
  { typeLabel: '文本', typeKey: 'text', alias: 'QA_TEXT', name: 'QA 文本', expectedContent: 'qa 文本内容' },
  { typeLabel: '多行文本', typeKey: 'textarea', alias: 'QA_TEXTAREA', name: 'QA 多行文本', expectedContent: '第一行\n第二行' },
  { typeLabel: '富文本', typeKey: 'richtext', alias: 'QA_RICHTEXT', name: 'QA 富文本', expectedContent: '<b>加粗</b>' },
  { typeLabel: '整数', typeKey: 'number', alias: 'QA_NUMBER', name: 'QA 整数', expectedContent: '42' },
  { typeLabel: '小数', typeKey: 'decimal', alias: 'QA_DECIMAL', name: 'QA 小数', expectedContent: '3.14' },
  { typeLabel: '布尔值', typeKey: 'boolean', alias: 'QA_BOOLEAN', name: 'QA 布尔', expectedContent: 'true' },
  {
    typeLabel: '键值对',
    typeKey: 'keyvalue',
    alias: 'QA_KEYVALUE',
    name: 'QA 键值对',
    expectedContent: '{"qa_key":"qa_value"}',
    expectJsonContent: { qa_key: 'qa_value' },
  },
  {
    typeLabel: '对象',
    typeKey: 'object',
    alias: 'QA_OBJECT',
    name: 'QA 对象',
    expectedContent: '{"arr":[1,2],"flag":true,"nested":{"k":"v"},"nil":null}',
    expectJsonContent: { arr: [1, 2], flag: true, nested: { k: 'v' }, nil: null },
  },
  { typeLabel: '色彩', typeKey: 'color', alias: 'QA_COLOR', name: 'QA 色彩', expectedContent: '#FF8800' },
  {
    typeLabel: '图片',
    typeKey: 'image',
    alias: 'QA_IMAGE',
    name: 'QA 图片',
    expectedContent: '',
    upload: { name: 'qa-page-image.png', mime: 'image/png', bytes: TINY_PNG },
  },
  {
    typeLabel: '文件',
    typeKey: 'file',
    alias: 'QA_FILE',
    name: 'QA 文件',
    expectedContent: '',
    upload: { name: 'qa-page-file.txt', mime: 'text/plain', bytes: TINY_TXT },
  },
]

test.describe('T03 十一种类型页面创建与运行时读取 @t03 @smoke', () => {
  test('页面逐个创建 11 种类型，重进页面并从 runtime/config 读取 @t03 @smoke', async ({ page }) => {
    test.setTimeout(180_000)
    const target = targetA()
    await ensureScope(target, scopeDevMain)

    await gotoPage(page, target, '/config')
    await selectEnvPipelineOnConfigPage(page, 'QA qa_dev (qa_dev)', 'QA main (main)')

    const created: { typeLabel: string; alias: string }[] = []
    for (const typeCase of TYPE_CASES) {
      await createConfigViaPage(page, {
        name: typeCase.name,
        alias: typeCase.alias,
        typeLabel: typeCase.typeLabel,
        content: typeCase.expectedContent || undefined,
        description: `T03 ${typeCase.typeLabel}`,
        uploadFile: typeCase.upload
          ? { name: typeCase.upload.name, mimeType: typeCase.upload.mime, buffer: typeCase.upload.bytes }
          : undefined,
      })
      created.push({ typeLabel: typeCase.typeLabel, alias: typeCase.alias })
    }

    // 重新进入页面，行仍在
    await gotoPage(page, target, '/config')
    await selectEnvPipelineOnConfigPage(page, 'QA qa_dev (qa_dev)', 'QA main (main)')
    for (const item of created) {
      await expectConfigRow(page, item.alias)
    }

    // runtime/config 读取并逐项核对类型与内容
    const runtime = await runtimeConfig(target, scopeDevMain)
    expect(runtime.json?.code).toBe(200)
    const runtimeConfigs: any[] = runtime.json?.data?.configs ?? []
    const checks: Record<string, unknown> = {}
    const assetProbes: Record<string, unknown> = {}
    const resourceProblems: string[] = []
    for (const typeCase of TYPE_CASES) {
      const found = runtimeConfigs.find((item) => item.alias === typeCase.alias)
      expect(found, `runtime/config 缺少 ${typeCase.alias}`).toBeTruthy()
      expect(found.type, `${typeCase.alias} 类型`).toBe(typeCase.typeKey)

      if (typeCase.expectJsonContent !== undefined) {
        // runtime/config 会把 object/keyvalue 的 content 解析为 JSON 对象返回；detail 返回字符串
        const parsedContent = typeof found.content === 'string' ? JSON.parse(found.content) : found.content
        expect(parsedContent, `${typeCase.alias} JSON 内容`).toEqual(typeCase.expectJsonContent)
        checks[typeCase.alias] = { type: found.type, content: parsedContent };
      } else if (typeCase.upload) {
        // 资源类内容应为可访问路径，且下载字节与原文件一致
        const expectedSha = sha256Hex(typeCase.upload.bytes)
        const detail = await getConfigDetail(target, scopeDevMain, String(found.resource_key))
        const detailContent: string = detail.json?.data?.config?.content ?? ''
        const resolved = resolveUrl(target, detailContent)
        const download = await request(target, resolved, { method: 'GET' })
        const sha = download.status === 200 ? sha256Hex(download.bytes) : null
        assetProbes[typeCase.alias] = {
          url: detailContent,
          resolvedUrl: resolved,
          status: download.status,
          contentType: download.contentType,
          sha,
          expectedSha,
          bodyPreview: download.contentType.includes('json') || download.status !== 200 ? download.text.slice(0, 200) : '',
        }
        checks[typeCase.alias] = { type: found.type, content: detailContent, sha256: expectedSha }
        if (download.status !== 200) { resourceProblems.push(`${typeCase.alias}: status=${download.status} url=${resolved} ct=${download.contentType} body=${download.text.slice(0, 120)}`) }
        else if (sha !== expectedSha) { resourceProblems.push(`${typeCase.alias}: sha 不一致 ${sha} != ${expectedSha}`) }
      } else {
        expect(found.content, `${typeCase.alias} 内容`).toBe(typeCase.expectedContent)
        checks[typeCase.alias] = { type: found.type, content: found.content }
      }
    }

    // 编辑对话框打开时类型与别名不可改
    const dialog = await openEditDialog(page, 'QA_TEXT')
    await expect(dialog.locator('#alias')).toBeDisabled()

    writeEvidence('t03-config-types', {
      created,
      runtimeChecks: checks,
      assetProbes,
      resourceProblems,
      storedContents: {
        QA_IMAGE: configsByAlias(target, scopeDevMain.env, scopeDevMain.pipeline, 'QA_IMAGE')[0]?.content ?? null,
        QA_FILE: configsByAlias(target, scopeDevMain.env, scopeDevMain.pipeline, 'QA_FILE')[0]?.content ?? null,
      },
      runtimeEnvironment: runtime.json?.data?.environment,
    })
    expect.soft(resourceProblems, `资源类型下载异常: ${resourceProblems.join(' | ')}`).toEqual([])
    // 存储层不应携带部署前缀(否则迁移/换前缀部署时资源链接失效)
    const storedImage = String(configsByAlias(target, scopeDevMain.env, scopeDevMain.pipeline, 'QA_IMAGE')[0]?.content ?? '')
    expect.soft(storedImage.startsWith('/api/v1/asset/file/'), `数据库存储了带部署前缀的资源路径: ${storedImage}`).toBe(true)
  })
})