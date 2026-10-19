import { expect, test } from '@playwright/test'
import { targetA, writeEvidence } from './support/env'
import { getJson, request } from './support/net'
import { TINY_PNG, createConfig, ensureScope, getConfigDetail, resolveUrl, scopeDevMain, setupSentinelScopes, uploadAsset } from './support/scopes'
import { dumpTable } from './support/db'

test.describe('T19 伪造身份与私有读取 @t19', () => {
  test('OBSERVED:X-User-Id 伪造、无效 Bearer、无身份三组读取路径对比 @t19', async () => {
    const target = targetA()
    await ensureScope(target, scopeDevMain)
    await setupSentinelScopes(target, 't19')

    const upload = await uploadAsset(target, scopeDevMain, {
      fileName: 'private.png', contentType: 'image/png', bytes: TINY_PNG, remark: 'T19 私有',
    })
    const { config } = await createConfig(target, scopeDevMain, {
      alias: 'PRIVATE_CFG', name: '私有配置', type: 'image', content: upload.reference, description: '私有读取样本',
    })
    const resourceKey = config!.resource_key
    // 置为 is_perm=true(非零更新语义)
    const { updateConfig } = await import('./support/scopes')
    await updateConfig(target, scopeDevMain, { resource_key: resourceKey, name: '私有配置', type: 'image', content: upload.reference, description: '私有读取样本', is_perm: true })

    const detailPath = `/api/v1/config/detail?environment_key=${scopeDevMain.env}&pipeline_key=${scopeDevMain.pipeline}&resource_key=${resourceKey}`
    const listPath = `/api/v1/config/list?environment_key=${scopeDevMain.env}&pipeline_key=${scopeDevMain.pipeline}`
    const assetListPath = `/api/v1/asset/list?environment_key=${scopeDevMain.env}&pipeline_key=${scopeDevMain.pipeline}`

    const groups = {
      forgedUserId: { 'X-User-Id': '1' },
      invalidBearer: { Authorization: 'Bearer forged-token' },
      anonymous: {},
    } as const

    const observations: Record<string, unknown> = {}
    for (const [name, headers] of Object.entries(groups)) {
      const detail = await getJson(target, detailPath, headers)
      const list = await getJson(target, listPath, headers)
      const assets = await getJson(target, assetListPath, headers)
      const detailConfig = detail.json?.data?.config
      let downloadStatus: number | null = null
      if (detailConfig?.content) {
        const download = await request(target, resolveUrl(target, String(detailConfig.content)), { method: 'GET', headers })
        downloadStatus = download.status
      }
      observations[name] = {
        detailCode: detail.json?.code,
        listContainsPrivate: (list.json?.data?.list ?? []).some((item: any) => item.alias === 'PRIVATE_CFG'),
        assetsCount: (assets.json?.data?.list ?? []).length,
        downloadStatus,
      }
    }

    const dbRow = dumpTable(target, 'resource_config', [
      `environment_key=${scopeDevMain.env}`, `pipeline_key=${scopeDevMain.pipeline}`, 'alias=PRIVATE_CFG',
    ]).rows[0]

    const observed = {
      observations,
      identityColumns: dbRow ? Object.keys(dbRow).filter((key) => /user|create|update/i.test(key)) : [],
      dbRowSample: dbRow ?? null,
      conclusion: '三组读取返回是否一致决定身份头是否影响读取；不把可伪造身份当成有效授权',
    }
    writeEvidence('t19-identity', observed)

    // 三组读取结果必须一致：身份头不构成授权边界(如实记录差异为缺陷线索)
    const first = observations['forgedUserId'] as any
    for (const [name, value] of Object.entries(observations)) {
      const item = value as any
      expect(item.detailCode, `${name} 读取私有配置结果与 X-User-Id 组不同`).toBe(first.detailCode)
      expect(item.downloadStatus, `${name} 资源下载结果不同`).toBe(first.downloadStatus)
    }
  })
})