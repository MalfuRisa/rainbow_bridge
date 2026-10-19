import { defineConfig, devices } from '@playwright/test'

/**
 * 本机隔离测试专用配置(RB-QA-01)。
 * 服务完全由 script/test-local.ps1 管理：本配置不启动、不复用任何 webServer，
 * 运行地址、证据目录全部来自脚本注入的 RB_QA_* 环境变量。
 */
const artifactsDir = process.env.RB_QA_ARTIFACTS_DIR ?? 'local-artifacts'
const outputDir = process.env.RB_QA_TEST_RESULTS_DIR ?? `${artifactsDir}/test-results`
const jsonReport = process.env.RB_QA_PW_JSON ?? `${artifactsDir}/pw.json`

export default defineConfig({
  testDir: './local',
  /* 仅 Chromium、单线程、不重试：避免数据互相覆盖与重试隐藏首轮失败。 */
  fullyParallel: false,
  workers: 1,
  retries: 0,
  forbidOnly: false,
  timeout: 60_000,
  expect: { timeout: 10_000 },
  reporter: [
    ['list'],
    ['json', { outputFile: jsonReport }],
  ],
  outputDir,
  use: {
    browserName: 'chromium',
    /* 第一次失败即保留现场 */
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    video: 'off',
    /* 不设置全局 baseURL：所有导航必须显式包含本轮前缀(baseURL 只能由用例自身拼接)。 */
  },
  projects: [
    {
      name: 'chromium',
      use: {
        ...devices['Desktop Chrome'],
        channel: process.env.RB_QA_CHANNEL || undefined,
      },
    },
  ],
})