import { expect, test } from '@playwright/test'
import * as http from 'http'
import { targetA, targetB, writeEvidence } from './support/env'
import {
  buildTransferZip,
  findEntry,
  readTarGzEntries,
  readZipEntries,
  usesPortableSeparators,
} from './support/archives'
import { exportSelective, importSelective, runtimeStatic } from './support/imports'
import {
  TINY_PNG,
  TINY_TXT,
  createConfig,
  ensureScope,
  getConfigDetail,
  resolveUrl,
  scopeDevMain,
  setupSentinelScopes,
  uploadAsset,
} from './support/scopes'
import { getJson, request, sha256Hex } from './support/net'

const ZIP_TEXT_ALIAS = 'RT_ZIP_TEXT'
const IMAGE_ALIAS = 'RT_IMAGE'

test.describe('T11 导出往返与静态托管 @t11 @smoke', () => {
  test('ZIP/TAR 往返一致、静态包每个本地资源 URL 可取回原字节 @t11 @smoke', async ({ page }) => {
    const a = targetA()
    const b = targetB()
    await ensureScope(a, scopeDevMain)
    await ensureScope(b, scopeDevMain)
    await setupSentinelScopes(a, 't11')

    const upload = await uploadAsset(a, scopeDevMain, {
      fileName: 'rt-image.png', contentType: 'image/png', bytes: TINY_PNG, remark: '往返图片',
    })
    await createConfig(a, scopeDevMain, {
      alias: ZIP_TEXT_ALIAS, name: '往返文本', type: 'text', content: 'roundtrip-text', description: '往返描述',
    })
    const imageConfig = await createConfig(a, scopeDevMain, {
      alias: IMAGE_ALIAS, name: '往返图片', type: 'image', content: upload.reference, description: '图片描述',
    })
    const imageKey = imageConfig.config!.resource_key
    const selections = [{ environment_key: scopeDevMain.env, pipeline_key: scopeDevMain.pipeline, resource_keys: [imageKey] }]

    // ---- 转移包 ZIP
    const zipExport = await exportSelective(a, selections, 'zip')
    expect(zipExport.status, 'ZIP 导出 HTTP').toBe(200)
    const zipEntries = await readZipEntries(zipExport.bytes)
    const zipEntryNames = [...zipEntries.keys()]
    const zipConfigsJson = zipEntries.get('configs.json')
      ? JSON.parse((findEntry(zipEntries, 'configs.json')!.toString('utf8')))
      : null
    const zipAssetBytes = findEntry(zipEntries, `files/${upload.fileId}/${upload.fileName}`)
    const zipUsesPortableNames = usesPortableSeparators(zipEntries)

    // ---- 转移包 TAR.GZ
    const tarExport = await exportSelective(a, selections, 'tar.gz')
    expect(tarExport.status, 'TAR 导出 HTTP').toBe(200)
    const tarEntries = await readTarGzEntries(tarExport.bytes)
    const tarEntryNames = [...tarEntries.keys()]
    const tarAssetBytes = findEntry(tarEntries, `files/${upload.fileId}/${upload.fileName}`)
    const tarUsesPortableNames = usesPortableSeparators(tarEntries)

    // ---- B 用页面导入 ZIP
    const pageRequests: string[] = []
    page.on('request', (request_) => {
      if (request_.url().includes('/api/v1/transfer/')) pageRequests.push(`${request_.method()} ${new URL(request_.url()).pathname}`)
    })
    await page.goto(`${b.origin}${b.prefix}/import-export`, { waitUntil: 'domcontentloaded' })
    await page.getByRole('tab', { name: '数据导入' }).click()
    await page.locator('#import-file').setInputFiles({ name: 'roundtrip.zip', mimeType: 'application/zip', buffer: zipExport.bytes })
    await expect(page.getByText('选择导入内容')).toBeVisible({ timeout: 15_000 })
    await page.getByRole('button', { name: '开始导入' }).click()
    const confirmButton = page.getByRole('button', { name: '确认导入' })
    if (await confirmButton.isVisible().catch(() => false)) {
      await confirmButton.click()
    }
    await expect(page.getByText('导入完成，资源恢复完整', { exact: true })).toBeVisible({ timeout: 20_000 })

    const bDetail = await getConfigDetail(b, scopeDevMain, imageKey)
    const bConfigAfterPage = bDetail.json?.data?.config
    const bImagePath = String(bConfigAfterPage?.content ?? '')
    const bImageDownload = bImagePath ? await assetProbe(b, bImagePath) : null
    const bSha = bImageDownload?.sha ?? null
    const bAssetFilesOnDisk = await listUploadFiles(b.instanceDir)

    // ---- TAR 走选择导入 API
    const tarImport = await importSelective(b, { bytes: tarExport.bytes, filename: 'roundtrip.tar.gz', contentType: 'application/gzip' }, {
      selections: [{ environment_key: scopeDevMain.env, pipeline_key: scopeDevMain.pipeline, resource_keys: [imageKey] }],
      overwrite: true,
    })
    const bImageDownloadAfterTar = bImagePath ? await assetProbe(b, bImagePath) : null
    const exportAssetEntries = zipEntryNames.filter((name) => /^files[\\/]/.test(name))
    const unrelatedAssetEntries = exportAssetEntries.filter((name) => !name.replace(/\\/g, '/').includes(upload.fileId))

    // ---- 静态包(config.json 内每条本地 URL 都要能取回原字节)
    const staticExport = await runtimeStatic(a, scopeDevMain)
    expect(staticExport.status).toBe(200)
    const staticEntries = await readZipEntries(staticExport.bytes)
    const configJson = JSON.parse(staticEntries.get('config.json')!.toString('utf8'))
    const localUrls: string[] = []
    for (const config of configJson.data.configs) {
      if (typeof config.content === 'string' && config.content.includes('/api/v1/asset/file/')) {
        localUrls.push(config.content)
      }
    }
    const fileMap = new Map<string, Buffer>()
    for (const [name, payload] of staticEntries) fileMap.set(name.replace(/\\/g, '/'), payload)
    const server = http.createServer((request_, response) => {
      const pathname = decodeURIComponent((request_.url ?? '/').split('?')[0]).replace(/^\//, '')
      const payload = fileMap.get(pathname)
      if (!payload) { response.statusCode = 404; response.end('not found'); return }
      response.statusCode = 200
      response.end(payload)
    })
    await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve))
    const port = (server.address() as { port: number }).port
    const staticChecks: { url: string; status: number; sha: string | null }[] = []
    try {
      for (const localUrl of localUrls) {
        const response = await fetch(`http://127.0.0.1:${port}${localUrl}`)
        const bytes = Buffer.from(await response.arrayBuffer())
        staticChecks.push({ url: localUrl, status: response.status, sha: response.status === 200 ? sha256Hex(bytes) : null })
      }
    } finally {
      await new Promise<void>((resolve) => server.close(() => resolve()))
    }

    writeEvidence('t11-roundtrip', {
      expectedAssetSha: upload.sha256,
      zip: {
        entryNames: zipEntryNames,
        usesPortableSeparators: zipUsesPortableNames,
        assetEntryFound: Boolean(zipAssetBytes),
        assetSha: zipAssetBytes ? sha256Hex(zipAssetBytes) : null,
        configsTotal: zipConfigsJson?.business_configs?.length ?? null,
        imageRowDescription: zipConfigsJson?.business_configs?.find((row: any) => row.alias === IMAGE_ALIAS)?.description ?? null,
      },
      tar: {
        entryNames: tarEntryNames,
        usesPortableSeparators: tarUsesPortableNames,
        assetEntryFound: Boolean(tarAssetBytes),
        assetSha: tarAssetBytes ? sha256Hex(tarAssetBytes) : null,
      },
      bAfterPageImport: {
        alias: bConfigAfterPage?.alias ?? null,
        description: bConfigAfterPage?.description ?? null,
        contentPath: bImagePath,
        probe: bImageDownload,
        uploadFilesOnDisk: bAssetFilesOnDisk,
      },
      bAfterTarImport: {
        code: tarImport.json?.code ?? null,
        total: tarImport.json?.data?.total ?? null,
        probe: bImageDownloadAfterTar,
      },
      overExport: {
        selectedResourceKey: imageKey,
        exportAssetEntries,
        unrelatedAssetEntries,
        note: 'writeConfigArchive 会把该环境/渠道下所有资源并入包,与 selections 的 resource_keys 不完全一致',
      },
      pageRequests,
      staticLocalUrls: localUrls,
      staticChecks,
    })

    // 夹具完整性(硬断言):两个包都能被读出且含 configs.json
    expect(zipConfigsJson, 'ZIP 缺少 configs.json').toBeTruthy()
    expect(localUrls.length, '静态包应至少包含一个本地资源 URL').toBeGreaterThan(0)

    // 缺陷敏感断言(soft):让每个问题都出现在同一轮证据里
    expect.soft(zipUsesPortableNames, `ZIP 条目名使用了反斜杠(Windows filepath.Join): ${JSON.stringify(zipEntryNames)}`).toBe(true)
    expect.soft(tarUsesPortableNames, `TAR 条目名使用了反斜杠: ${JSON.stringify(tarEntryNames)}`).toBe(true)
    expect.soft(Boolean(zipAssetBytes), `ZIP 内缺资源文件 files/${upload.fileId}/${upload.fileName}: ${JSON.stringify(zipEntryNames)}`).toBe(true)
    if (zipAssetBytes) expect.soft(sha256Hex(zipAssetBytes), 'ZIP 内资源字节不一致').toBe(upload.sha256)
    if (tarAssetBytes) expect.soft(sha256Hex(tarAssetBytes), 'TAR 内资源字节不一致').toBe(upload.sha256)

    expect.soft(bConfigAfterPage?.alias ?? null, 'B 未导入图片配置').toBe(IMAGE_ALIAS)
    expect.soft(bConfigAfterPage?.description ?? null, 'B 导入后 description 不一致').toBe('图片描述')
    // 资源必须真正恢复:内容类型为图片且字节一致(HTTP 200 + 错误 JSON 不能算成功)
    expect.soft(bImageDownload?.contentType ?? '', `B 资源返回非图片内容类型(磁盘文件: ${JSON.stringify(bAssetFilesOnDisk)}; body=${bImageDownload?.bodyPreview ?? ''})`).toContain('image/')
    expect.soft(bSha, `B 导入后资源字节与 A 不一致(probe=${JSON.stringify(bImageDownload)})`).toBe(upload.sha256)
    expect.soft(bImageDownloadAfterTar?.contentType ?? '', 'B(TAR 路径)资源返回非图片内容类型').toContain('image/')

    for (const check of staticChecks) {
      // 静态包会包含该范围全部资源;逐条 URL 对齐各自资源的 SHA
      const expectedSha = check.url.includes('qa-page-file.txt') ? sha256Hex(TINY_TXT) : upload.sha256
      expect.soft(check.status, `静态托管取回失败: ${check.url}`).toBe(200)
      expect.soft(check.sha, `静态托管字节不一致: ${check.url}`).toBe(expectedSha)
    }
    if (unrelatedAssetEntries.length > 0) {
      test.info().annotations.push({
        type: 'observed',
        description: `选择性导出携带未选中配置的资源(整范围收集): ${JSON.stringify(unrelatedAssetEntries)}`,
      })
    }
  })
})

interface AssetProbe {
  status: number
  contentType: string
  sha: string | null
  businessCode: number | null
  bodyPreview: string
}

/** 资源 URL 探测:同时记录 HTTP 状态、Content-Type、业务 code 与字节哈希。 */
async function assetProbe(target: ReturnType<typeof targetB>, contentPath: string): Promise<AssetProbe> {
  const response = await request(target, resolveUrl(target, contentPath), { method: 'GET' })
  return {
    status: response.status,
    contentType: response.contentType,
    sha: sha256Hex(response.bytes),
    businessCode: typeof response.json?.code === 'number' ? response.json.code : null,
    bodyPreview: response.contentType.includes('json') ? response.text.slice(0, 160) : '',
  }
}

async function listUploadFiles(instanceDir: string): Promise<string[]> {
  const fs = await import('fs')
  const path = await import('path')
  const root = path.join(instanceDir, 'data', 'uploads')
  if (!fs.existsSync(root)) return []
  const files: string[] = []
  const walk = (dir: string) => {
    for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
      const full = path.join(dir, entry.name)
      if (entry.isDirectory()) walk(full)
      else files.push(path.relative(root, full).replace(/\\/g, '/'))
    }
  }
  walk(root)
  return files
}
