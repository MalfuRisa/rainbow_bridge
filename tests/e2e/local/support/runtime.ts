import { randomUUID } from 'node:crypto'
import { freezeToken, type Target } from './env'
import { getJson, postJson } from './net'
import type { Scope } from './scopes'
import { lockScopeFromCurrentRevision, scopeStatus, unlockScopeFromCurrentRevision } from '../freeze/support'

/** Explicit fixture step: publish the current editor contents, then restore editability.
 * Runtime read helpers remain read-only and never publish implicitly.
 */
export async function publishRuntimeSnapshot(target: Target, scope: Scope): Promise<string> {
  const token = freezeToken()
  const original = await scopeStatus(target, scope)
  const revision = original.state === 'locked'
    ? original.revision
    : await lockScopeFromCurrentRevision(target, scope, token)
  try {
    const version = 'qa-' + randomUUID()
    const created = await postJson(target, '/api/v1/release/create', {
      environment_key: scope.env, pipeline_key: scope.pipeline, version,
      expected_freeze_revision: revision, request_id: randomUUID(), reason: '',
    })
    if (created.status !== 200) throw new Error(`Publish fixture create failed: ${created.status} ${created.json?.error_type}`)
    const active = await getJson(target, '/api/v1/release/active?' + new URLSearchParams({ environment_key: scope.env, pipeline_key: scope.pipeline }))
    const switched = await postJson(target, '/api/v1/release/activate', {
      environment_key: scope.env, pipeline_key: scope.pipeline, version,
      expected_active_revision: active.json?.data?.active?.revision ?? '0', request_id: randomUUID(), reason: '',
    })
    if (switched.status !== 200) throw new Error(`Publish fixture activation failed: ${switched.status} ${switched.json?.error_type}`)
    return version
  } finally {
    if (original.state !== 'locked') await unlockScopeFromCurrentRevision(target, scope, token)
  }
}
