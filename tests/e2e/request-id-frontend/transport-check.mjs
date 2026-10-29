import { chromium } from '@playwright/test'
const url = 'http://10.60.226.43:31880/rainbow-bridge/api/v1/version'
for (const direct of [false, true]) {
  const browser = await chromium.launch({ headless: true, ...(direct ? { proxy: { server: 'direct://' } } : {}) })
  try {
    const page = await browser.newPage()
    const response = await page.goto(url, { waitUntil: 'domcontentloaded', timeout: 15000 })
    process.stdout.write(JSON.stringify({ direct, status: response.status(), secure: await page.evaluate(() => isSecureContext) }) + '\n')
  } catch (error) { process.stdout.write(JSON.stringify({ direct, error: error.name }) + '\n') }
  finally { await browser.close() }
}
