import { readonly, shallowRef } from 'vue'

export type AuthStatus = 'unknown' | 'authenticated' | 'anonymous' | 'failed'
export interface AuthIdentity {
  user: { id: string; username: string; role: string }
  session: { expires_at: string; idle_expires_at: string }
  csrf_token: string
}

// Memory only. This boundary has no dependency on stores, routes or HTTP.
const status = shallowRef<AuthStatus>('unknown')
const identity = shallowRef<AuthIdentity | null>(null)
let generation = 0
export type ClearReason = 'restore' | 'expired' | 'forbidden' | 'logout' | 'login'
const invalidators = new Set<(reason: ClearReason) => void>()
export const sessionStatus = readonly(status)
export const sessionIdentity = readonly(identity)
export const sessionProblem = shallowRef<{ message: string; refreshable: boolean } | null>(null)
export const sessionGeneration = () => generation
export const sessionCsrf = () => identity.value?.csrf_token ?? ''
export function onSessionCleared(callback: (reason: ClearReason) => void) {
  invalidators.add(callback)
  return () => invalidators.delete(callback)
}
export function clearSession(next: AuthStatus = 'anonymous', reason: ClearReason = 'restore') {
  generation += 1
  identity.value = null
  sessionProblem.value = null
  status.value = next
  invalidators.forEach(callback => callback(reason))
}
export function acceptSession(value: AuthIdentity) {
  if (!value.user?.username || !value.csrf_token) throw new Error('登录服务响应不完整，请重试')
  identity.value = value
  sessionProblem.value = null
  status.value = 'authenticated'
}
