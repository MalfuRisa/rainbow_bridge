import { expect, test } from '@playwright/test'
import { freezeToken, targetA, writeEvidence } from '../support/env'
import { listConfigs, scopeDevMain, setupSentinelScopes } from '../support/scopes'
import { gotoPage, selectEnvPipelineOnConfigPage } from '../support/pages'
import { lockScopeFromCurrentRevision, scopeStatus, unlockScopeFromCurrentRevision } from './support'

/**
 * F22:页面编辑中另一个上下文锁定 → 保存收到 409 时保留草稿、刷新状态、不显示成功。
 */
test.describe('冻结与编辑草稿 @freeze', () => {
  test('外部锁定后保存:提示 409、保留输入、状态刷新、不显示成功 @freeze', async ({ page }) => {
    test.setTimeout(120_000)
    const target = targetA()
    const token = freezeToken()
    await setupSentinelScopes(target, 'freeze-draft')

    const initial = await scopeStatus(target, scopeDevMain)
    if (initial.state === 'locked') {
      await unlockScopeFromCurrentRevision(target, scopeDevMain, token)
    }

    await gotoPage(page, target, '/config')
    await selectEnvPipelineOnConfigPage(page, 'QA qa_dev (qa_dev)', 'QA main (main)')
    await expect(page.getByText('可编辑', { exact: true })).toBeVisible()

    // 打开新建弹窗并填写草稿(此时页面认为可编辑)
    await page.getByRole('button', { name: '新增配置' }).click()
    const dialog = page.getByRole('dialog').filter({ has: page.locator('#alias') })
    await dialog.locator('#name').fill('外部锁定草稿样本')
    await dialog.locator('#alias').fill('FREEZE_DRAFT_CFG')
    await dialog.locator('#content').fill('draft-content')

    // 另一个上下文(API + 管理令牌)锁定该范围
    await lockScopeFromCurrentRevision(target, scopeDevMain, token)

    // 保存:必须显示冻结错误、保留输入、不出现“配置创建成功”
    await dialog.getByRole('button', { name: '保存' }).click()
    await expect(dialog.getByText(/已冻结|frozen/).first()).toBeVisible({ timeout: 15_000 })
    await expect(dialog.locator('#name')).toHaveValue('外部锁定草稿样本')
    await expect(dialog.locator('#alias')).toHaveValue('FREEZE_DRAFT_CFG')
    await expect(dialog.locator('#content')).toHaveValue('draft-content')
    await expect(page.locator('[data-sonner-toast]').filter({ hasText: '配置创建成功' })).toHaveCount(0)

    // 页面状态已刷新为已冻结
    await expect(page.getByText('已冻结', { exact: true }).first()).toBeVisible()
    const statusAfter = await scopeStatus(target, scopeDevMain)

    // 取消弹窗(草稿丢弃由用户决定),服务端无该配置
    await dialog.getByRole('button', { name: '取消' }).click()
    const rows = await listConfigs(target, scopeDevMain)
    writeEvidence('freeze-draft', {
      statusAfter,
      draftPersisted: rows.some((item) => item.alias === 'FREEZE_DRAFT_CFG'),
      dialogErrorShown: true,
    })
    expect(rows.some((item) => item.alias === 'FREEZE_DRAFT_CFG'), '被拒绝的草稿不得落库').toBe(false)

    // 清理:解锁
    await unlockScopeFromCurrentRevision(target, scopeDevMain, token)
  })
})