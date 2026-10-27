import { defineConfig } from '@playwright/test'
export default defineConfig({
 testDir: './concurrency', workers: 1, retries: 0, reporter: 'list',
 outputDir: process.env.RB_CONCURRENCY_RESULTS || '../../.local-qa/concurrency/results',
 use: { baseURL: process.env.RAINBOW_AUTH_UI_URL || 'http://127.0.0.1:19301/rainbow-bridge/', viewport: {width:1440,height:1000}, trace:'off',video:'off',screenshot:'off' },
 projects: [{name:'chromium',use:{browserName:'chromium'}}],
})
