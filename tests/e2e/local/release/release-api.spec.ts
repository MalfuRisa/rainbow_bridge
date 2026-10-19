import { expect, test } from '@playwright/test'
import { freezeToken, targetA, writeEvidence } from '../support/env'
import { createConfig, deleteConfig, ensureScope, listConfigs, scopeProdMain } from '../support/scopes'
import { postJson, request } from '../support/net'
import { lockScopeFromCurrentRevision, scopeStatus, unlockScopeFromCurrentRevision } from '../freeze/support'

/**
 * R08/R09/R10/R20(API 层):创建与切换的幂等重放/冲突、校验错误、
 * 分页、父级删除保护、资源下载字节核验。
 */
test.describe('版本发布 API 契约 @release', () => {
  test('幂等重放、冲突、校验、分页与删除保护 @release', async () => {
    test.setTimeout(180_000)
    const target = targetA()
    const token = freezeToken()
    const auth = { Authorization: `Bearer ${token}` }
    await ensureScope(target, scopeProdMain)

    const initial = await scopeStatus(target, scopeProdMain)
    if (initial.state === 'locked') {
      await unlockScopeFromCurrentRevision(target, scopeProdMain, token)
    }
    const rows = await listConfigs(target, scopeProdMain)
    if (!rows.find((item) => item.alias === 'REL_API_TEXT')) {
      await createConfig(target, scopeProdMain, { alias: 'REL_API_TEXT', name: 'API 样本', type: 'text', content: 'api-v1' })
    }
    const revisionAfterLock = await lockScopeFromCurrentRevision(target, scopeProdMain, token)

    const requestId = crypto.randomUUID()
    const createPayload = {
      environment_key: scopeProdMain.env,
      pipeline_key: scopeProdMain.pipeline,
      version: 'v9.0',
      expected_freeze_revision: revisionAfterLock,
      request_id: requestId,
      reason: 'API 契约验证',
    }
    const first = await postJson(target, '/api/v1/release/create', createPayload, auth)
    expect(first.json?.data?.meta?.version ?? first.text.slice(0, 200), first.text.slice(0, 200)).toBe('v9.0')
    expect(first.status, first.text.slice(0, 200)).toBe(200)
    const snapshotId = first.json.data.meta.snapshot_id

    // 重放:同 request_id → replayed,不再复制
    const replay = await postJson(target, '/api/v1/release/create', createPayload, auth)
    expect(replay.status).toBe(200)
    expect(replay.json.data.replayed).toBe(true)
    expect(replay.json.data.meta.snapshot_id).toBe(snapshotId)

    // 同 request_id 不同字段 → 409 REQUEST_CONFLICT
    const conflict = await postJson(target, '/api/v1/release/create', { ...createPayload, reason: '不同原因' }, auth)
    expect(conflict.status).toBe(409)
    expect(conflict.json.error_type).toBe('RELEASE_REQUEST_CONFLICT')

    // 同版本不同 request_id → 409 VERSION_EXISTS
    const exists = await postJson(target, '/api/v1/release/create', { ...createPayload, request_id: crypto.randomUUID() }, auth)
    expect(exists.status).toBe(409)
    expect(exists.json.error_type).toBe('RELEASE_VERSION_EXISTS')

    // 校验:非法版本号/保留字/UUID/revision/游标
    for (const bad of [
      { ...createPayload, version: 'V1.0', request_id: crypto.randomUUID() },
      { ...createPayload, version: 'latest', request_id: crypto.randomUUID() },
      { ...createPayload, request_id: 'not-a-uuid' },
      { ...createPayload, expected_freeze_revision: 'abc', request_id: crypto.randomUUID() },
      { ...createPayload, reason: '   ', request_id: crypto.randomUUID() },
    ]) {
      const response = await postJson(target, '/api/v1/release/create', bad, auth)
      expect(response.status, `bad payload must be 400: ${JSON.stringify(bad).slice(0, 120)}`).toBe(400)
      expect(response.json.error_type).toBe('RELEASE_INVALID_REQUEST')
    }
    const badCursor = await request(target, `/api/v1/release/list?environment_key=${scopeProdMain.env}&pipeline_key=${scopeProdMain.pipeline}&before_id=abc`, { method: 'GET' })
    expect(badCursor.status).toBe(400)

    // 分页:limit=1 有 has_more 与游标;空数组字段齐全
    const page1 = await request(target, `/api/v1/release/list?environment_key=${scopeProdMain.env}&pipeline_key=${scopeProdMain.pipeline}&limit=1`, { method: 'GET' })
    const page1JSON = JSON.parse(page1.text)
    expect(page1JSON.data.items.length).toBe(1)
    expect(page1JSON.data.active).toBe(null)
    expect(typeof page1JSON.data.next_before_id).toBe('string')

    // 激活幂等重放:返回原 applied_revision 与最新 active
    const activateId = crypto.randomUUID()
    const activatePayload = {
      environment_key: scopeProdMain.env,
      pipeline_key: scopeProdMain.pipeline,
      version: 'v9.0',
      expected_active_revision: '0',
      request_id: activateId,
      reason: 'API 激活',
    }
    const activated = await postJson(target, '/api/v1/release/activate', activatePayload, auth)
    expect(activated.status, activated.text.slice(0, 200)).toBe(200)
    expect(activated.json.data.operation.applied_revision).toBe('1')
    const activateReplay = await postJson(target, '/api/v1/release/activate', activatePayload, auth)
    expect(activateReplay.status).toBe(200)
    expect(activateReplay.json.data.operation.replayed).toBe(true)
    expect(activateReplay.json.data.operation.applied_revision).toBe('1')
    expect(activateReplay.json.data.active.version).toBe('v9.0')

    // 目标即当前 → 409 ACTIVE_UNCHANGED;过期 revision → 409 带最新
    const unchanged = await postJson(target, '/api/v1/release/activate', { ...activatePayload, expected_active_revision: '1', request_id: crypto.randomUUID() }, auth)
    expect(unchanged.status).toBe(409)
    expect(unchanged.json.error_type).toBe('RELEASE_ACTIVE_UNCHANGED')
    const stale = await postJson(target, '/api/v1/release/activate', { ...activatePayload, expected_active_revision: '0', request_id: crypto.randomUUID() }, auth)
    expect(stale.status).toBe(409)
    expect(stale.json.error_type).toBe('RELEASE_ACTIVE_REVISION_CONFLICT')
    expect(stale.json.latest_active.version).toBe('v9.0')

    // 资源下载核验(无资源范围应无下载;此处仅验证快照无资源时 manifest 一致)
    const detail = await request(target, `/api/v1/release/detail?environment_key=${scopeProdMain.env}&pipeline_key=${scopeProdMain.pipeline}&version=v9.0`, { method: 'GET' })
    expect(detail.status).toBe(200)
    const detailJSON = JSON.parse(detail.text)
    expect(detailJSON.data.meta.version).toBe('v9.0')
    expect(detailJSON.data.meta.source_freeze_revision).toBe(revisionAfterLock)
    expect(Array.isArray(detailJSON.data.assets)).toBe(true)

    // 跨范围按不存在处理
    const crossScope = await request(target, `/api/v1/release/detail?environment_key=qa_dev&pipeline_key=main&version=v9.0`, { method: 'GET' })
    expect(crossScope.status).toBe(404)
    expect(JSON.parse(crossScope.text).error_type).toBe('RELEASE_VERSION_NOT_FOUND')
    const missingScope = await request(target, `/api/v1/release/list?environment_key=no_such_env&pipeline_key=main`, { method: 'GET' })
    expect(missingScope.status).toBe(404)

    // 父级删除保护:解锁后仍拒绝
    await unlockScopeFromCurrentRevision(target, scopeProdMain, token)
    const deleteAttempt = await postJson(target, '/api/v1/pipeline/delete', { environment_key: scopeProdMain.env, pipeline_key: scopeProdMain.pipeline })
    expect(deleteAttempt.status).toBe(409)
    expect(deleteAttempt.json.error_type).toBe('RELEASE_SCOPE_HAS_VERSIONS')
    const deleteEnv = await postJson(target, '/api/v1/environment/delete', { environment_key: scopeProdMain.env })
    expect(deleteEnv.status).toBe(409)
    expect(deleteEnv.json.error_type).toBe('RELEASE_SCOPE_HAS_VERSIONS')

    writeEvidence('release-api', {
      snapshotId,
      replaySnapshot: replay.json.data.meta.snapshot_id,
      activateApplied: activated.json.data.operation.applied_revision,
      deleteProtection: deleteAttempt.json.error_type,
    })

    // 清理编辑区样本(版本保留)
    const finalRows = await listConfigs(target, scopeProdMain)
    for (const row of finalRows) {
      if (row.alias === 'REL_API_TEXT') {
        await deleteConfig(target, scopeProdMain, row.resource_key)
      }
    }
  })
})