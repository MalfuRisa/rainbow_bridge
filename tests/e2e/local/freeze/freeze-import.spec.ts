import { expect, test } from '@playwright/test'
import { freezeToken, targetA, writeEvidence } from '../support/env'
import { buildTransferZip } from '../support/archives'
import { listConfigs, scopeDevMain, setupSentinelScopes } from '../support/scopes'
import { lockScopeFromCurrentRevision, scopeStatus, unlockScopeFromCurrentRevision } from './support'

/**
 * F09/F10(页面部分):导入预览展示受锁目标;提交收到 409 时展示实际受阻范围;不产生数据变化。
 */
test.describe('冻结与导入页面 @freeze', () => {
  test('导入预览标记受锁目标,提交被 409 拒绝并展示范围 @freeze', async ({ page }) => {
    test.setTimeout(120_000)
    const target = targetA()
    const token = freezeToken()
    await setupSentinelScopes(target, 'freeze-import')

    const initial = await scopeStatus(target, scopeDevMain)
    if (initial.state === 'locked') {
      await unlockScopeFromCurrentRevision(target, scopeDevMain, token)
    }
    const before = await listConfigs(target, scopeDevMain)
    const beforeCount = before.length

    const archive = await buildTransferZip({
      configs: [
        {
          alias: 'IMPORT_WHILE_LOCKED',
          name: '冻结导入样本',
          environment_key: scopeDevMain.env,
          pipeline_key: scopeDevMain.pipeline,
          type: 'text',
          content: 'x',
        },
      ],
    })

    // 预览前锁定:页面应在预览后展示受锁目标
    await lockScopeFromCurrentRevision(target, scopeDevMain, token)

    await page.goto(`${target.origin}${target.prefix}/import-export`, { waitUntil: 'domcontentloaded' })
    await page.getByRole('tab', { name: '数据导入' }).click()
    await page.locator('#import-file').setInputFiles({ name: 'freeze-import.zip', mimeType: 'application/zip', buffer: archive })
    await expect(page.getByText('选择导入内容')).toBeVisible({ timeout: 15_000 })
    await expect(page.getByText('以下目标已冻结，导入将在写入前被整体拒绝')).toBeVisible()
    await expect(page.getByText(`${scopeDevMain.env}/${scopeDevMain.pipeline}`).first()).toBeVisible()

    // 点击导入:409 提示展示实际目标;选择不丢失(仍显示待导入统计)
    await page.getByRole('button', { name: '开始导入' }).click()
    await expect(page.getByText('导入被拒绝(目标已冻结)')).toBeVisible({ timeout: 15_000 })
    await expect(page.getByText(new RegExp(`scope ${scopeDevMain.env}/${scopeDevMain.pipeline} is frozen`)).first()).toBeVisible()
    await expect(page.getByText('选择导入内容')).toBeVisible()

    // 数据零变化
    const after = await listConfigs(target, scopeDevMain)
    writeEvidence('freeze-import', {
      beforeCount,
      afterCount: after.length,
      blockedConfig: after.some((item) => item.alias === 'IMPORT_WHILE_LOCKED'),
    })
    expect(after.length, '被拒绝的导入不得产生配置').toBe(beforeCount)
    expect(after.some((item) => item.alias === 'IMPORT_WHILE_LOCKED')).toBe(false)

    await unlockScopeFromCurrentRevision(target, scopeDevMain, token)
  })
})