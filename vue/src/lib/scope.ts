import { shallowRef } from 'vue'
import type { Environment } from './types'
import { ApiError } from './api/error'

export interface ScopeTarget { environmentKey: string; pipelineKey: string; environmentId: string; scopeId: string }
const catalog = shallowRef<Environment[]>([])
export const scopeProblem = shallowRef('')
export function registerScopes(environments: Environment[]) { catalog.value = environments }
export const scopeCatalog = catalog
export const scopeIdFor = (env: string, pipeline: string) => catalog.value.find(item => item.key === env)?.pipelines.find(item => item.key === pipeline)?.scopeId || ''
export function scopeError(kind = 'SCOPE_INVALID_REQUEST') {
  const messages: Record<string, string> = {
    SCOPE_INVALID_REQUEST: '范围信息不完整，请刷新环境数据后重新操作。',
    SCOPE_IDENTITY_CONFLICT: '该环境或渠道已被重新创建，请刷新后重新选择；旧操作不能继续。',
    SCOPE_DELETED: '该环境或渠道已删除，请刷新后重新选择。',
  }
  const error = new ApiError(kind === 'SCOPE_INVALID_REQUEST' ? 400 : kind === 'SCOPE_DELETED' ? 410 : 409, messages[kind] || messages.SCOPE_INVALID_REQUEST!)
  error.errorType = kind
  scopeProblem.value = error.message
  return error
}
export function requireIdentity(value: string | undefined): string {
  if (!value || !/^[a-f\d]{8}-[a-f\d]{4}-[a-f\d]{4}-[a-f\d]{4}-[a-f\d]{12}$/i.test(value)) throw scopeError()
  return value
}
export function captureScope(environmentKey: string, pipelineKey: string): ScopeTarget {
  const env = catalog.value.find(item => item.key === environmentKey)
  const pipeline = env?.pipelines.find(item => item.key === pipelineKey)
  return { environmentKey, pipelineKey, environmentId: requireIdentity(env?.environmentId), scopeId: requireIdentity(pipeline?.scopeId) }
}
export const isCurrentScope = (target: Pick<ScopeTarget, 'environmentKey' | 'pipelineKey' | 'scopeId'>) =>
  Boolean(target.scopeId) && scopeIdFor(target.environmentKey, target.pipelineKey) === target.scopeId
export function assertScope(environmentKey: string, pipelineKey: string, scopeId: string) {
  requireIdentity(scopeId)
  if (scopeIdFor(environmentKey, pipelineKey) !== scopeId) throw scopeError('SCOPE_IDENTITY_CONFLICT')
}
export function assertEnvironment(environmentKey: string, environmentId: string) {
  requireIdentity(environmentId)
  if (catalog.value.find(item => item.key === environmentKey)?.environmentId !== environmentId) throw scopeError('SCOPE_IDENTITY_CONFLICT')
}
export async function readScope<T>(environmentKey: string, pipelineKey: string, read: (scopeId: string) => Promise<T>, scopeId = scopeIdFor(environmentKey, pipelineKey)): Promise<T> {
  assertScope(environmentKey, pipelineKey, scopeId)
  const result = await read(scopeId)
  assertScope(environmentKey, pipelineKey, scopeId)
  return result
}
export const isScopeError = (error: unknown) => error instanceof ApiError && ['SCOPE_INVALID_REQUEST', 'SCOPE_IDENTITY_CONFLICT', 'SCOPE_DELETED'].includes(error.errorType)
