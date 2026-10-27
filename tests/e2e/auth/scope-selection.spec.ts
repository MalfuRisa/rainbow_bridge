import { test, expect, type Page } from '@playwright/test'
import { mkdir } from 'node:fs/promises'
import { join } from 'node:path'
import { mockApi, ok, fail, meta, login } from './mock'

const options = [
  { environment_key: 'qa', environment_name: '测试环境', pipelines: [
    { pipeline_key: 'web', pipeline_name: '网页渠道' },
    { pipeline_key: 'default', pipeline_name: '测试默认渠道' },
  ] },
  { environment_key: 'default', environment_name: '默认环境', pipelines: [
    { pipeline_key: 'web', pipeline_name: '灰度渠道' },
    { pipeline_key: 'default', pipeline_name: '默认渠道' },
  ] },
  { environment_key: 'empty', environment_name: '空环境', pipelines: [] },
]
async function setup(page: Page) {
  const mock = await mockApi(page, true)
  mock.overrides.set('runtime/overview', route => ok(route, { list: options, total: options.length }))
  mock.overrides.set('runtime/release', route => {
    const env = route.request().headers()['x-environment']
    const pipeline = route.request().headers()['x-pipeline']
    return ok(route, { ...meta, environment_key: env, pipeline_key: pipeline, selection_mode: 'active', active_revision: '1',
      environment: { environment_key: env, pipeline_key: pipeline }, configs: [] })
  })
  return mock
}
async function selected(page: Page, env: string, pipeline?: string) {
  await expect(page.getByRole('combobox', { name: '环境', exact: true })).toContainText(env)
  if (pipeline) await expect(page.getByRole('combobox', { name: '渠道', exact: true })).toContainText(pipeline)
}
async function select(page: Page, label: string, option: string) {
  await page.getByRole('combobox', { name: label, exact: true }).click()
  await page.getByRole('option', { name: option, exact: true }).click()
}

for (const [path, endpoint] of [['./', 'runtime/release'], ['config', 'config/list'], ['resources', 'asset/list'], ['releases', 'release/list']]) {
  test(`${path}首次进入自动选择默认范围，仅请求正确范围一次`, async ({ page }) => {
    const mock = await setup(page)
    await page.goto(path)
    await selected(page, '默认环境', '默认渠道')
    await expect.poll(() => mock.count(endpoint)).toBe(1)
    const request = mock.requests.find(request => new URL(request.url()).pathname.endsWith('/' + endpoint))!
    const url = new URL(request.url())
    expect(endpoint === 'runtime/release' ? request.headers()['x-environment'] : url.searchParams.get('environment_key')).toBe('default')
    expect(endpoint === 'runtime/release' ? request.headers()['x-pipeline'] : url.searchParams.get('pipeline_key')).toBe('default')
  })
}

test('切换后四页共享范围，刷新和来回导航保留选择，明确链接优先', async ({ page }) => {
  await setup(page)
  await page.goto('./')
  await selected(page, '默认环境', '默认渠道')
  await select(page, '环境', '测试环境')
  await selected(page, '测试环境', '测试默认渠道')
  await select(page, '渠道', '网页渠道')
  const nav = page.getByRole('navigation', { name: '主要导航' })
  for (const name of ['配置管理', '资源管理', '版本发布', '项目概览', '配置管理']) {
    await nav.getByRole('link', { name, exact: true }).click()
    await selected(page, '测试环境', '网页渠道')
  }
  await page.reload()
  await selected(page, '测试环境', '网页渠道')
  await page.goto('resources?env=default&pipeline=web')
  await selected(page, '默认环境', '灰度渠道')
  await nav.getByRole('link', { name: '项目概览', exact: true }).click()
  await selected(page, '默认环境', '灰度渠道')
})

test('删除记忆中的渠道或环境时安全回退，非法链接不触发非法范围请求', async ({ page }) => {
  const mock = await setup(page)
  await page.goto('config?env=qa&pipeline=web')
  await selected(page, '测试环境', '网页渠道')
  mock.overrides.set('runtime/overview', route => ok(route, { list: [
    { ...options[0], pipelines: [options[0].pipelines[1]] }, options[1],
  ], total: 2 }))
  await page.goto('resources')
  await selected(page, '测试环境', '测试默认渠道')
  mock.overrides.set('runtime/overview', route => ok(route, { list: [options[1]], total: 1 }))
  await page.goto('releases?env=missing&pipeline=missing')
  await selected(page, '默认环境', '默认渠道')
  expect(mock.requests.some(request => request.url().includes('environment_key=missing'))).toBe(false)
})

test('没有默认项时选择首项，空渠道不请求业务，无环境保留空状态', async ({ page }) => {
  const mock = await setup(page)
  mock.overrides.set('runtime/overview', route => ok(route, { list: [{ ...options[0], pipelines: [options[0].pipelines[0]] }], total: 1 }))
  await page.goto('resources')
  await selected(page, '测试环境', '网页渠道')
  await expect.poll(() => mock.count('asset/list')).toBe(1)
  mock.overrides.set('runtime/overview', route => ok(route, { list: [{ ...options[2], pipelines: null }], total: 1 }))
  await page.goto('resources')
  await selected(page, '空环境')
  await expect(page.getByRole('combobox', { name: '渠道', exact: true })).toContainText('请选择渠道')
  expect(mock.count('asset/list')).toBe(1)
  mock.overrides.set('runtime/overview', route => ok(route, { list: [], total: 0 }))
  await page.goto('config')
  await expect(page.getByRole('combobox', { name: '环境', exact: true })).toContainText('请选择环境')
  expect(mock.count('config/list')).toBe(0)
})

test('存储被禁用时导航仍保留选择，主动退出后清除本次记忆', async ({ page }) => {
  await page.addInitScript(() => {
    const setItem = Storage.prototype.setItem
    const getItem = Storage.prototype.getItem
    Storage.prototype.setItem = function(key, value) {
      if (this === sessionStorage) throw new Error('storage unavailable')
      return setItem.call(this, key, value)
    }
    Storage.prototype.getItem = function(key) {
      if (this === sessionStorage) throw new Error('storage unavailable')
      return getItem.call(this, key)
    }
  })
  await setup(page)
  await page.goto('config?env=qa&pipeline=web')
  await selected(page, '测试环境', '网页渠道')
  const nav = page.getByRole('navigation', { name: '主要导航' })
  await nav.getByRole('link', { name: '资源管理', exact: true }).click()
  await selected(page, '测试环境', '网页渠道')
  await page.getByRole('button', { name: '退出', exact: true }).click()
  await expect(page.getByRole('heading', { name: '登录工作台', exact: true })).toBeVisible()
  await login(page)
  await selected(page, '默认环境', '默认渠道')
})

test('概览请求迟到且页面已卸载，不覆盖新页面的范围记忆', async ({ page }) => {
  const mock = await setup(page)
  let release!: () => void
  const gate = new Promise<void>(resolve => { release = resolve })
  let calls = 0
  mock.overrides.set('runtime/overview', async route => {
    if (++calls === 1) await gate
    await ok(route, { list: options, total: options.length })
  })
  await page.goto('./?env=qa&pipeline=web')
  await expect.poll(() => mock.count('runtime/overview')).toBe(1)
  await page.getByRole('navigation').getByRole('link', { name: '配置管理', exact: true }).click()
  await selected(page, '默认环境', '默认渠道')
  release()
  await page.getByRole('navigation').getByRole('link', { name: '资源管理', exact: true }).click()
  await selected(page, '默认环境', '默认渠道')
})

test('环境加载失败后重新加载也能自动选中，桌面范围可读', async ({ page }) => {
  const mock = await setup(page)
  mock.overrides.set('runtime/overview', route => fail(route, 503, 'UNAVAILABLE'))
  await page.goto('config')
  await expect(page.getByRole('button', { name: '重新加载环境', exact: true })).toBeVisible()
  expect(mock.count('config/list')).toBe(0)
  mock.overrides.set('runtime/overview', route => ok(route, { list: options, total: options.length }))
  await page.getByRole('button', { name: '重新加载环境', exact: true }).click()
  await selected(page, '默认环境', '默认渠道')
  for (const width of [1280, 1440]) {
    await page.setViewportSize({ width, height: 900 })
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    if (process.env.RB_QA_VISUAL_DIR) {
      await mkdir(process.env.RB_QA_VISUAL_DIR, { recursive: true })
      await page.screenshot({ path: join(process.env.RB_QA_VISUAL_DIR, `scope-default-${width}.png`), animations: 'disabled' })
    }
  }
})
