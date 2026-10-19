import { expect, test } from '@playwright/test'
import { targetA, url, writeEvidence } from './support/env'
import { PAGE_ROUTES, expectPageTitle, gotoPage } from './support/pages'

test.describe('T02 页面与部署前缀 @t02 @smoke', () => {
  test('六个页面逐一进入、刷新关键页、资源与请求检查 @t02 @smoke', async ({ page }) => {
    const target = targetA()
    const externalRequests: string[] = []
    await page.route('**/*', route => {
      const url = new URL(route.request().url())
      if (!['127.0.0.1', 'localhost', '[::1]'].includes(url.hostname)) {
        externalRequests.push(url.toString()); return route.abort()
      }
      return route.continue()
    })
    const consoleErrors: { page: string; text: string }[] = []
    const failedRequests: { page: string; url: string; failure: string }[] = []
    let currentPage = ''
    page.on('console', (message) => {
      if (message.type() === 'error') consoleErrors.push({ page: currentPage, text: message.text() })
    })
    page.on('requestfailed', (failed) =>
      failedRequests.push({ page: currentPage, url: failed.url(), failure: failed.failure()?.errorText ?? '' }),
    )

    const visits: { path: string; title: string; mounted: boolean }[] = []
    for (const route of PAGE_ROUTES) {
      currentPage = route.path
      await gotoPage(page, target, route.path)
      await expectPageTitle(page, route.title)
      // Vue 应用必须真正挂载(根节点内出现内容)，不能只回一个空壳 HTML
      const appHtmlLength = (await page.locator('#app').innerHTML()).length
      visits.push({ path: route.path, title: route.title, mounted: appHtmlLength > 100 })
      expect(appHtmlLength, `${route.path} 页面似乎未挂载(#app 为空)`).toBeGreaterThan(100)
    }

    // 刷新 /config 与 /import-export(带前缀直接导航，不允许裸 '/')
    for (const path of ['/config', '/import-export']) {
      currentPage = `${path} (refresh)`
      await page.goto(url(target, path), { waitUntil: 'domcontentloaded' })
      await page.reload({ waitUntil: 'domcontentloaded' })
      const appHtmlLength = (await page.locator('#app').innerHTML()).length
      expect(appHtmlLength, `${path} 刷新后未挂载`).toBeGreaterThan(100)
    }

    // 侧边栏导航可用(点击而非直接 goto)
    currentPage = 'sidebar'
    await gotoPage(page, target, '/')
    await page.getByText('配置管理', { exact: true }).first().click()
    await expect(page).toHaveURL(new RegExp(`${target.prefix.replace(/\//g, '\\/')}/config$`))
    await expectPageTitle(page, '配置管理')

    writeEvidence('t02-pages', { visits, consoleErrors, failedRequests, externalRequests })
    expect(externalRequests).toEqual([])
    // 刷新/导航会中断在途请求并触发应用侧 "Failed to fetch" 日志，属浏览器行为，单独记录
    const refreshWindow = (page_: string) => page_.includes('refresh')
    const abortedByNavigation = failedRequests.filter(
      (item) => item.url.startsWith(target.origin) && item.failure.includes('ERR_ABORTED'),
    )
    const sameOriginFailures = failedRequests.filter(
      (item) => item.url.startsWith(target.origin) && !item.failure.includes('ERR_ABORTED'),
    )
    const refreshConsoleNoise = consoleErrors.filter((item) => refreshWindow(item.page) && item.text.includes('Failed to fetch'))
    const realConsoleErrors = consoleErrors.filter((item) => !refreshConsoleNoise.includes(item))
    expect(sameOriginFailures, `同源失败请求: ${JSON.stringify(sameOriginFailures)}`).toEqual([])
    expect(realConsoleErrors, `存在控制台错误: ${JSON.stringify(realConsoleErrors)}`).toEqual([])
    const externalFailures = failedRequests.filter((item) => !item.url.startsWith(target.origin))
    expect.soft(externalFailures, `外部依赖请求失败(内网部署不可达): ${JSON.stringify(externalFailures)}`).toEqual([])
    writeEvidence('t02-request-classification', { abortedByNavigation, sameOriginFailures, externalFailures, refreshConsoleNoise })
  })
})