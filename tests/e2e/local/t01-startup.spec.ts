import { expect, test } from '@playwright/test'
import { targetA, writeEvidence } from './support/env'
import { getJson, postJson, request } from './support/net'
import { expectPageTitle, gotoPage } from './support/pages'

test.describe('T01 启动与基础可用性 @t01 @smoke', () => {
  test('首页、ping、version、未知 API 行为与静态资源 @t01 @smoke', async ({ page }) => {
    const target = targetA()
    const consoleErrors: string[] = []
    const failedRequests: { url: string; failure: string }[] = []
    page.on('console', (message) => {
      if (message.type() === 'error') consoleErrors.push(message.text())
    })
    page.on('requestfailed', (failed) =>
      failedRequests.push({ url: failed.url(), failure: failed.failure()?.errorText ?? '' }),
    )

    await gotoPage(page, target, '/')
    await expectPageTitle(page, '项目概览')

    const ping = await getJson(target, '/ping')
    expect(ping.status).toBe(200)
    expect(ping.text).toContain('pong')

    const version = await getJson(target, '/api/v1/version')
    expect(version.status).toBe(200)
    expect(version.json?.code).toBe(200)

    // 未知 API：不能返回伪成功 HTML；状态码与 Content-Type 都要如实记录
    const unknownGet = await request(target, '/api/v1/definitely-unknown-endpoint', { method: 'GET' })
    const unknownHead = await request(target, '/api/v1/definitely-unknown-endpoint', { method: 'HEAD' })
    expect(unknownHead.status).toBe(404)
    expect(unknownHead.bytes.length).toBe(0)
    const unknownPost = await postJson(target, '/api/v1/definitely-unknown-endpoint', {})
    const evidence = {
      ping: ping.text,
      version: version.json,
      unknownGet: { status: unknownGet.status, contentType: unknownGet.contentType, body: unknownGet.text.slice(0, 200) },
      unknownPost: { status: unknownPost.status, contentType: unknownPost.contentType, body: unknownPost.text.slice(0, 200) },
      consoleErrors,
      failedRequests,
    }
    writeEvidence('t01-startup', evidence)

    expect(unknownGet.status, `GET 未知 API 状态: ${unknownGet.text.slice(0, 120)}`).toBe(404)
    expect(unknownPost.status, `POST 未知 API 状态: ${unknownPost.text.slice(0, 120)}`).toBe(404)
    expect(unknownGet.contentType).toContain('application/json')
    expect(unknownPost.contentType).toContain('application/json')
    expect(failedRequests, `页面存在失败请求: ${JSON.stringify(failedRequests)}`).toEqual([])
    expect(consoleErrors, `页面存在控制台错误: ${JSON.stringify(consoleErrors)}`).toEqual([])
  })
})