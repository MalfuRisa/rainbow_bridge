import { defineConfig } from '@playwright/test'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
const baseURL = process.env.RAINBOW_AUTH_UI_URL
if (!baseURL || !/^http:\/\/127\.0\.0\.1:18(?:09\d|1[01]\d|120)\/rainbow-bridge\/$/.test(baseURL)) throw new Error('Backup real test requires an explicitly isolated local test port')
export default defineConfig({
  testDir: './backup', testMatch: 'real.spec.ts', workers: 1, retries: 0, timeout: 120000, reporter: 'list',
  outputDir: join(tmpdir(), 'rainbow-backup-real-ui-tests'),
  use: { baseURL, viewport: { width: 1440, height: 900 }, trace: 'off', video: 'off', screenshot: 'off', acceptDownloads: true,
    launchOptions: { downloadsPath: join(process.env.RB_BACKUP_UI_INSTANCE!, 'browser-downloads') } },
})
