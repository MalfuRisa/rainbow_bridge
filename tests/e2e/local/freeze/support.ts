import { Target } from '../support/env'
import { getJson, postJson } from '../support/net'
import { Scope, ensureScope } from '../support/scopes'

export interface ScopeStatus {
  state: 'unlocked' | 'locked' | string
  revision: string
  admin_configured: boolean
  last_reason: string
  last_changed_at: string
}

export async function scopeStatus(target: Target, scope: Scope): Promise<ScopeStatus> {
  const response = await getJson(
    target,
    `/api/v1/freeze/status?environment_key=${encodeURIComponent(scope.env)}&pipeline_key=${encodeURIComponent(scope.pipeline)}`,
  )
  if (response.json?.code !== 200) {
    throw new Error(`freeze status failed: ${response.text.slice(0, 200)}`)
  }
  return response.json.data as ScopeStatus
}

export async function lockScope(
  target: Target,
  scope: Scope,
  token: string,
  reason = 'QA 冻结测试',
  expectedRevision = '0',
): Promise<{ httpStatus: number; body: any }> {
  const response = await postJson(
    target,
    '/api/v1/freeze/lock',
    {
      environment_key: scope.env,
      pipeline_key: scope.pipeline,
      reason,
      expected_revision: expectedRevision,
    },
  )
  return { httpStatus: response.status, body: response.json }
}

export async function unlockScope(
  target: Target,
  scope: Scope,
  token: string,
  reason = 'QA 解除冻结',
  expectedRevision = '1',
): Promise<{ httpStatus: number; body: any }> {
  const response = await postJson(
    target,
    '/api/v1/freeze/unlock',
    {
      environment_key: scope.env,
      pipeline_key: scope.pipeline,
      reason,
      expected_revision: expectedRevision,
    },
  )
  return { httpStatus: response.status, body: response.json }
}

export async function lockScopeFromCurrentRevision(target: Target, scope: Scope, token: string): Promise<string> {
  const status = await scopeStatus(target, scope)
  const result = await lockScope(target, scope, token, 'QA 冻结测试', status.revision)
  if (result.httpStatus !== 200) {
    throw new Error(`lock failed: ${JSON.stringify(result.body).slice(0, 200)}`)
  }
  return result.body.data.revision as string
}

export async function unlockScopeFromCurrentRevision(target: Target, scope: Scope, token: string): Promise<string> {
  const status = await scopeStatus(target, scope)
  const result = await unlockScope(target, scope, token, 'QA 解除冻结', status.revision)
  if (result.httpStatus !== 200) {
    throw new Error(`unlock failed: ${JSON.stringify(result.body).slice(0, 200)}`)
  }
  return result.body.data.revision as string
}

export async function seedScope(target: Target, scope: Scope): Promise<void> {
  await ensureScope(target, scope)
}