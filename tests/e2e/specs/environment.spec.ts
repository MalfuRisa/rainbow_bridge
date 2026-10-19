import { test, expect } from '@playwright/test'

/**
 * 环境管理用例(基于真实 UI:列表为 div 行、按钮文案为中文、删除无二次确认)。
 * 注意:此文件属于旧的默认 e2e 配置(webServer 启动旧二进制)，
 * 本机隔离套件在 tests/e2e/local 下运行，不依赖本文件。
 */
test.describe('Environment Management', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/')
    await page.getByText('环境管理', { exact: true }).first().click()
    await page.waitForURL('**/environments')
  })

  test('should display environments list', async ({ page }) => {
    await expect(page.locator('h1')).toContainText('环境管理')
    await expect(page.getByRole('button', { name: '新增环境' })).toBeVisible()
    // 列表卡标题
    await expect(page.getByText('环境列表')).toBeVisible()
  })

  test('should create new environment', async ({ page }) => {
    const timestamp = Date.now()
    const envKey = `test-env-${timestamp}`
    const envName = `Test Environment ${timestamp}`

    await page.getByRole('button', { name: '新增环境' }).click()
    await page.locator('#env-key').fill(envKey)
    await page.locator('#env-name').fill(envName)
    await page.getByRole('button', { name: '保存' }).click()

    await expect(page.locator('[data-sonner-toast]').filter({ hasText: '环境创建成功' }).first()).toBeVisible({
      timeout: 10_000,
    })
    await expect(page.getByText(envName, { exact: true })).toBeVisible()
  })

  test('edit affordance is absent by design', async ({ page }) => {
    // 环境行没有任何编辑入口(只有删除)。这里显式断言“不存在”，
    // 替代过去“找不到按钮就静默跳过”的零断言写法。
    const envKey = `no-edit-${Date.now()}`
    await page.getByRole('button', { name: '新增环境' }).click()
    await page.locator('#env-key').fill(envKey)
    await page.locator('#env-name').fill(`No Edit ${envKey}`)
    await page.getByRole('button', { name: '保存' }).click()
    await expect(page.getByText(`No Edit ${envKey}`, { exact: true })).toBeVisible({ timeout: 10_000 })

    const row = page.getByText(`No Edit ${envKey}`, { exact: true }).locator('xpath=ancestor::div[.//button][1]')
    await expect(row.locator('button')).toHaveCount(1)
    await expect(row.locator('button .lucide-trash-2')).toHaveCount(1)
    await expect(row.locator('button .lucide-pencil')).toHaveCount(0)
  })

  test('should delete environment', async ({ page }) => {
    const timestamp = Date.now()
    const envKey = `delete-test-${timestamp}`
    const envName = `To Delete ${timestamp}`

    await page.getByRole('button', { name: '新增环境' }).click()
    await page.locator('#env-key').fill(envKey)
    await page.locator('#env-name').fill(envName)
    await page.getByRole('button', { name: '保存' }).click()
    await expect(page.getByText(envName, { exact: true })).toBeVisible({ timeout: 10_000 })

    const row = page.getByText(envName, { exact: true }).locator('xpath=ancestor::div[.//button][1]')
    await row.locator('button .lucide-trash-2').first().click()

    await expect(page.getByText(envName, { exact: true })).not.toBeVisible({ timeout: 10_000 })
  })
})