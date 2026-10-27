import { test } from '@playwright/test'
import { targetA, writeEvidence } from './support/env'
import { postJson, request } from './support/net'
import { scopeDevMain, setupSentinelScopes } from './support/scopes'
import { dumpTable } from './support/db'

test.describe('T18 匿名写入 @t18', () => {
  test('OBSERVED:无凭据与无效 Bearer 下的创建/更新/删除实测 @t18', async () => {
    const target = targetA()
    await setupSentinelScopes(target, 't18')

    const created = await postJson(target, '/api/v1/config/create', {
      config: {
        environment_key: scopeDevMain.env, pipeline_key: scopeDevMain.pipeline,
        alias: 'ANON_CFG', name: '匿名创建', type: 'text', content: 'anon-create',
      },
    })
    const resourceKey = String(created.json?.data?.config?.resource_key ?? '')

    const updated = resourceKey
      ? await postJson(target, '/api/v1/config/update', {
          config: { resource_key: resourceKey, environment_key: scopeDevMain.env, pipeline_key: scopeDevMain.pipeline, name: '匿名更新', type: 'text', content: 'anon-update' },
        })
      : null

    const withInvalidBearer = await postJson(
      target,
      '/api/v1/config/create',
      { config: { environment_key: scopeDevMain.env, pipeline_key: scopeDevMain.pipeline, alias: 'ANON_BEARER_CFG', name: '无效凭据', type: 'text', content: 'x' } },
      { Authorization: 'Bearer not-a-real-jwt' },
    )

    // 删除自建测试配置(匿名)
    const deleted = resourceKey
      ? await postJson(target, '/api/v1/config/delete', { environment_key: scopeDevMain.env, pipeline_key: scopeDevMain.pipeline, resource_key: resourceKey })
      : null

    const rowsAfter = dumpTable(target, 'resource_config', [
      `environment_key=${scopeDevMain.env}`, `pipeline_key=${scopeDevMain.pipeline}`, 'alias=ANON_CFG',
    ])
    const bearerRows = dumpTable(target, 'resource_config', [
      `environment_key=${scopeDevMain.env}`, `pipeline_key=${scopeDevMain.pipeline}`, 'alias=ANON_BEARER_CFG',
    ])
    // 清理无效凭据样本，避免污染后续哨兵对比
    if (bearerRows.count > 0) {
      await postJson(target, '/api/v1/config/delete', {
        environment_key: scopeDevMain.env, pipeline_key: scopeDevMain.pipeline,
        resource_key: String(bearerRows.rows[0]?.resource_key ?? ''),
      })
    }

    const observed = {
      create: { code: created.json?.code, error: created.json?.error ?? null },
      update: updated ? { code: updated.json?.code } : null,
      invalidBearer: { code: withInvalidBearer.json?.code, error: withInvalidBearer.json?.error ?? null },
      delete: deleted ? { code: deleted.json?.code } : null,
      rowsAfterDelete: rowsAfter.count,
      invalidBearerRowWritten: bearerRows.count,
      deploymentMode: '本机直连(无网关)。线上网关是否保护需另行评估，不据此推断外网可利用性。',
    }
    writeEvidence('t18-anonymous', observed)

    // 记录实际信任边界:本机部署允许匿名写入时，必须如实记录而不是假设有鉴权
    if (created.json?.code === 200) {
      test.info().annotations.push({ type: 'observed', description: '匿名创建成功:本机直连模式下无写保护' })
    }
    if (withInvalidBearer.json?.code === 200) {
      test.info().annotations.push({ type: 'observed', description: '无效 Bearer 创建成功:身份校验缺失' })
    }
  })
})