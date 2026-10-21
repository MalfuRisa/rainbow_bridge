import { defineConfig } from '@playwright/test'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
export default defineConfig({
  testDir: './local/release', testMatch: '**/release-cleanup*ui.spec.ts',
  workers: 1, fullyParallel: false, retries: 0,
  timeout: 30000, reporter: 'list', outputDir: join(tmpdir(), 'rainbow-cleanup-ui'),
  use: { browserName: 'chromium', viewport: { width: 1440, height: 900 }, trace: 'off', video: 'off', screenshot: 'off' },
})
