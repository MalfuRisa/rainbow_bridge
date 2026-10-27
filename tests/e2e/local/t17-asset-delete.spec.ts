import { expect, test } from '@playwright/test'
import { targetA, writeEvidence } from './support/env'
import { getJson, postJson, request } from './support/net'
import { TINY_PNG, createConfig, ensureScope, getConfigDetail, resolveUrl, scopeDevMain, setupSentinelScopes, uploadAsset } from './support/scopes'

test.describe('T17 资源被删除的影响 @t17', () => {
  test('OBSERVED:资源删除入口与断链行为实测 @t17', async () => {
    const target = targetA()
    await ensureScope(target, scopeDevMain)
    await setupSentinelScopes(target, 't17')

    const upload = await uploadAsset(target, scopeDevMain, {
      fileName: 'delete-impact.png', contentType: 'image/png', bytes: TINY_PNG, remark: 'T17',
    })
    await createConfig(target, scopeDevMain, {
      alias: 'DEL_IMPACT_CFG', name: '删除影响样本', type: 'image', content: upload.reference,
    })

    // 是否存在资源删除入口:逐一探测常见路径(只探测,不写入)
    const probes = [
      { method: 'POST', path: '/api/v1/asset/delete', body: { file_id: upload.fileId } },
      { method: 'POST', path: '/api/v1/asset/remove', body: { file_id: upload.fileId } },
    ] as const
    const probeResults: Record<string, unknown> = {}
    for (const probe of probes) {
      const response = probe.method === 'POST'
        ? await postJson(target, probe.path, probe.body)
        : await getJson(target, probe.path)
      probeResults[`${probe.method} ${probe.path}`] = { status: response.status, body: response.text.slice(0, 160) }
    }
    // 资源列表确认仍在(探测未产生副作用)
    const listAfterProbe = await getJson(target, `/api/v1/asset/list?environment_key=${scopeDevMain.env}&pipeline_key=${scopeDevMain.pipeline}`)
    const stillThere = (listAfterProbe.json?.data?.list ?? []).some((asset: any) => asset.file_id === upload.fileId)

    // 引用该资源的配置仍可读取与下载(暂无删除行为时的基线)
    const detail = await getConfigDetail(target, scopeDevMain, '')
    const rows = await getJson(target, `/api/v1/config/list?environment_key=${scopeDevMain.env}&pipeline_key=${scopeDevMain.pipeline}`)
    const configRow = (rows.json?.data?.list ?? []).find((item: any) => item.alias === 'DEL_IMPACT_CFG')
    const download = await request(target, resolveUrl(target, String(configRow?.content ?? '')), { method: 'GET' })

    const observed = {
      probes: probeResults,
      assetStillListedAfterProbes: stillThere,
      referencingConfigReadable: Boolean(configRow),
      assetDownloadStatus: download.status,
      note: 'UI 与后端均未提供资源删除接口(本轮实测)；删除保护/断链提示无法验证——能力缺口，据实记录',
      detailProbe: { status: detail.status },
    }
    writeEvidence('t17-asset-delete', observed)

    // 无删除入口:探测必须全部不是成功状态
    for (const [key, value] of Object.entries(probeResults)) {
      expect((value as any).status, `${key} 不应存在删除能力`).toBe(404)
    }
    expect(stillThere, '探测不得删除资源').toBe(true)
    expect(download.status, '引用中的资源应可下载').toBe(200)
  })
})