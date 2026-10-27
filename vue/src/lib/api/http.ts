import { ApiError, ErrorType } from './error'
import { operationMessage } from './messages'
import { isScopeError, scopeProblem } from '@/lib/scope'
import { isTrustedApiURL } from './url'
import { clearSession, sessionCsrf, sessionGeneration, sessionProblem, sessionStatus } from '../auth/session'

export class StaleSessionError extends ApiError {
  constructor() { super(0, '请求所属会话已结束'); this.errorType = 'SESSION_STALE' }
}
export const isSessionError = (error: unknown) => error instanceof ApiError &&
  ['AUTH_REQUIRED', 'AUTH_FORBIDDEN', 'SESSION_STALE'].includes(error.errorType)

export interface HttpOptions { handleAuth?: boolean }

export function isManagementRequest(url: URL) {
  return /\/api\/v1\/(environment|pipeline|config|freeze|release|asset|transfer|backup)\//.test(url.pathname) &&
    !/\/api\/v1\/(asset\/file|release\/asset)\//.test(url.pathname)
}
export function isSessionBoundRequest(url: URL) {
  return isTrustedApiURL(url) && (isManagementRequest(url) || url.pathname.includes('/api/v1/auth/'))
}

export async function responseError(response: Response): Promise<ApiError> {
  const body = response.headers.get('content-type')?.includes('application/json')
    ? await response.json().catch(() => null) : null
  const kind = typeof body?.error_type === 'string' ? body.error_type : ''
  const messages: Record<string, string> = {
    BACKUP_MAINTENANCE: '备份或恢复正在暂停业务写入，请稍后重试；当前输入已保留。',
    AUTH_INVALID_CREDENTIALS: '账号或密码错误',
    AUTH_REQUIRED: '请重新登录',
    AUTH_FORBIDDEN: '账号不可用，请联系管理员',
    AUTH_CSRF_INVALID: '登录状态校验失败，请刷新登录状态后重试',
    AUTH_ORIGIN_DENIED: '来源配置错误，请联系管理员',
    AUTH_RATE_LIMITED: '请求过于频繁，请稍后重试',
  }
  const message = messages[kind] || (response.status >= 500 && kind.startsWith('AUTH_')
    ? '服务暂不可用，请重试' : body?.error || body?.msg || `请求失败：HTTP ${response.status}`)
  const error = new ApiError(response.status, operationMessage(kind, message), '', ErrorType.HTTP)
  error.errorType = kind
  if (isScopeError(error)) scopeProblem.value = error.message
  // Auth payloads may contain credentials or internal diagnostics; never retain them.
  if (!kind.startsWith('AUTH_')) error.payload = body ?? undefined
  const retry = response.headers.get('retry-after') ?? ''
  const seconds = /^\d+$/.test(retry) ? Number(retry) : Math.ceil((Date.parse(retry) - Date.now()) / 1000)
  error.retryAfter = Number.isFinite(seconds) ? Math.min(300, Math.max(0, seconds)) : 0
  return error
}

/** JSON, FormData and ZIP requests share credentials, CSRF and stale-response checks. */
export async function fetchApi(input: string | URL, init: RequestInit = {}, options: HttpOptions = {}) {
  const url = new URL(input, window.location.origin)
  const trusted = isTrustedApiURL(url)
  const management = isManagementRequest(url)
  const sessionBound = isSessionBoundRequest(url)
  if (trusted && management && sessionStatus.value === 'anonymous') {
    const error = new ApiError(401, '请重新登录')
    error.errorType = 'AUTH_REQUIRED'
    throw error
  }
  const batch = sessionGeneration()
  const headers = new Headers(init.headers)
  if (trusted && !['GET', 'HEAD'].includes((init.method ?? 'GET').toUpperCase()) && sessionCsrf()) {
    headers.set('X-CSRF-Token', sessionCsrf())
  }
  let response: Response
  try {
    response = await fetch(url, { ...init, headers, credentials: trusted ? 'include' : 'omit' })
  } catch {
    if (sessionBound && batch !== sessionGeneration()) throw new StaleSessionError()
    throw new ApiError(-1, '服务暂不可用，请重试', '', ErrorType.NETWORK)
  }
  if (sessionBound && batch !== sessionGeneration()) throw new StaleSessionError()
  if (response.ok) return response
  const error = await responseError(response)
  if (sessionBound && batch !== sessionGeneration()) throw new StaleSessionError()
  if (url.pathname.includes('/api/v1/auth/') && !error.errorType.startsWith('AUTH_')) {
    if (error.errorType !== 'API_NOT_FOUND') error.message = response.status === 429 ? '请求过于频繁，请稍后重试' :
      response.status === 401 ? '账号或密码错误' : '服务暂不可用，请重试'
    error.payload = undefined
  }
  if (trusted && ['AUTH_CSRF_INVALID', 'AUTH_ORIGIN_DENIED'].includes(error.errorType)) {
    sessionProblem.value = { message: error.message, refreshable: error.errorType === 'AUTH_CSRF_INVALID' }
  }
  if (trusted && management && options.handleAuth !== false && isSessionError(error)) {
    clearSession('anonymous', error.errorType === 'AUTH_FORBIDDEN' ? 'forbidden' : 'expired')
  }
  throw error
}
