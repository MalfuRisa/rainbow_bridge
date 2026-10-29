import { defineConfig } from '@playwright/test'
import { resolve } from 'node:path'

export default defineConfig({
  testDir: '.', testMatch: '*.spec.ts', workers: 1, retries: 0, reporter: 'list',
  outputDir: resolve(__dirname, '../../../.local-qa/request-id-frontend/mock-results'),
  use: { baseURL: process.env.RAINBOW_AUTH_UI_URL || 'http://127.0.0.1:3002/rainbow-bridge/',
    viewport: { width: 1440, height: 900 }, trace: 'off', video: 'off', screenshot: 'off' },
})
