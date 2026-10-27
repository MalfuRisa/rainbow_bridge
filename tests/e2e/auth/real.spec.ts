import { test, expect } from '@playwright/test'
import { apiUrl, checked, credentials, management, privateRequest, uiLogin, uiWrite } from './real-helper'
import JSZip from 'jszip'

// No page.route/auth interception, storageState or manually installed login Cookie.
// Real credentials exist only in this Node/Playwright process; trace/video/HAR are off.
test.beforeAll(() => { credentials() })

test('最小真实登录链：UI登录→配置→刷新恢复→退出→旧会话拒绝', async ({ page }) => {
  await uiLogin(page)
  expect(page.url()).toContain('env=default&pipeline=default')
  const me = page.waitForResponse(response => new URL(response.url()).pathname.endsWith('/api/v1/auth/me'))
  await page.reload()
  await checked(await me, 'auth/me')
  await expect(page.getByRole('heading', { name: '配置管理', exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: '退出', exact: true })).toBeVisible()
  const oldCookies = (await page.context().cookies()).map(cookie => cookie.name + '=' + cookie.value).join('; ')
  await uiWrite(page, 'auth/logout', () => page.getByRole('button', { name: '退出', exact: true }).click())
  await expect(page.getByRole('heading', { name: '登录工作台', exact: true })).toBeVisible()
  // Old Cookie is used only for this negative revocation check, never to log into UI.
  const rejected = await privateRequest('logout/revocation-check', () =>
    page.request.get(apiUrl(page, 'config/list?environment_key=default&pipeline_key=default'),
      { headers: { Cookie: oldCookies, Origin: new URL(page.url()).origin } }))
  expect(rejected.status()).toBe(401)
  await page.goto('config?env=default&pipeline=default')
  await expect(page.getByRole('heading', { name: '登录工作台', exact: true })).toBeVisible()
})

test('完整真实发布链：编辑/上传/归档→冻结→创建/切换/切回→匿名首页与资源', async ({ page, browser }) => {
  test.setTimeout(180_000)
  await uiLogin(page)
  const api = await management(page)
  const env = 'qa_ui_' + Date.now().toString(36)
  const label = '登录UI联调 ' + env
  const query = '?env=' + env + '&pipeline=web'
  const observations: { endpoint: string; hasCsrf: boolean; bearer: boolean }[] = []
  page.on('request', request => {
    if (request.method() !== 'POST' || !request.url().includes('/api/v1/')) return
    const endpoint = new URL(request.url()).pathname.split('/api/v1/')[1]
    if (!endpoint.startsWith('auth/')) observations.push({ endpoint,
      hasCsrf: Boolean(request.headers()['x-csrf-token']), bearer: Boolean(request.headers()['authorization']) })
  })
  await api.post('environment/create', { environment: { environment_key: env, environment_name: label, is_active: true } })
  await api.post('pipeline/create', { environment_key: env, pipeline: { pipeline_key: 'web', pipeline_name: '网页渠道联调', is_active: true } })
  await page.goto('config' + query)
  await page.getByRole('button', { name: '新增配置', exact: true }).click()
  await page.locator('#name').fill('真实登录标题')
  await page.locator('#alias').fill('LOGIN_TITLE')
  await page.locator('#content').fill('draft-one')
  await uiWrite(page, 'config/create', () => page.getByRole('dialog').getByRole('button', { name: '保存', exact: true }).click())
  await expect(page.getByRole('dialog')).toBeHidden()

  async function edit(value: string) {
    await page.goto('config' + query)
    await page.getByRole('textbox', { name: '搜索配置名称或别名', exact: true }).fill('LOGIN_TITLE')
    await page.getByRole('button', { name: '编辑配置', exact: true }).click()
    await page.locator('#content').fill(value)
    await uiWrite(page, 'config/update', () => page.getByRole('dialog').getByRole('button', { name: '保存', exact: true }).click())
    await expect(page.getByRole('dialog')).toBeHidden()
  }
  await edit('published-one')
  await page.goto('resources' + query)
  const png = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==', 'base64')
  const upload = await uiWrite(page, 'asset/upload', () => page.locator('input[type=file]').setInputFiles({ name: 'auth-ui.png', mimeType: 'image/png', buffer: png }))
  await api.post('config/create', { config: { environment_key: env, pipeline_key: 'web', alias: 'LOGIN_IMAGE', name: '真实登录图片',
    type: 'image', content: 'asset://' + upload.asset.file_id } })
  await page.goto('import-export')
  await page.getByRole('checkbox', { name: '选择环境 ' + label, exact: true }).check()
  const download = page.waitForEvent('download')
  await page.getByRole('button', { name: '导出数据', exact: true }).click()
  expect((await download).suggestedFilename()).toMatch(/\.zip$/)
  const archive = await api.archive({ format: 'zip', selections: [{ environment_key: env }] })
  const zip = await JSZip.loadAsync(archive)
  expect(Boolean(zip.file('configs.json'))).toBe(true)
  await page.getByRole('tab', { name: '数据导入', exact: true }).click()
  await uiWrite(page, 'transfer/import-preview', () => page.locator('#import-file').setInputFiles({ name: 'auth-ui.zip', mimeType: 'application/zip', buffer: archive }))
  await expect(page.getByRole('heading', { name: '选择导入内容', exact: true })).toBeVisible()

  async function freeze(lock: boolean) {
    await page.goto('config' + query)
    await page.getByRole('button', { name: lock ? '锁版' : '解除冻结', exact: true }).click()
    await expect(page.getByRole('dialog').locator('input[type=password]')).toHaveCount(0)
    await page.locator('#freeze-reason').fill('真实UI发布联调')
    await uiWrite(page, lock ? 'freeze/lock' : 'freeze/unlock', () => page.getByRole('dialog').getByRole('button', { name: lock ? '确认锁定' : '确认解除', exact: true }).click())
    await expect(page.getByRole('dialog')).toBeHidden()
  }
  async function createVersion(version: string) {
    await page.goto('releases' + query)
    await page.getByRole('button', { name: '创建版本', exact: true }).click()
    await expect(page.getByRole('dialog').locator('input[type=password]')).toHaveCount(0)
    await expect(page.locator('#release-version')).toHaveCount(0)
    await page.locator('#release-reason').fill('真实UI快照联调')
    await uiWrite(page, 'release/create', () => page.getByRole('dialog').getByRole('button', { name: '保存版本', exact: true }).click())
    await expect(page.getByRole('dialog')).toBeHidden()
    await expect(page.getByRole('cell', { name: version, exact: true })).toBeVisible()
  }
  async function activate(version: string, back = false) {
    const row = page.getByRole('row').filter({ has: page.getByRole('cell', { name: version, exact: true }) })
    await row.getByRole('button', { name: back ? '切回此版本' : '切换生效版本', exact: true }).click()
    await expect(page.getByRole('dialog').locator('input[type=password]')).toHaveCount(0)
    await page.locator('#review-activation-note').fill('真实UI生效联调')
    await uiWrite(page, 'release/activate', () => page.getByRole('dialog').getByRole('button', { name: `确认生效 ${version}`, exact: true }).click())
    await page.getByRole('dialog').getByRole('button', { name: '完成', exact: true }).click()
    await expect(page.getByRole('dialog')).toBeHidden()
  }
  await freeze(true)
  await expect(page.getByRole('button', { name: '新增配置', exact: true })).toBeDisabled()
  await createVersion('v1')
  await activate('v1')
  await freeze(false)
  await edit('published-two')
  await freeze(true)
  await createVersion('v2')
  await activate('v2')
  await activate('v1', true)
  expect(observations.every(item => item.hasCsrf && !item.bearer)).toBe(true)

  const anonymous = await browser.newContext()
  try {
    const headers = { 'x-environment': env, 'x-pipeline': 'web' }
    for (const [version, value] of [['', 'published-one'], ['v2', 'published-two']]) {
      const data = await checked(await anonymous.request.get(apiUrl(page, 'runtime/release?content_format=raw' + (version ? '&version=' + version : '')), { headers }), 'runtime/release')
      expect(data.configs.find((item: { alias: string }) => item.alias === 'LOGIN_TITLE').content).toBe(value)
      const image = data.configs.find((item: { alias: string }) => item.alias === 'LOGIN_IMAGE').content
      const resource = await anonymous.request.get(new URL(image, page.url()).toString())
      expect(resource.status()).toBe(200)
      expect((await resource.body()).equals(png)).toBe(true)
    }
    const home = await anonymous.newPage()
    await home.goto(process.env.RAINBOW_AUTH_UI_URL || 'http://127.0.0.1:3001/rainbow-bridge/')
    await home.getByRole('combobox', { name: '环境', exact: true }).click()
    await home.getByRole('option', { name: label, exact: true }).click()
    await home.getByRole('combobox', { name: '渠道', exact: true }).click()
    await home.getByRole('option', { name: '网页渠道联调', exact: true }).click()
    await expect(home.getByText('published-one', { exact: true })).toBeVisible()
    await expect(home.getByRole('link', { name: '登录', exact: true })).toBeVisible()
  } finally { await anonymous.close() }
})
