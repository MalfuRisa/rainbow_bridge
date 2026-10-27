import { defineConfig } from '@playwright/test'
import { join } from 'node:path'
const baseURL = process.env.RAINBOW_AUTH_UI_URL
const instance = process.env.RB_SCOPE_UI_INSTANCE
if (!baseURL || !/^http:\/\/127\.0\.0\.1:1810[0-9]\/rainbow-bridge\/$/.test(baseURL) || !instance) throw new Error('Requires an isolated scope UI test instance and explicit URL')
export default defineConfig({
  testDir: './scope-transfer', testMatch: ['real.spec.ts', 'release-review.spec.ts', 'release-active-state.spec.ts'], workers: 1, retries: 0, timeout: 120000, reporter: 'list',
  outputDir: join(instance, 'test-results'),
  use: { baseURL, viewport: { width: 1440, height: 1000 }, trace: 'off', screenshot: 'off', video: 'off', acceptDownloads: true,
    launchOptions: { downloadsPath: join(instance, 'browser-downloads') } },
})
