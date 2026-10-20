import { defineConfig } from '@playwright/test'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
export default defineConfig({
  testDir: './auth',
  testMatch: process.env.RAINBOW_AUTH_REAL === '1' ? '**/real.spec.ts' : '**/*.spec.ts',
  testIgnore: process.env.RAINBOW_AUTH_REAL === '1' ? [] : ['**/real.spec.ts'],
  fullyParallel: false,
  workers: 1,
  retries: 0,
  reporter: 'list',
  outputDir: process.env.RAINBOW_AUTH_RESULTS_DIR || join(tmpdir(), 'rainbow-admin-login-ui-tests'),
  use: {
    baseURL: process.env.RAINBOW_AUTH_UI_URL || 'http://127.0.0.1:3001/rainbow-bridge/',
    viewport: { width: 1440, height: 900 },
    trace: 'off', video: 'off', screenshot: 'off',
  },
  projects: [{ name: 'chromium', use: { browserName: 'chromium' } }],
})
