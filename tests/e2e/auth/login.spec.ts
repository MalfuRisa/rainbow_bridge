import { test, expect } from '@playwright/test'
import { mockApi, login, scopeQuery, CSRF, identity, config, ok, fail } from './mock'

test('D-F01/02 登录错误就地提示，Enter登录回原范围，刷新恢复且无凭据落盘/日志', async ({ page }) => {
  const mock = await mockApi(page)
  const messages: string[] = []
  page.on('console', message => messages.push(message.text()))
  await page.goto('config' + scopeQuery)
  await expect(page.getByRole('heading', { name: '登录工作台', exact: true })).toBeVisible()
  await expect(page.getByLabel('账号', { exact: true })).toHaveValue('admin')
  await expect(page.getByLabel('密码', { exact: true })).toHaveValue('')
  mock.loginStatus = 401
  await page.getByLabel('密码', { exact: true }).fill('mock-wrong-only')
  await page.getByLabel('密码', { exact: true }).press('Enter')
  await expect(page.getByRole('alert')).toHaveText('账号或密码错误')
  expect(mock.count('auth/me')).toBe(1)
  mock.loginStatus = 200
  await login(page)
  await expect(page.getByRole('heading', { name: '配置管理', exact: true })).toBeVisible()
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  if (process.env.RAINBOW_AUTH_MOCK_SCREENSHOT_DIR) {
    await page.screenshot({ path: process.env.RAINBOW_AUTH_MOCK_SCREENSHOT_DIR + '/workspace-account-desktop.png' })
  }
  await expect(page.getByRole('combobox', { name: '环境', exact: true })).toContainText('演示环境')
  await expect(page.getByRole('combobox', { name: '渠道', exact: true })).toContainText('网页渠道')
  await page.reload()
  await expect(page.getByRole('heading', { name: '配置管理', exact: true })).toBeVisible()
  expect(mock.count('auth/me')).toBe(2)
  expect(messages.join(' ')).not.toMatch(/mock-password-only|mock-wrong-only|mock-csrf/)
  const storage = await page.evaluate(() => JSON.stringify({ ...localStorage, ...sessionStorage }))
  expect(storage).not.toMatch(/mock-password|csrf|username|expires_at/)
  expect(page.url()).toContain(scopeQuery)
})

for (const target of ['https://evil.invalid/', '//evil.invalid', 'javascript:alert(1)', '/login', '/unknown', '/\\evil.invalid']) {
  test('返回地址拒绝 ' + target, async ({ page }) => {
    await mockApi(page, true)
    await page.goto('login?redirect=' + encodeURIComponent(target))
    await expect(page.getByRole('heading', { name: '配置管理', exact: true })).toBeVisible()
    expect(new URL(page.url()).pathname).toBe('/rainbow-bridge/config')
  })
}

test('D-F06 恢复503不当作未登录，可显式重试，无初始化管理请求', async ({ page }) => {
  const mock = await mockApi(page, true)
  mock.meStatus = 503
  await page.goto('config' + scopeQuery)
  await expect(page.getByRole('button', { name: '重新确认登录状态', exact: true })).toBeVisible()
  expect(mock.count('config/list')).toBe(0)
  mock.meStatus = 200
  await page.getByRole('button', { name: '重新确认登录状态', exact: true }).click()
  await expect(page.getByRole('heading', { name: '配置管理', exact: true })).toBeVisible()
  expect(mock.count('auth/me')).toBe(2)
})

test('D-F06 Retry-After 限流不重复登录，不泄露服务错误', async ({ page }) => {
  const mock = await mockApi(page)
  mock.loginStatus = 429
  await page.goto('login')
  await login(page)
  await expect(page.getByRole('alert')).toHaveText('请求过于频繁，请 1 秒后重试')
  await expect(page.getByRole('button', { name: '请稍后重试', exact: true })).toBeDisabled()
  expect(mock.count('auth/login')).toBe(1)
  mock.loginStatus = 503
  await page.getByLabel('密码', { exact: true }).fill('mock-password-only')
  await expect(page.getByRole('button', { name: '登录', exact: true })).toBeEnabled()
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await expect(page.getByRole('alert')).toHaveText('服务暂不可用，请重试')
})

test('D-F05 退出失败保留身份，可重试；成功后管理页面重新登录', async ({ page }) => {
  const mock = await mockApi(page, true)
  await page.goto('resources' + scopeQuery)
  await expect(page.getByRole('heading', { name: '资源管理', exact: true })).toBeVisible()
  mock.logoutStatus = 503
  await page.getByRole('button', { name: '退出', exact: true }).click()
  await expect(page.getByRole('button', { name: '重试退出', exact: true })).toBeVisible()
  expect(mock.authenticated).toBe(true)
  mock.logoutStatus = 200
  await page.getByRole('button', { name: '重试退出', exact: true }).click()
  await expect(page.getByRole('heading', { name: '登录工作台', exact: true })).toBeVisible()
  expect(mock.writes('auth/logout').every(request => request.headers()['x-csrf-token'] === CSRF)).toBe(true)
  expect(mock.count('freeze/unlock')).toBe(0)
  await page.goto('resources' + scopeQuery)
  await expect(page.getByRole('heading', { name: '登录工作台', exact: true })).toBeVisible()
})

test('D-F06 多个管理401只提示/跳转一次，不重放业务请求', async ({ page }) => {
  const mock = await mockApi(page, true)
  let release!: () => void
  const gate = new Promise<void>(resolve => { release = resolve })
  let held = 0
  for (const endpoint of ['freeze/status', 'asset/list']) mock.overrides.set(endpoint, async route => {
    held++
    await gate
    await fail(route, 401, 'AUTH_REQUIRED')
  })
  await page.goto('resources' + scopeQuery)
  await expect.poll(() => held).toBe(2)
  release()
  await expect(page.getByRole('heading', { name: '登录工作台', exact: true })).toBeVisible()
  await expect(page.locator('[data-sonner-toast]').filter({ hasText: '请重新登录' })).toHaveCount(1)
  expect(mock.count('asset/list')).toBe(1)
  expect(mock.count('auth/me')).toBe(1)
})

test('D-F06 CSRF/Origin 不退回输密码，刷新me不重放写请求', async ({ page }) => {
  const mock = await mockApi(page, true)
  mock.overrides.set('environment/create', route => fail(route, 403, 'AUTH_CSRF_INVALID'))
  await page.goto('environments')
  await page.getByRole('button', { name: '新增环境', exact: true }).click()
  await page.locator('#env-key').fill('new')
  await page.locator('#env-name').fill('新环境')
  await page.getByRole('button', { name: '保存', exact: true }).click()
  await expect(page.getByRole('dialog').getByText('登录状态校验失败，请刷新登录状态后重试')).toBeVisible()
  await page.getByRole('dialog').getByRole('button', { name: '取消', exact: true }).click()
  await expect(page.getByRole('dialog')).toBeHidden()
  await page.getByRole('button', { name: '刷新登录状态', exact: true }).click()
  await expect.poll(() => mock.count('auth/me')).toBe(2)
  expect(mock.count('environment/create')).toBe(1)
  mock.overrides.set('asset/list', route => fail(route, 403, 'AUTH_ORIGIN_DENIED'))
  await page.getByRole('link', { name: '资源管理', exact: true }).click()
  await page.getByRole('combobox', { name: '环境', exact: true }).click()
  await page.getByRole('option', { name: '演示环境 (qa)', exact: true }).click()
  await page.getByRole('combobox', { name: '渠道', exact: true }).click()
  await page.getByRole('option', { name: '网页渠道 (web)', exact: true }).click()
  await expect(page.getByText('来源配置错误，请联系管理员', { exact: true }).first()).toBeVisible()
  expect(mock.count('auth/login')).toBe(0)
})

test('D-F07 退出后迟到的管理响应不恢复列表；重新登录不出现旧数据', async ({ page }) => {
  const mock = await mockApi(page, true)
  let release!: () => void
  const gate = new Promise<void>(resolve => { release = resolve })
  mock.overrides.set('asset/list', async route => {
    await gate
    await ok(route, { list: [{ file_id: 'old', file_name: 'late-private-file.txt', content_type: 'text/plain', file_size: 1 }], total: 1 })
  })
  await page.goto('resources' + scopeQuery)
  await expect.poll(() => mock.count('asset/list')).toBe(1)
  await page.getByRole('button', { name: '退出', exact: true }).click()
  await expect(page.getByRole('heading', { name: '登录工作台', exact: true })).toBeVisible()
  release()
  mock.overrides.delete('asset/list')
  await login(page)
  await expect(page.getByRole('heading', { name: '配置管理', exact: true })).toBeVisible()
  await page.goto('resources' + scopeQuery)
  await expect(page.getByRole('heading', { name: '资源管理', exact: true })).toBeVisible()
  await expect(page.getByText('late-private-file.txt')).toHaveCount(0)
})

test('D-F07 正在恢复的旧me在退出后返回不能复活身份', async ({ page }) => {
  const mock = await mockApi(page, true)
  mock.overrides.set('asset/list', route => fail(route, 403, 'AUTH_CSRF_INVALID'))
  await page.goto('resources' + scopeQuery)
  await expect(page.getByRole('button', { name: '刷新登录状态', exact: true })).toBeVisible()
  let release!: () => void
  const gate = new Promise<void>(resolve => { release = resolve })
  mock.overrides.set('auth/me', async route => { await gate; await ok(route, identity) })
  await page.getByRole('button', { name: '刷新登录状态', exact: true }).click()
  await expect.poll(() => mock.count('auth/me')).toBe(2)
  await page.getByRole('button', { name: '退出', exact: true }).click()
  await expect(page.getByRole('heading', { name: '登录工作台', exact: true })).toBeVisible()
  release()
  await page.getByRole('link', { name: '返回项目概览', exact: true }).click()
  await expect(page.getByRole('link', { name: '登录', exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: '退出', exact: true })).toHaveCount(0)
})

test('D-F08 匿名首页仅请求公开概览/已生效版本，不读取编辑区与冻结状态', async ({ page }) => {
  const mock = await mockApi(page)
  await page.goto('./')
  await page.getByRole('combobox', { name: '环境', exact: true }).click()
  await page.getByRole('option', { name: '演示环境', exact: true }).click()
  await page.getByRole('combobox', { name: '渠道', exact: true }).click()
  await page.getByRole('option', { name: '网页渠道', exact: true }).click()
  await expect(page.getByText('光谱标题', { exact: true })).toBeVisible()
  expect(mock.count('freeze/status')).toBe(0)
  expect(mock.count('config/list')).toBe(0)
  expect(mock.count('release/list')).toBe(0)
})

for (const status of [401, 503]) {
  test('匿名首页的公开概览不受迟到me ' + status + '清理', async ({ page }) => {
    const mock = await mockApi(page)
    let release!: () => void
    const gate = new Promise<void>(resolve => { release = resolve })
    let delivered = false
    mock.overrides.set('auth/me', async route => {
      await gate
      await fail(route, status, status === 401 ? 'AUTH_REQUIRED' : 'AUTH_UNAVAILABLE')
      delivered = true
    })
    await page.goto('./')
    await page.getByRole('combobox', { name: '环境', exact: true }).click()
    await expect(page.getByRole('option', { name: '演示环境', exact: true })).toBeVisible()
    await page.keyboard.press('Escape')
    release()
    await expect.poll(() => delivered).toBe(true)
    await page.getByRole('combobox', { name: '环境', exact: true }).click()
    await page.getByRole('option', { name: '演示环境', exact: true }).click()
    await page.getByRole('combobox', { name: '渠道', exact: true }).click()
    await page.getByRole('option', { name: '网页渠道', exact: true }).click()
    await expect(page.getByText('光谱标题', { exact: true })).toBeVisible()
    expect(mock.count('freeze/status')).toBe(0)
    expect(mock.count('config/list')).toBe(0)
  })
}
