import { expect, test } from '@playwright/test'
import { targetA, writeEvidence } from './support/env'
import { postJson } from './support/net'
import { createConfig, deleteConfig, ensureScope, listConfigs, scopeDevMain, setupSentinelScopes } from './support/scopes'
import { configsByAlias } from './support/db'

const ALIAS = 'CONC_ALIAS'
const ROUNDS = 3
const CONCURRENCY = 10

test.describe('T22 并发同 alias 创建 @t22', () => {
  test('同范围 10 并发 × 3 轮：库内不得出现重复 alias @t22', async () => {
    test.setTimeout(120_000)
    const target = targetA()
    await ensureScope(target, scopeDevMain)
    await setupSentinelScopes(target, 't22')

    const roundResults: unknown[] = []
    for (let round = 1; round <= ROUNDS; round += 1) {
      const responses = await Promise.all(
        Array.from({ length: CONCURRENCY }, (_, index) =>
          postJson(target, '/api/v1/config/create', {
            config: {
              environment_key: scopeDevMain.env,
              pipeline_key: scopeDevMain.pipeline,
              alias: ALIAS,
              name: `并发样本 r${round}-${index}`,
              type: 'text',
              content: `round-${round}-attempt-${index}`,
            },
          }),
        ),
      )
      const successes = responses.filter((response) => response.json?.code === 200)
      const failures = responses.filter((response) => response.json?.code !== 200)
      for (const failure of failures) {
        expect(failure.json?.code).toBe(400)
        expect(String(failure.json?.error)).toContain('相同别名')
      }
      const rows = configsByAlias(target, scopeDevMain.env, scopeDevMain.pipeline, ALIAS)
      roundResults.push({
        round,
        successCount: successes.length,
        failureCount: failures.length,
        failureSamples: failures.slice(0, 3).map((response) => response.json?.error ?? response.text.slice(0, 120)),
        rowCount: rows.length,
        rowKeys: rows.map((row) => row.resource_key),
        rowContentSamples: rows.map((row) => row.content),
      })
      // 清理，独立夹具重复三轮
      for (const row of rows) {
        await deleteConfig(target, scopeDevMain, String(row.resource_key))
      }
    }

    writeEvidence('t22-concurrency', { alias: ALIAS, concurrency: CONCURRENCY, rounds: roundResults })

    const duplicated = (roundResults as any[]).filter((item) => item.rowCount > 1)
    expect(duplicated, `并发产生重复 alias: ${JSON.stringify(duplicated)}`).toEqual([])
    for (const item of roundResults as any[]) {
      expect(item.successCount + item.failureCount).toBe(CONCURRENCY)
      expect(item.successCount).toBe(1)
      expect(item.rowCount).toBe(1)
    }
    // 校验结束后固定状态:库中不应残留该 alias
    expect(configsByAlias(target, scopeDevMain.env, scopeDevMain.pipeline, ALIAS).length).toBe(0)
    // 保持列表可读(避免与后续用例混淆)
    await listConfigs(target, scopeDevMain)
  })
})
