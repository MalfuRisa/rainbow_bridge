import { createHash } from 'crypto'
import { Target, url } from './env'

export interface RawResponse {
  status: number
  contentType: string
  bytes: Buffer
  text: string
  json: any
}

export interface MultipartFile {
  field: string
  filename: string
  contentType: string
  bytes: Buffer
}

export function sha256Hex(bytes: Buffer): string {
  return createHash('sha256').update(bytes).digest('hex')
}

/**
 * 管理接口会话:登录一次后所有管理请求自动携带 Cookie 与 CSRF。
 * 身份只来自服务端会话,测试不再持有或传递旧管理密钥。
 */
interface AdminSession {
  cookie: string
  csrf: string
}

const adminSessions = new Map<string, Promise<AdminSession>>()

/** 需要登录的请求:除 auth、业务 runtime、版本信息外的 /api/ 路由。 */
export function isManagementPath(pathname: string): boolean {
  if (!pathname.startsWith('/api/')) return false
  if (pathname.startsWith('/api/v1/auth/')) return false
  if (pathname.startsWith('/api/v1/runtime/')) return false
  if (pathname === '/api/v1/version' || pathname === '/api/v1/version/latest') return false
  if (pathname.startsWith('/api/v1/asset/file/') || pathname.startsWith('/api/v1/release/asset/')) return false
  if (pathname === '/ping') return false
  return true
}

/** 预检之外的写方法需要 CSRF 头。 */
function needsCSRF(method: string): boolean {
  return !['GET', 'HEAD', 'OPTIONS'].includes(method.toUpperCase())
}

async function createAdminSession(target: Target): Promise<AdminSession> {
  const username = process.env.RB_QA_ADMIN_USERNAME ?? 'admin'
  const password = process.env.RB_QA_ADMIN_PASSWORD ?? ''
  if (!password) {
    throw new Error('缺少 RB_QA_ADMIN_PASSWORD:管理接口需要登录,套件必须由 script/test-local.ps1 驱动')
  }
  const response = await fetch(url(target, '/api/v1/auth/login'), {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      Origin: target.origin,
    },
    body: JSON.stringify({ username, password }),
  })
  const payload = await response.json().catch(() => null)
  if (!response.ok) {
    throw new Error(`管理登录失败 HTTP=${response.status} ${JSON.stringify(payload).slice(0, 200)}`)
  }
  const setCookie = response.headers.get('set-cookie') ?? ''
  const cookie = setCookie.split(';')[0]
  const csrf = payload?.data?.csrf_token ?? ''
  if (!cookie || !csrf) {
    throw new Error('管理登录响应缺少 Cookie 或 csrf_token')
  }
  return { cookie, csrf }
}

/** 每个实例只登录一次(失败后不缓存失败结果,便于诊断)。 */
export async function ensureAdminSession(target: Target): Promise<AdminSession> {
  const key = target.origin
  let pending = adminSessions.get(key)
  if (!pending) {
    pending = createAdminSession(target)
    adminSessions.set(key, pending)
  }
  try {
    return await pending
  } catch (error) {
    adminSessions.delete(key)
    throw error
  }
}

/** 登出并清除缓存会话(退出类用例使用)。 */
export async function resetAdminSession(target: Target): Promise<void> {
  adminSessions.delete(target.origin)
}

/** 真实 HTTP 请求，直连本轮 127.0.0.1 实例；不使用框架内 PerformRequest。 */
export async function request(
  target: Target,
  pathname: string,
  init: RequestInit = {},
): Promise<RawResponse> {
  // resolveUrl 可能已返回绝对 URL；此时不得再拼接部署前缀(否则会构造出畸形 URL)
  const requestUrl = pathname.startsWith('http') ? pathname : url(target, pathname)
  const method = (init.method ?? 'GET').toString().toUpperCase()
  const headers = new Headers(init.headers as HeadersInit)
  const normalizedPath = new URL(requestUrl).pathname.slice(target.prefix.length)
  if (isManagementPath(normalizedPath)) {
    // 管理接口统一走登录会话:自动携带 Cookie,写操作再带 CSRF 与合法 Origin
    const session = await ensureAdminSession(target)
    headers.set('Cookie', session.cookie)
    if (needsCSRF(method)) {
      headers.set('X-CSRF-Token', session.csrf)
      if (!headers.has('Origin')) headers.set('Origin', target.origin)
    }
  }
  const response = await fetch(requestUrl, { ...init, headers })
  const arrayBuffer = await response.arrayBuffer()
  const bytes = Buffer.from(arrayBuffer)
  const text = bytes.toString('utf8')
  let json: any = null
  try {
    json = JSON.parse(text)
  } catch {
    json = null
  }
  return {
    status: response.status,
    contentType: response.headers.get('content-type') ?? '',
    bytes,
    text,
    json,
  }
}

export async function getJson(
  target: Target,
  pathname: string,
  headers: Record<string, string> = {},
): Promise<RawResponse> {
  return request(target, pathname, { method: 'GET', headers })
}

export async function postJson(
  target: Target,
  pathname: string,
  body: unknown,
  headers: Record<string, string> = {},
): Promise<RawResponse> {
  return request(target, pathname, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...headers },
    body: JSON.stringify(body),
  })
}

/** 以原始字符串发送 JSON，避免测试端先做数字/转义加工(T06 精度样本需要)。 */
export async function postRawJson(
  target: Target,
  pathname: string,
  rawBody: string,
  headers: Record<string, string> = {},
): Promise<RawResponse> {
  return request(target, pathname, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...headers },
    body: rawBody,
  })
}

export async function postMultipart(
  target: Target,
  pathname: string,
  fields: Record<string, string>,
  files: MultipartFile[],
  headers: Record<string, string> = {},
): Promise<RawResponse> {
  const form = new FormData()
  for (const [key, value] of Object.entries(fields)) {
    form.append(key, value)
  }
  for (const file of files) {
    form.append(
      file.field,
      new Blob([new Uint8Array(file.bytes)], { type: file.contentType }),
      file.filename,
    )
  }
  return request(target, pathname, { method: 'POST', body: form as any, headers })
}

export function businessCode(response: RawResponse): number | undefined {
  const code = response.json?.code
  return typeof code === 'number' ? code : undefined
}

export function expectBusinessOk(response: RawResponse, context: string): void {
  if (businessCode(response) !== 200) {
    throw new Error(`${context}: 业务失败 HTTP=${response.status} body=${response.text.slice(0, 400)}`)
  }
}