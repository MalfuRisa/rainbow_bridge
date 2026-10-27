import { test, expect, type Page } from '@playwright/test'
import JSZip from 'jszip'
import { mockApi, ok, fail, fixtureIdentity, scopeQuery } from './mock'

async function invoke(page: Page, baseURL: string, method: 'config' | 'exportStatic') {
  return page.evaluate(async ({ url, method }) => {
    const { runtimeApi } = await import(/* @vite-ignore */ url)
    try {
      const value = await runtimeApi[method]('qa', 'web')
      return { success: true, size: value instanceof Blob ? value.size : undefined }
    } catch (error) {
      const cause = error as { code: number; errorType: string; message: string }
      return { success: false, code: cause.code, kind: cause.errorType, message: cause.message }
    }
  }, { url: new URL('src/lib/api/runtime.ts', baseURL).href, method })
}

test('保留的旧runtime适配器以真实scope pin请求，页面仍使用发布入口', async ({ page, baseURL }) => {
  const mock = await mockApi(page, true)
  const zip = await new JSZip().file('configs.json', '[]').generateAsync({ type: 'nodebuffer' })
  mock.overrides.set('runtime/config', route => ok(route, { configs: [], environment: {} }))
  mock.overrides.set('runtime/static', route => route.fulfill({ contentType: 'application/zip', body: zip }))
  await page.goto('resources' + scopeQuery)
  await expect.poll(() => mock.count('asset/list')).toBe(1)
  expect(mock.count('runtime/config')).toBe(0)
  expect(mock.count('runtime/static')).toBe(0)
  expect((await invoke(page, baseURL!, 'config')).success).toBe(true)
  expect((await invoke(page, baseURL!, 'exportStatic')).size).toBeGreaterThan(0)
  const config = mock.requests.find(request => new URL(request.url()).pathname.endsWith('/runtime/config'))!
  expect(new URL(config.url()).searchParams.get('scope_id')).toBe(fixtureIdentity('qa', 'web'))
  expect(config.headers()['x-environment']).toBe('qa')
  expect(config.headers()['x-pipeline']).toBe('web')
  const download = new URL(mock.requests.find(request => new URL(request.url()).pathname.endsWith('/runtime/static'))!.url())
  expect(Object.fromEntries(download.searchParams)).toEqual({ environment_key: 'qa', pipeline_key: 'web', scope_id: fixtureIdentity('qa', 'web') })
})

for (const method of ['config', 'exportStatic'] as const) test(`${method}迟到结果不能读到同名新scope`, async ({ page, baseURL }) => {
  const mock = await mockApi(page, true)
  const zip = await new JSZip().file('configs.json', '[]').generateAsync({ type: 'nodebuffer' })
  const endpoint = method === 'config' ? 'runtime/config' : 'runtime/static'
  let finish: (() => void) | undefined
  mock.overrides.set(endpoint, async route => {
    await new Promise<void>(resolve => { finish = resolve })
    if (method === 'config') await ok(route, { configs: [], environment: {} })
    else await route.fulfill({ contentType: 'application/zip', body: zip })
  })
  await page.goto('resources' + scopeQuery)
  await expect.poll(() => mock.count('asset/list')).toBe(1)
  const pending = invoke(page, baseURL!, method)
  await expect.poll(() => Boolean(finish)).toBe(true)
  await page.evaluate(async ({ url, environmentId, newScope }) => {
    const { registerScopes } = await import(/* @vite-ignore */ url)
    registerScopes([{ key: 'qa', name: '演示环境', environmentId, pipelines: [{ key: 'web', name: '网页渠道', environmentId, scopeId: newScope }] }])
  }, { url: new URL('src/lib/scope.ts', baseURL!).href, environmentId: fixtureIdentity('qa'), newScope: fixtureIdentity('new-qa-web') })
  finish!()
  expect(await pending).toMatchObject({ success: false, kind: 'SCOPE_IDENTITY_CONFLICT' })
  expect(mock.count(endpoint)).toBe(1)
})

for (const [status, kind] of [[409, 'SCOPE_IDENTITY_CONFLICT'], [410, 'SCOPE_DELETED'], [404, 'API_NOT_FOUND']] as const) {
  test(`旧static ${kind}保留JSON错误，不产生归档或降级请求`, async ({ page, baseURL }) => {
    const mock = await mockApi(page, true)
    mock.overrides.set('runtime/static', route => fail(route, status, kind))
    await page.goto('resources' + scopeQuery)
    await expect.poll(() => mock.count('asset/list')).toBe(1)
    const result = await invoke(page, baseURL!, 'exportStatic')
    expect(result).toMatchObject({ success: false, code: status, kind })
    expect(result.size).toBeUndefined()
    expect(mock.count('runtime/static')).toBe(1)
    expect(mock.count('runtime/config')).toBe(0)
    await expect(page.getByRole('heading', { name: '登录工作台', exact: true })).toHaveCount(0)
  })
}
