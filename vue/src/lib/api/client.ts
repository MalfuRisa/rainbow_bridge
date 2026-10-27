import type { ApiResponse, OperateResponse } from './types'
import { ApiError, ErrorType, handleApiError } from './error'
import { apiURL } from './url'
import { fetchApi, isSessionError, StaleSessionError, isSessionBoundRequest } from './http'
import { sessionGeneration } from '../auth/session'
import { operationMessage } from './messages'
import { isScopeError, scopeProblem } from '@/lib/scope'
export { getBasePath } from './url'

export interface RequestOptions {
  headers?: Record<string, string>
  showErrorToast?: boolean
  handleAuth?: boolean
}

async function readJson<T>(response: Response): Promise<ApiResponse<T>> {
  if (!response.headers.get('content-type')?.includes('application/json')) {
    throw new ApiError(502, '服务响应格式不正确，请重试')
  }
  const body = await response.json() as ApiResponse<T> & { operate_response?: OperateResponse; error_type?: string }
  if (!body || typeof body !== 'object') throw new ApiError(502, '服务响应格式不正确，请重试')
  const operation = body.operate_response
  const operationFailed = operation && operation.code !== 0 && operation.code !== 200
  const code = operationFailed ? operation.code : body.code
  if (code !== undefined && code !== 0 && code !== 200) {
    const error = new ApiError(code, body.error || (operationFailed ? operation.msg : body.msg) || '操作失败', '', ErrorType.API)
    error.errorType = body.error_type ?? ''
    error.message = operationMessage(error.errorType, error.message)
    if (isScopeError(error)) scopeProblem.value = error.message
    error.payload = body as unknown as Record<string, unknown>
    throw error
  }
  return { code: body.code ?? 0, msg: body.msg || 'success', error: body.error || '', data: body.data }
}

export class ApiClient {
  private async send<T>(path: string, init: RequestInit, options: RequestOptions): Promise<ApiResponse<T>> {
    const batch = sessionGeneration()
    const bound = isSessionBoundRequest(apiURL(path))
    try {
      const response = await fetchApi(apiURL(path), init, options)
      const body = await readJson<T>(response)
      if (bound && batch !== sessionGeneration()) throw new StaleSessionError()
      return body
    } catch (error) {
      if (bound && batch !== sessionGeneration() && !isSessionError(error)) throw new StaleSessionError()
      // Authentication errors belong to the auth form/lifecycle. No request body,
      // password, CSRF, response payload or headers are logged here.
      if (path.startsWith('/api/v1/auth/') || isSessionError(error) ||
          (error instanceof ApiError && error.errorType.startsWith('AUTH_'))) throw error
      handleApiError(error, { showErrorToast: options.showErrorToast })
    }
  }

  request<T>(method: 'GET' | 'POST' | 'PUT' | 'DELETE', path: string,
    data?: Record<string, unknown>, options: RequestOptions = {}): Promise<ApiResponse<T>> {
    const url = apiURL(path)
    if (method === 'GET') {
      Object.entries(data ?? {}).forEach(([key, value]) => {
        if (value !== undefined && value !== null && value !== '') url.searchParams.set(key, String(value))
      })
    }
    // send accepts an API-relative path, including its query, so the base is added once.
    const query = url.search
    return this.send<T>(path.split('?')[0] + query, {
      method, headers: { 'Content-Type': 'application/json', ...options.headers },
      body: method === 'GET' || data === undefined ? undefined : JSON.stringify(data),
    }, options)
  }
  get<T>(path: string, params?: Record<string, unknown>, options?: RequestOptions) {
    return this.request<T>('GET', path, params, options)
  }
  post<T>(path: string, data?: Record<string, unknown>, options?: RequestOptions) {
    return this.request<T>('POST', path, data, options)
  }
  put<T>(path: string, data?: Record<string, unknown>, options?: RequestOptions) {
    return this.request<T>('PUT', path, data, options)
  }
  delete<T>(path: string, data?: Record<string, unknown>, options?: RequestOptions) {
    return this.request<T>('DELETE', path, data, options)
  }
  upload<T>(path: string, data: FormData, options: RequestOptions = {}) {
    return this.send<T>(path, { method: 'POST', body: data, headers: options.headers }, options)
  }
  async downloadArchive(path: string, init: RequestInit = {}) {
    const batch = sessionGeneration()
    const response = await fetchApi(apiURL(path), init)
    const contentType = response.headers.get('content-type') ?? ''
    if (contentType.includes('json') || contentType.includes('text/')) {
      throw new ApiError(502, '导出响应不是有效的归档文件，请重试')
    }
    const blob = await response.blob()
    if (batch !== sessionGeneration()) throw new StaleSessionError()
    if (!blob.size) throw new ApiError(502, '导出文件为空，请重试')
    const signature = new Uint8Array(await blob.slice(0, 4).arrayBuffer())
    if (batch !== sessionGeneration()) throw new StaleSessionError()
    if (!((signature[0] === 0x50 && signature[1] === 0x4b && signature[2] === 3 && signature[3] === 4) ||
      (signature[0] === 0x1f && signature[1] === 0x8b))) throw new ApiError(502, '下载内容不是有效的 ZIP 或 GZIP 文件')
    const disposition = response.headers.get('content-disposition') || ''
    const encoded = /filename\*=UTF-8''([^;]+)/i.exec(disposition)?.[1]
    const plain = /filename="([^"]+)"|filename=([^;]+)/i.exec(disposition)
    let filename = plain?.[1] || plain?.[2]?.trim() || ''
    if (encoded) { try { filename = decodeURIComponent(encoded) } catch { /* Keep plain filename. */ } }
    filename = filename.replace(/[\\/:*?"<>|\x00-\x1f]/g, '_')
    return { blob, filename, source: response.headers.get('x-rainbow-export-source') || '',
      snapshotId: response.headers.get('x-rainbow-snapshot-id') || '', version: response.headers.get('x-rainbow-version') || '' }
  }
  async download(path: string, init: RequestInit = {}): Promise<Blob> {
    return (await this.downloadArchive(path, init)).blob
  }
}
export const apiClient = new ApiClient()
export const request = apiClient.request.bind(apiClient)
export const get = apiClient.get.bind(apiClient)
export const post = apiClient.post.bind(apiClient)
export const upload = apiClient.upload.bind(apiClient)
