import { Page, expect } from '@playwright/test'
import { Target, url } from './env'
import { ensureAdminSession } from './net'

/** 页面操作辅助：只使用侦察确认过的稳定定位(文本/label/id/lucide 图标 class)。 */

export const PAGE_ROUTES = [
  { path: '/', title: '项目概览' },
  { path: '/config', title: '配置管理' },
  { path: '/environments', title: '环境管理' },
  { path: '/resources', title: '资源管理' },
  { path: '/migration', title: '配置迁移' },
  { path: '/import-export', title: '导入导出' },
] as const

export async function gotoPage(page: Page, target: Target, pathname: string): Promise<void> {
  // 管理页面需要一个有效会话:进入页面前确保浏览器上下文已登录
  await ensureBrowserSession(page, target)
  const response = await page.goto(url(target, pathname), { waitUntil: 'domcontentloaded' })
  if (!response || response.status() >= 400) {
    throw new Error(`导航失败 ${url(target, pathname)} status=${response?.status()}`)
  }
}

/** 当前页 h1(由 Layout 渲染)应等于给定中文标题。侧边栏 logo 也是 h1，需按文本收敛。 */
export async function expectPageTitle(page: Page, title: string): Promise<void> {
  const heading = page.getByRole('heading', { level: 1 }).filter({ hasText: title })
  await expect(heading.first()).toBeVisible()
  await expect(heading.first()).toHaveText(title)
}

/** 配置页：必须先选环境与渠道，否则列表不发请求。 */
export async function selectEnvPipelineOnConfigPage(
  page: Page,
  envName: string,
  pipelineName: string,
): Promise<void> {
  const envSelect = page.getByRole('combobox').first()
  await envSelect.click()
  await page.getByRole('option', { name: envName }).click()
  const pipelineSelect = page.getByRole('combobox').nth(1)
  await pipelineSelect.click()
  await page.getByRole('option', { name: pipelineName }).click()
}

/** 配置页保存成功后等待列表出现该别名行。 */
export async function expectConfigRow(page: Page, alias: string): Promise<void> {
  await expect(page.locator('tr').filter({ hasText: alias }).first()).toBeVisible()
}

export async function waitToast(page: Page, text: string): Promise<void> {
  await expect(page.locator('[data-sonner-toast]').filter({ hasText: text }).first()).toBeVisible({
    timeout: 10_000,
  })
}

/** 通过页面表单创建一条配置；type 用中文标签，内容按类型分支填写。 */
export async function createConfigViaPage(
  page: Page,
  input: {
    name: string
    alias: string
    typeLabel: string
    content?: string
    description?: string
    uploadFile?: { name: string; mimeType: string; buffer: Buffer }
  },
): Promise<void> {
  await page.getByRole('button', { name: '新增配置' }).click()
  const dialog = page.getByRole('dialog').filter({ has: page.locator('#alias') })
  await dialog.locator('#name').fill(input.name)
  await dialog.locator('#alias').fill(input.alias)

  if (input.typeLabel !== '文本') {
    await dialog.getByRole('combobox').click()
    await page.getByRole('option', { name: new RegExp(input.typeLabel) }).click()
  }

  if (input.typeLabel === '布尔值') {
    await dialog.getByRole('switch').click()
  } else if (input.typeLabel === '键值对') {
    await dialog.getByPlaceholder('键名').first().fill('qa_key')
    await dialog.getByPlaceholder('键值').first().fill('qa_value')
  } else if (input.typeLabel === '色彩') {
    await dialog.locator('input[maxlength="7"]').fill(input.content ?? '#FF8800')
  } else if (input.typeLabel === '图片' || input.typeLabel === '文件') {
    const chooserPromise = page.waitForEvent('filechooser')
    await dialog.getByText(input.typeLabel === '图片' ? '点击上传图片' : '点击上传文件').click()
    const chooser = await chooserPromise
    await chooser.setFiles({
      name: input.uploadFile?.name ?? 'qa-page.png',
      mimeType: input.uploadFile?.mimeType ?? 'image/png',
      buffer: input.uploadFile?.buffer ?? Buffer.from('cG5n', 'base64'),
    })
  } else if (input.content !== undefined) {
    await dialog.locator('#content').fill(input.content)
  }

  if (input.description !== undefined) {
    await dialog.locator('#description').fill(input.description)
  }
  await dialog.getByRole('button', { name: '保存' }).click()
  await expectConfigRow(page, input.alias)
}

/** 打开某行配置的编辑对话框(收敛到包含表单的那个 dialog)。 */
export async function openEditDialog(page: Page, alias: string): Promise<ReturnType<Page['getByRole']>> {
  const row = page.locator('tr').filter({ hasText: alias }).first()
  await row.locator('button .lucide-pencil').first().click()
  const dialog = page.getByRole('dialog').filter({ has: page.locator('#alias') })
  await expect(dialog).toBeVisible()
  return dialog as ReturnType<Page['getByRole']>
}
/** 为浏览器上下文准备管理会话:登录一次并注入 Cookie(客户端请求自行获取 CSRF)。 */
export async function ensureBrowserSession(page: Page, target: Target): Promise<void> {
  const session = await ensureAdminSession(target)
  const separator = session.cookie.indexOf('=')
  await page.context().addCookies([{
    name: session.cookie.slice(0, separator),
    value: session.cookie.slice(separator + 1),
    url: target.origin + (target.prefix || '') + '/',
    httpOnly: true, sameSite: 'Lax', secure: false,
  }])
  const me = await page.request.get(url(target, '/api/v1/auth/me'))
  if (me.status() !== 200) throw new Error(`浏览器共享会话不可用: me HTTP=${me.status()}`)
}
/** 断言页面实际发出的管理请求携带会话 Cookie(读取类页面走这条路径)。 */
export async function expectManagementRequestCarriesCookie(page: Page, pathFragment: string): Promise<void> {
  const cookieName = 'rb_admin_auth_dev'
  await page.waitForRequest(
    (request) => request.url().includes(pathFragment) && (request.headers()['cookie'] ?? '').includes(cookieName),
    { timeout: 15_000 },
  )
}
