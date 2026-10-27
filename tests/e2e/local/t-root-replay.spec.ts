import { expect, test } from '@playwright/test'
import { publishRuntimeSnapshot } from './support/runtime'
import * as http from 'http'
import { readZipEntries, findEntry } from './support/archives'
import { exportSelective, importSelective, runtimeConfig, runtimeStatic } from './support/imports'
import { getJson, postJson, request, sha256Hex } from './support/net'
import { expectPageTitle, gotoPage, PAGE_ROUTES } from './support/pages'
import {
  TINY_PNG,
  createConfig,
  ensureScope,
  getConfigDetail,
  resolveUrl,
  scopeDevMain,
  updateConfig,
  uploadAsset,
} from './support/scopes'
import { targetRootA, targetRootB, writeEvidence } from './support/env'

/**
 * 根路径部署重跑(PRD §3.2 第 5 条):独立实例目录 + web-root 构建，
 * 至少覆盖 T01–T04、T11 与资源访问。
 */
test.describe('根路径前缀重跑 @root', () => {
  test('T01-root 启动与未知 API @root', async ({ page }) => {
    const target = targetRootA()
    await gotoPage(page, target, '/')
    await expectPageTitle(page, '项目概览')
    const ping = await getJson(target, '/ping')
    expect(ping.text).toContain('pong')
    const version = await getJson(target, '/api/v1/version')
    expect(version.json?.code).toBe(200)
    const unknown = await request(target, '/api/v1/root-unknown', { method: 'GET' })
    const unknownPost = await postJson(target, '/api/v1/root-unknown', {})
    const unknownHead = await request(target, '/api/v1/root-unknown', { method: 'HEAD' })
    expect(unknownHead.status).toBe(404)
    expect(unknownHead.bytes.length).toBe(0)
    writeEvidence('t01-root', { ping: ping.text, version: version.json, unknown: { status: unknown.status, contentType: unknown.contentType }, unknownPost: { status: unknownPost.status } })
    expect(unknown.status).toBe(404)
    expect(unknownPost.status).toBe(404)
  })

  test('T02-root 六个页面与刷新 @root', async ({ page }) => {
    const target = targetRootA()
    const failedRequests: string[] = []
    page.on('requestfailed', (failed) => failedRequests.push(`${failed.url()} ${failed.failure()?.errorText ?? ''}`))
    for (const route of PAGE_ROUTES) {
      await gotoPage(page, target, route.path)
      await expectPageTitle(page, route.title)
    }
    for (const path of ['/config', '/import-export']) {
      await page.goto(`${target.origin}${path}`, { waitUntil: 'domcontentloaded' })
      await page.reload({ waitUntil: 'domcontentloaded' })
      expect((await page.locator('#app').innerHTML()).length).toBeGreaterThan(100)
    }
    writeEvidence('t02-root', { failedRequests })
    const sameOriginFailures = failedRequests.filter((item) => item.startsWith(target.origin) && !item.includes('ERR_ABORTED'))
    const externalFailures = failedRequests.filter((item) => !item.startsWith(target.origin))
    expect(sameOriginFailures, `根路径同源失败请求: ${sameOriginFailures.join('; ')}`).toEqual([])
    expect.soft(externalFailures, `根路径外部依赖失败(内网不可达): ${externalFailures.join('; ')}`).toEqual([])
  })

  test('T03-root 类型创建与资源访问 @root', async ({ page }) => {
    const target = targetRootA()
    await ensureScope(target, scopeDevMain)

    await gotoPage(page, target, '/config')
    const { selectEnvPipelineOnConfigPage, createConfigViaPage, expectConfigRow } = await import('./support/pages')
    await selectEnvPipelineOnConfigPage(page, 'QA qa_dev (qa_dev)', 'QA main (main)')
    const alias = 'ROOT_TEXT'
    const rows = await (await import('./support/scopes')).listConfigs(target, scopeDevMain)
    if (!rows.find((item) => item.alias === alias)) {
      await createConfigViaPage(page, { name: '根路径文本', alias, typeLabel: '文本', content: 'root-text' })
    } else {
      await expectConfigRow(page, alias)
    }

    const upload = await uploadAsset(target, scopeDevMain, { fileName: 'root.png', contentType: 'image/png', bytes: TINY_PNG })
    const imageAlias = 'ROOT_IMAGE'
    const after = await (await import('./support/scopes')).listConfigs(target, scopeDevMain)
    if (!after.find((item) => item.alias === imageAlias)) {
      await createConfig(target, scopeDevMain, { alias: imageAlias, name: '根路径图片', type: 'image', content: upload.reference })
    }

    await publishRuntimeSnapshot(target, scopeDevMain)
    const runtime = await runtimeConfig(target, scopeDevMain)
    const imageRuntime = (runtime.json?.data?.configs ?? []).find((item: any) => item.alias === imageAlias)
    const detail = await getConfigDetail(target, scopeDevMain, String(imageRuntime?.resource_key ?? ''))
    const contentPath: string = detail.json?.data?.config?.content ?? ''
    const resolved = resolveUrl(target, contentPath)
    const download = await request(target, resolved, { method: 'GET' })
    const stored = (await import('./support/db')).configsByAlias(target, scopeDevMain.env, scopeDevMain.pipeline, imageAlias)[0]?.content ?? null
    writeEvidence('t03-root', {
      contentPath,
      resolvedUrl: resolved,
      downloadStatus: download.status,
      contentType: download.contentType,
      bodyPreview: download.contentType.includes('json') ? download.text.slice(0, 160) : '',
      sha: download.status === 200 ? sha256Hex(download.bytes) : null,
      expectedSha: upload.sha256,
      storedContent: stored,
    })
    // 根路径部署:资源 URL 不带 /rainbow-bridge 前缀
    expect.soft(contentPath.startsWith('/api/v1/asset/file/'), `根路径 content 应无前缀: ${contentPath}`).toBe(true)
    expect.soft(String(stored), `根路径数据库存储不应带前缀: ${stored}`).toBe(contentPath)
    expect.soft(download.status, `根路径资源不可下载: url=${resolved} ct=${download.contentType} body=${download.text.slice(0, 120)}`).toBe(200)
    if (download.status === 200) {
      expect(sha256Hex(download.bytes)).toBe(upload.sha256)
    }
  })

  test('T04-root 更新/清空与身份不可变 @root', async () => {
    const target = targetRootA()
    await ensureScope(target, scopeDevMain)
    const alias = 'ROOT_UPDATE'
    const rows = await (await import('./support/scopes')).listConfigs(target, scopeDevMain)
    let resourceKey = String(rows.find((item) => item.alias === alias)?.resource_key ?? '')
    if (!resourceKey) {
      const { config } = await createConfig(target, scopeDevMain, { alias, name: '根路径更新', type: 'text', content: 'v1', description: '有描述' })
      resourceKey = config!.resource_key
    }
    await updateConfig(target, scopeDevMain, { resource_key: resourceKey, name: '根路径更新', type: 'text', content: 'v1', description: '' })
    const cleared = await getConfigDetail(target, scopeDevMain, resourceKey)
    await updateConfig(target, scopeDevMain, { resource_key: resourceKey, alias: 'ROOT_RENAMED', name: '根路径更新', type: 'text', content: 'v1', description: '新描述' })
    const after = await getConfigDetail(target, scopeDevMain, resourceKey)
    writeEvidence('t04-root', { descriptionAfterClear: cleared.json?.data?.config?.description ?? '', aliasAfterAttempt: after.json?.data?.config?.alias })
    expect(cleared.json?.data?.config?.description ?? '', '根路径描述清空').toBe('')
    expect(after.json?.data?.config?.alias, '根路径 alias 不可变').toBe(alias)
  })

  test('T11-root 导出往返与静态托管 @root', async () => {
    const a = targetRootA()
    const b = targetRootB()
    await ensureScope(a, scopeDevMain)
    await ensureScope(b, scopeDevMain)

    const upload = await uploadAsset(a, scopeDevMain, { fileName: 'root-rt.png', contentType: 'image/png', bytes: TINY_PNG })
    const alias = 'ROOT_RT_IMAGE'
    const rows = await (await import('./support/scopes')).listConfigs(a, scopeDevMain)
    let resourceKey = String(rows.find((item) => item.alias === alias)?.resource_key ?? '')
    if (!resourceKey) {
      const { config } = await createConfig(a, scopeDevMain, { alias, name: '根路径往返', type: 'image', content: upload.reference, description: 'root-roundtrip' })
      resourceKey = config!.resource_key
    }

    const exportResponse = await exportSelective(a, [{ environment_key: scopeDevMain.env, pipeline_key: scopeDevMain.pipeline, resource_keys: [resourceKey] }], 'zip')
    const entries = exportResponse.status === 200 ? await readZipEntries(exportResponse.bytes) : new Map<string, Buffer>()
    const assetEntry = findEntry(entries, `files/${upload.fileId}/${upload.fileName}`)

    const importResponse = await importSelective(b, { bytes: exportResponse.bytes, filename: 'root-roundtrip.zip' }, {
      selections: [{ environment_key: scopeDevMain.env, pipeline_key: scopeDevMain.pipeline, resource_keys: [resourceKey] }],
      overwrite: true,
    })

    const bDetail = await getConfigDetail(b, scopeDevMain, resourceKey)
    const bContentPath = String(bDetail.json?.data?.config?.content ?? '')
    const bResolved = bContentPath ? resolveUrl(b, bContentPath) : ''
    const bDownload = bResolved ? await request(b, bResolved, { method: 'GET' }) : null
    const bSha = bDownload && bDownload.status === 200 ? sha256Hex(bDownload.bytes) : null

    // 静态包:根路径下本地 URL 无前缀,静态托管可逐条取回
    await publishRuntimeSnapshot(a, scopeDevMain)
    const staticExport = await runtimeStatic(a, scopeDevMain)
    const staticEntries = staticExport.status === 200 ? await readZipEntries(staticExport.bytes) : new Map<string, Buffer>()
    const configJson = staticEntries.get('config.json') ? JSON.parse(staticEntries.get('config.json')!.toString('utf8')) : null
    const localUrls: string[] = (configJson?.data?.configs as any[] ?? [])
      .map((item) => item.content)
      .filter((content) => typeof content === 'string' && content.startsWith('./assets/'))
    const fileMap = new Map<string, Buffer>()
    for (const [name, payload] of staticEntries) fileMap.set(name.replace(/\\/g, '/'), payload)
    const server = http.createServer((request_, response) => {
      const pathname = decodeURIComponent((request_.url ?? '/').split('?')[0]).replace(/^\//, '')
      const payload = fileMap.get(pathname)
      if (!payload) { response.statusCode = 404; response.end('nf'); return }
      response.statusCode = 200
      response.end(payload)
    })
    await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve))
    const port = (server.address() as { port: number }).port
    const checks: { url: string; status: number; sha: string | null }[] = []
    try {
      for (const localUrl of localUrls) {
        const response = await fetch(new URL(localUrl, `http://127.0.0.1:${port}/`))
        const bytes = Buffer.from(await response.arrayBuffer())
        checks.push({ url: localUrl, status: response.status, sha: response.status === 200 ? sha256Hex(bytes) : null })
      }
    } finally {
      await new Promise<void>((resolve) => server.close(() => resolve()))
    }

    writeEvidence('t11-root', {
      exportStatus: exportResponse.status,
      exportEntryNames: [...entries.keys()],
      exportUsesPortableSeparators: [...entries.keys()].every((name) => !name.includes('\\')),
      assetEntryFound: Boolean(assetEntry),
      assetEntrySha: assetEntry ? sha256Hex(assetEntry) : null,
      expectedSha: upload.sha256,
      importCode: importResponse.json?.code ?? null,
      importError: importResponse.json?.error ?? null,
      bContentPath,
      bResolved,
      bProbe: bDownload ? { status: bDownload.status, contentType: bDownload.contentType, sha: bSha, bodyPreview: bDownload.contentType.includes('json') ? bDownload.text.slice(0, 160) : '' } : null,
      localUrls,
      staticChecks: checks,
    })

    expect(staticExport.status, '根路径静态导出 HTTP').toBe(200)
    expect(localUrls.length, '根路径静态包应至少包含一个本地资源 URL').toBeGreaterThan(0)
    expect.soft(exportResponse.status, '根路径转移导出 HTTP').toBe(200)
    expect.soft([...entries.keys()].every((name) => !name.includes('\\')), `根路径导出条目名含反斜杠: ${JSON.stringify([...entries.keys()])}`).toBe(true)
    expect.soft(Boolean(assetEntry), `根路径导出包内缺资源(磁盘哈希 49779… 见实例目录, 实际条目 ${JSON.stringify([...entries.keys()])})`).toBe(true)
    if (assetEntry) expect.soft(sha256Hex(assetEntry), '根路径导出包资源字节与上传不符').toBe(upload.sha256)
    expect.soft(importResponse.json?.code ?? null, `根路径选择性导入失败: ${importResponse.text.slice(0, 200)}`).toBe(200)
    expect.soft(bDownload?.contentType ?? '', `根路径 B 资源内容类型异常(HTTP=${bDownload?.status} url=${bResolved})`).toContain('image/')
    expect.soft(bSha, `根路径 B 资源字节不符(HTTP=${bDownload?.status} body=${bDownload?.text.slice(0, 120)})`).toBe(upload.sha256)
    for (const check of checks) {
      expect.soft(check.status, `根路径静态托管取回失败: ${check.url}`).toBe(200)
      expect.soft(check.sha, `根路径静态字节不一致: ${check.url}`).toBe(upload.sha256)
    }
  })
})
