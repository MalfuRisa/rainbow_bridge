/**
 * API客户端工具类，封装了HTTP请求逻辑
 */
import { getApiBaseUrl } from '../utils'
import type { ApiResponse, OperateResponse } from './types'
import { ApiError, ErrorType, handleApiError } from './error'

export function getBasePath(): string {
  // 生产环境：优先使用运行时注入的 basePath
  if (typeof window !== 'undefined' && window.__BASE_PATH__) {
    const injected = window.__BASE_PATH__
    // 如果不是占位符，使用注入的值
    if (injected !== '__BASE_PATH__') {
      return injected
    }
  }
  
  const viteBase = import.meta.env.BASE_URL || ''
  // 如果 Vite 的 BASE_URL 是占位符，返回空字符串
  if (viteBase.includes('__BASE_PATH__')) {
    return ''
  }
  return viteBase
}

interface RawApiResponse<T = unknown> extends ApiResponse<T> {
  operate_response?: OperateResponse
}

async function handleResponse<T>(response: Response): Promise<ApiResponse<T>> {
  const contentType = response.headers.get('content-type')
  const isJson = contentType?.includes('application/json')
  // 非 2xx 也要读取响应体:冻结拦截等错误用真实 HTTP 状态 + error_type 表达
  const body: (RawApiResponse<T> & { error_type?: string }) | null = isJson
    ? await response.json().catch(() => null)
    : null

  if (!response.ok) {
    const apiError = new ApiError(
      typeof body?.code === 'number' ? body.code : response.status,
      body?.error || body?.msg || `HTTP error: ${response.status}`,
      '',
      ErrorType.HTTP
    )
    apiError.errorType = body?.error_type || ''
    apiError.payload = (body as unknown as Record<string, unknown>) || undefined
    throw apiError
  }

  if (body) {
    if (
      body.operate_response &&
      body.operate_response.code !== 0 &&
      body.operate_response.code !== 200
    ) {
      throw new ApiError(
        body.operate_response.code,
        body.operate_response.msg || '操作失败',
        body.operate_response.error || '',
        ErrorType.API
      )
    }

    if (body.code !== undefined && body.code !== 0 && body.code !== 200) {
      throw new ApiError(
        body.code,
        body.msg || '请求失败',
        body.error || '',
        ErrorType.API
      )
    }

    // 标准化响应格式
    return {
      code: body.code || 0,
      msg: body.msg || 'success',
      error: body.error || '',
      data: body.data
    }
  }

  return {
    code: 0,
    msg: 'success',
    error: '',
    data: {} as T
  }
}

/**
 * API客户端类，封装了HTTP请求逻辑
 */
export class ApiClient {
  /**
   * API基础URL
   */
  private baseUrl: string
  /**
   * 应用基础路径
   */
  private basePath: string

  /**
   * 构造函数
   */
  constructor() {
    this.baseUrl = getApiBaseUrl() || ''
    this.basePath = getBasePath()
  }

  /**
   * 获取完整路径
   * @param path - API路径
   * @returns 完整路径
   */
  private getFullPath(path: string): string {
    return path
  }

  /**
   * 获取URL对象
   * @param path - API路径
   * @returns URL对象
   */
  private getUrl(path: string): URL {
    if (path.startsWith('/api/')) {

      const baseUrl = this.baseUrl || window.location.origin
      // 如果baseUrl是相对路径，需要和basePath结合
      if (this.baseUrl && !this.baseUrl.startsWith('http')) {
        const normalizedBase = this.baseUrl.replace(/\/$/, '')
        return new URL(`${normalizedBase}${path}`, window.location.origin)
      }
      // 如果没有设置baseUrl，使用basePath
      if (!this.baseUrl) {
        const normalizedBasePath = this.basePath.replace(/\/$/, '')
        return new URL(`${normalizedBasePath}${path}`, window.location.origin)
      }
      return new URL(path, baseUrl)
    }
    const fullPath = this.getFullPath(path)
    const baseUrl = this.baseUrl || window.location.origin
    return new URL(`${fullPath}`, baseUrl)
  }

  /**
   * 发送HTTP请求
   * @param method - HTTP方法
   * @param path - API路径
   * @param data - 请求数据
   * @param options - 选项
   * @returns API响应
   */
  async request<T>(
    method: 'GET' | 'POST' | 'PUT' | 'DELETE',
    path: string,
    data?: Record<string, unknown>,
    options?: {
      headers?: Record<string, string>
      showErrorToast?: boolean
    }
  ): Promise<ApiResponse<T>> {
    const url = this.getUrl(path)
    const showErrorToast = options?.showErrorToast !== false
    const headers: Record<string, string> = {
      'Content-Type': 'application/json',
      ...options?.headers,
    }

    let body: string | undefined
    if (method === 'GET' && data) {
      Object.entries(data).forEach(([key, value]) => {
        if (value !== undefined && value !== null && value !== '') {
          url.searchParams.append(key, String(value))
        }
      })
    } else if (data) {
      body = JSON.stringify(data)
    }

    try {
      const response = await fetch(url.toString(), {
        method,
        headers,
        body,
        credentials: 'include',
      })

      return await handleResponse<T>(response)
    } catch (error) {
      const context = {
        endpoint: path,
        method,
        params: method === 'GET' ? data : undefined,
        data: method !== 'GET' ? data : undefined,
        timestamp: Date.now(),
        userAgent: navigator.userAgent,
        url: url.toString()
      }

      handleApiError(error, {
        showErrorToast,
        context
      })
    }
  }

  /**
   * 标准化响应处理
   * @param response - API响应
   * @returns 标准化后的响应
   */
  private normalizeResponse<T>(response: ApiResponse<T>): ApiResponse<T> {
    return {
      code: response.code || 0,
      msg: response.msg || 'success',
      error: response.error || '',
      data: response.data
    }
  }

  /**
   * 发送GET请求
   * @param path - API路径
   * @param params - 查询参数
   * @param options - 选项
   * @returns API响应
   */
  async get<T>(
    path: string,
    params?: Record<string, unknown>,
    options?: {
      headers?: Record<string, string>
    }
  ): Promise<ApiResponse<T>> {
    return this.request<T>('GET', path, params, options)
  }

  /**
   * 发送POST请求
   * @param path - API路径
   * @param data - 请求数据
   * @param options - 选项
   * @returns API响应
   */
  async post<T>(
    path: string,
    data?: Record<string, unknown>,
    options?: {
      headers?: Record<string, string>
    }
  ): Promise<ApiResponse<T>> {
    return this.request<T>('POST', path, data, options)
  }

  /**
   * 发送PUT请求
   * @param path - API路径
   * @param data - 请求数据
   * @param options - 选项
   * @returns API响应
   */
  async put<T>(
    path: string,
    data?: Record<string, unknown>,
    options?: {
      headers?: Record<string, string>
    }
  ): Promise<ApiResponse<T>> {
    return this.request<T>('PUT', path, data, options)
  }

  /**
   * 发送DELETE请求
   * @param path - API路径
   * @param data - 请求数据
   * @param options - 选项
   * @returns API响应
   */
  async delete<T>(
    path: string,
    data?: Record<string, unknown>,
    options?: {
      headers?: Record<string, string>
    }
  ): Promise<ApiResponse<T>> {
    return this.request<T>('DELETE', path, data, options)
  }

  /**
   * 上传文件
   * @param path - API路径
   * @param formData - 表单数据
   * @param options - 选项
   * @returns API响应
   */
  async upload<T>(
    path: string,
    formData: FormData,
    options?: {
      headers?: Record<string, string>
      showErrorToast?: boolean
    }
  ): Promise<ApiResponse<T>> {
    const url = this.getUrl(path)
    const showErrorToast = options?.showErrorToast !== false

    try {
      const response = await fetch(url.toString(), {
        method: 'POST',
        body: formData,
        credentials: 'include',
      })

      return await handleResponse<T>(response)
    } catch (error) {
      const file = formData.get('file')
      const context = {
        endpoint: path,
        method: 'POST',
        data: { fileSize: file instanceof File ? file.size : 0 },
        timestamp: Date.now(),
        userAgent: navigator.userAgent,
        url: url.toString()
      }

      handleApiError(error, {
        showErrorToast,
        context
      })
    }
  }
}

// 创建单例实例
export const apiClient = new ApiClient()

// 保持向后兼容
export async function request<T>(
  method: 'GET' | 'POST' | 'PUT' | 'DELETE',
  path: string,
  data?: Record<string, unknown>,
  options?: {
    headers?: Record<string, string>
    showErrorToast?: boolean
  }
): Promise<ApiResponse<T>> {
  return apiClient.request<T>(method, path, data, options)
}

export async function get<T>(
  path: string,
  params?: Record<string, unknown>,
  options?: {
    headers?: Record<string, string>
  }
): Promise<ApiResponse<T>> {
  return apiClient.get<T>(path, params, options)
}

export async function post<T>(
  path: string,
  data?: Record<string, unknown>
): Promise<ApiResponse<T>> {
  return apiClient.post<T>(path, data)
}

export async function upload<T>(
  path: string,
  formData: FormData,
  options?: {
    headers?: Record<string, string>
    showErrorToast?: boolean
  }
): Promise<ApiResponse<T>> {
  return apiClient.upload<T>(path, formData, options)
}
