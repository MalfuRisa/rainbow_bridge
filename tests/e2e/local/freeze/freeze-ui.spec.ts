import { expect, test } from '@playwright/test'
import { freezeToken, targetA, writeEvidence } from '../support/env'
import { createConfig, deleteConfig, listConfigs, scopeDevMain } from '../support/scopes'
import { expectConfigRow, gotoPage, selectEnvPipelineOnConfigPage } from '../support/pages'
import { lockScopeFromCurrentRevision, scopeStatus, unlockScopeFromCurrentRevision } from './support'
import { configsByAlias } from '../support/db'

/**
 * 冻结专项 F02/F03/F07/F23(页面部分):
 * 状态展示 → 锁版弹窗(目标范围确认/原因/令牌/修订号)→ 写入口禁用 → 历史记录 → 解除冻结。
 */
test.describe('冻结控制页面交互 @freeze', () => {
  test('锁版/解除/历史与写入口禁用 @freeze', async ({ page }) => {
    test.setTimeout(120_000)
    const target = targetA()
    const token = freezeToken()

    // 确保范围干净:未解锁时先解锁
    const initial = await scopeStatus(target, scopeDevMain)
    if (initial.state === 'locked') {
      await unlockScopeFromCurrentRevision(target, scopeDevMain, token)
    }

    // 前置配置(用于校验锁定期间的禁用)
    const existingRows = await listConfigs(target, scopeDevMain)
    if (!existingRows.find((item) => item.alias === 'FREEZE_UI_CFG')) {
      await createConfig(target, scopeDevMain, {
        alias: 'FREEZE_UI_CFG',
        name: '冻结交互样本',
        type: 'text',
        content: 'v1',
      })
    }

    await gotoPage(page, target, '/config')
    await selectEnvPipelineOnConfigPage(page, 'QA qa_dev (qa_dev)', 'QA main (main)')

    // 初始:可编辑
    const panel = page.locator('div').filter({ hasText: '未选择范围' }).first()
    await expect(page.getByText('可编辑', { exact: true })).toBeVisible()
    void panel

    // 打开锁版弹窗:必须显示目标范围与 revision
    await expect(page.getByRole('button', { name: '锁版' })).toBeEnabled()
    await page.getByRole('button', { name: '锁版' }).click()
    const dialog = page.getByRole('dialog').filter({ has: page.locator('#freeze-reason') })
    await expect(dialog.getByText('QA qa_dev(qa_dev) / QA main(main)')).toBeVisible()
    await expect(dialog.getByText('目标范围', { exact: true })).toBeVisible()

    await dialog.locator('#freeze-reason').fill('QA 上线前验收完成')
    await dialog.getByRole('button', { name: '确认锁定' }).click()
    await expect(page.locator('[data-sonner-toast]').filter({ hasText: '已锁定该范围' }).first()).toBeVisible()
    await expect(page.getByText('已冻结', { exact: true }).first()).toBeVisible()

    // 写入口禁用:新增配置按钮、编辑铅笔、删除按钮
    await expect(page.getByRole('button', { name: '新增配置' })).toBeDisabled()
    const row = page.locator('tr').filter({ hasText: 'FREEZE_UI_CFG' }).first()
    await expect(row.locator('button .lucide-pencil').first()).toBeDisabled()
    await expect(row.locator('button .lucide-trash-2').first()).toBeDisabled()

    // 操作记录:包含 lock 事件
    await page.getByRole('button', { name: '操作记录' }).click()
    const history = page.getByRole('dialog').filter({ hasText: '冻结操作记录' })
    await expect(history.getByText('锁定配置', { exact: true }).first()).toBeVisible()
    await expect(history.getByText(/QA 上线前验收完成/).first()).toBeVisible()
    await page.keyboard.press('Escape')
    await expect(history).not.toBeVisible()

    // 解除冻结:弹窗显示最新 revision,成功后恢复可编辑
    await page.getByRole('button', { name: '解除冻结' }).click()
    const unlockDialog = page.getByRole('dialog').filter({ has: page.locator('#freeze-reason') })
    await unlockDialog.locator('#freeze-reason').fill('QA 验收调整')
    await unlockDialog.getByRole('button', { name: '确认解除' }).click()
    await expect(page.locator('[data-sonner-toast]').filter({ hasText: '已解除冻结' }).first()).toBeVisible()
    await expect(page.getByText('可编辑', { exact: true })).toBeVisible()
    await expect(page.getByRole('button', { name: '新增配置' })).toBeEnabled()

    const statusAfter = await scopeStatus(target, scopeDevMain)
    const rowAfter = configsByAlias(target, scopeDevMain.env, scopeDevMain.pipeline, 'FREEZE_UI_CFG')
    writeEvidence('freeze-ui', {
      tokenLeakedToDom: (await page.content()).includes(token),
      statusAfter,
      configRows: rowAfter.length,
    })
    expect(statusAfter.state).toBe('unlocked')
    expect(rowAfter.length, '锁定期间配置数量不得变化').toBe(1)
    // 令牌绝不进入页面 DOM
    expect((await page.content()).includes(token), '管理令牌不得出现在页面内容中').toBe(false)

    // 清理
    for (const row_ of rowAfter) {
      await deleteConfig(target, scopeDevMain, String(row_.resource_key))
    }
  })
})
