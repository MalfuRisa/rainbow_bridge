import { expect, type Page, type APIResponse, type Response } from '@playwright/test'

export function credentials() {
  const username = process.env.RB_QA_ADMIN_USERNAME
  const password = process.env.RB_QA_ADMIN_PASSWORD
  if (!username || !password) throw new Error('真实测试需要后端进程注入 RB_QA_ADMIN_USERNAME、RB_QA_ADMIN_PASSWORD；不会以Mock或跳过代替。')
  return { username, password }
}
export function apiUrl(page: Page, endpoint: string) {
  const root = process.env.RAINBOW_AUTH_UI_URL || 'http://127.0.0.1:3001/rainbow-bridge/'
  return new URL('api/v1/' + endpoint, root.endsWith('/') ? root : root + '/').toString()
}
export async function checked(response: APIResponse | Response, endpoint: string) {
  const body = await response.json().catch(() => null)
  // Only non-sensitive metadata in errors; never print login/me response bodies.
  if (!response.ok() || !body || ![0, 200].includes(body.code)) {
    throw new Error(endpoint + ': HTTP=' + response.status() + ', code=' + String(body?.code ?? '') +
      ', error_type=' + String(body?.error_type ?? '') + '；检查后端脱敏日志。')
  }
  return body.data
}
export async function uiLogin(page: Page, target = 'config?env=default&pipeline=default') {
  const { username, password } = credentials()
  await page.goto(target)
  await expect(page.getByRole('heading', { name: '登录工作台', exact: true })).toBeVisible()
  try {
    await page.getByLabel('账号', { exact: true }).fill(username)
    await page.getByLabel('密码', { exact: true }).fill(password)
  } catch { throw new Error('登录表单输入未完成；已隐藏测试凭据。') }
  const response = page.waitForResponse(response => new URL(response.url()).pathname.endsWith('/api/v1/auth/login'))
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await checked(await response, 'auth/login')
  await expect(page.getByRole('heading', { name: '配置管理', exact: true })).toBeVisible()
}
export async function privateRequest<T>(endpoint: string, action: () => Promise<T>): Promise<T> {
  try { return await action() }
  catch { throw new Error(endpoint + ': 网络请求未完成；已隐藏Cookie/CSRF及测试凭据。') }
}
export async function management(page: Page) {
  const me = await checked(await privateRequest('auth/me', () => page.request.get(apiUrl(page, 'auth/me'))), 'auth/me')
  const origin = new URL(page.url()).origin
  const headers = { Origin: origin, 'X-CSRF-Token': String(me.csrf_token) }
  return {
    async post(endpoint: string, data: unknown) {
      return checked(await privateRequest(endpoint, () => page.request.post(apiUrl(page, endpoint), { headers, data })), endpoint)
    },
    async get(endpoint: string) {
      return checked(await privateRequest(endpoint, () => page.request.get(apiUrl(page, endpoint), { headers })), endpoint)
    },
    async archive(data: unknown) {
      const response = await privateRequest('transfer/export', () => page.request.post(apiUrl(page, 'transfer/export'), { headers, data }))
      if (!response.ok() || !response.headers()['content-type']?.includes('application/zip')) {
        throw new Error('transfer/export: 未返回成功ZIP，HTTP=' + response.status())
      }
      const bytes = await response.body()
      expect(bytes.length > 0).toBe(true)
      return bytes
    },
  }
}
export async function uiWrite(page: Page, endpoint: string, action: () => Promise<void>) {
  const response = page.waitForResponse(response => new URL(response.url()).pathname.endsWith('/api/v1/' + endpoint) && response.request().method() === 'POST')
  await action()
  return checked(await response, endpoint)
}
