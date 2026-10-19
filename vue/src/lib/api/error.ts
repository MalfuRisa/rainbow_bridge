import { toast } from 'vue-sonner'

export enum ErrorType {
  NETWORK = 'network',
  HTTP = 'http',
  API = 'api',
  VALIDATION = 'validation',
  UNKNOWN = 'unknown'
}

export interface ErrorContext {
  endpoint?: string
  method?: string
  params?: Record<string, unknown>
  data?: Record<string, unknown>
  timestamp: number
  userAgent: string
  url: string
}

export class ApiError extends Error {
  code: number
  detail: string
  type: ErrorType
  context?: ErrorContext
  /** machine-readable 错误分类(如 SCOPE_FROZEN);非冻结错误为空串 */
  errorType: string
  /** 原始响应体(供展示实际受阻范围等声明式字段) */
  payload?: Record<string, unknown>

  constructor(
    code: number,
    message: string,
    detail: string = '',
    type: ErrorType = ErrorType.API,
    context?: ErrorContext
  ) {
    super(message)
    this.code = code
    this.detail = detail
    this.type = type
    this.context = context
    this.errorType = ''
    this.name = 'ApiError'
  }

  toJSON() {
    return {
      name: this.name,
      code: this.code,
      message: this.message,
      detail: this.detail,
      type: this.type,
      errorType: this.errorType,
      context: this.context
    }
  }
}

/** 判断错误是否为冻结拦截(409 SCOPE_FROZEN 或相关 503)。 */
export function isFreezeBlockedError(error: unknown): boolean {
  if (!(error instanceof ApiError)) return false
  return error.errorType === 'SCOPE_FROZEN'
}

/** 冻结类错误的可读信息(含实际受阻范围文本)。 */
export function freezeErrorMessage(error: unknown): string {
  if (!(error instanceof ApiError)) {
    return error instanceof Error ? error.message : '未知错误'
  }
  return error.detail ? `${error.message}: ${error.detail}` : error.message
}

export function formatError(error: unknown): string {
  if (error instanceof ApiError) {
    return error.detail ? `${error.message}: ${error.detail}` : error.message
  }
  return error instanceof Error ? error.message : '未知错误'
}

export function logError(error: unknown, context?: ErrorContext) {
  const errorInfo = {
    error: error instanceof Error ? error.message : error,
    stack: error instanceof Error ? error.stack : undefined,
    context: {
      ...context,
      timestamp: context?.timestamp || Date.now(),
      userAgent: navigator.userAgent,
      url: window.location.href
    }
  }

  // 可以在这里添加更多的日志记录逻辑，比如发送到服务器
  console.error('API Error:', errorInfo)
}

export function handleApiError(error: unknown, options?: {
  showErrorToast?: boolean
  context?: ErrorContext
}): never {
  const showErrorToast = options?.showErrorToast !== false
  const context = options?.context

  // 记录错误
  logError(error, context)

  if (error instanceof ApiError) {
    const errorMessage = formatError(error)
    
    if (showErrorToast) {
      toast.error(errorMessage)
    }
    throw error
  }

  const message = error instanceof Error ? error.message : '网络连接失败'
  const apiError = new ApiError(
    -1,
    message,
    '',
    ErrorType.NETWORK,
    context
  )

  if (showErrorToast) {
    toast.error(message)
  }
  throw apiError
}
