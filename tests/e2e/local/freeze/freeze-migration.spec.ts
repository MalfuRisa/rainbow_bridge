import { expect, test } from '@playwright/test'
import { freezeToken, targetA, writeEvidence } from '../support/env'
import { createConfig, listConfigs, scopeDevMain, scopeProdMain, setupSentinelScopes } from '../support/scopes'
import { expectPageTitle, gotoPage } from '../support/pages'
import { lockScopeFromCurrentRevision, scopeStatus, unlockScopeFromCurrentRevision } from './support'

/**
 * F11(页面部分):目标范围 locked 时,迁移预览入口被阻止并给出原因;源 locked 允许复制。
 */
test.describe('冻结与迁移页面 @freeze', () => {
  test('目标锁定阻止迁移启动;源锁定提示允许只读复制 @freeze', async ({ page }) => {
    test.setTimeout(120_000)
    const target = targetA()
    const token = freezeToken()
    await setupSentinelScopes(target, 'freeze-migration')

    const prodInitial = await scopeStatus(target, scopeProdMain)
    if (prodInitial.state === 'locked') {
      await unlockScopeFromCurrentRevision(target, scopeProdMain, token)
    }
    const devInitial = await scopeStatus(target, scopeDevMain)
    if (devInitial.state === 'locked') {
      await unlockScopeFromCurrentRevision(target, scopeDevMain, token)
    }

    const devRows = await listConfigs(target, scopeDevMain)
    if (!devRows.find((item) => item.alias === 'FREEZE_MIG_SRC')) {
      await createConfig(target, scopeDevMain, {
        alias: 'FREEZE_MIG_SRC',
        name: '迁移来源样本',
        type: 'text',
        content: 'migrate-freeze',
      })
    }

    const prodRowsBefore = await listConfigs(target, scopeProdMain)
    // 目标锁定
    await lockScopeFromCurrentRevision(target, scopeProdMain, token)

    await gotoPage(page, target, '/migration')
    await expectPageTitle(page, '配置迁移')
    const comboboxes = page.getByRole('combobox')
    await comboboxes.nth(0).click()
    await page.getByRole('option', { name: 'QA qa_dev' }).click()
    await comboboxes.nth(1).click()
    await page.getByRole('option', { name: 'QA main' }).click()
    await comboboxes.nth(2).click()
    await page.getByRole('option', { name: 'QA qa_prod' }).click()
    await comboboxes.nth(3).click()
    await page.getByRole('option', { name: 'QA main' }).click()

    await expect(page.getByText('目标范围已冻结,迁移被阻止')).toBeVisible()
    await expect(page.getByText(`${scopeProdMain.env}/${scopeProdMain.pipeline}`, { exact: false }).first()).toBeVisible()
    await expect(page.getByRole('button', { name: '预览变更' })).toBeDisabled()

    // 源锁定(目标解锁):提示允许复制,预览按钮可用
    await unlockScopeFromCurrentRevision(target, scopeProdMain, token)
    await lockScopeFromCurrentRevision(target, scopeDevMain, token)
    // 页面重载后重新选择,触发源/目标状态重新读取
    await page.reload({ waitUntil: 'domcontentloaded' })
    const combos = page.getByRole('combobox')
    await combos.nth(0).click()
    await page.getByRole('option', { name: 'QA qa_dev' }).click()
    await combos.nth(1).click()
    await page.getByRole('option', { name: 'QA main' }).click()
    await combos.nth(2).click()
    await page.getByRole('option', { name: 'QA qa_prod' }).click()
    await combos.nth(3).click()
    await page.getByRole('option', { name: 'QA main' }).click()
    await expect(page.getByText('源范围已冻结')).toBeVisible()
    await expect(page.getByText('仍可复制到未冻结的目标。')).toBeVisible()

    const srcStatus = await scopeStatus(target, scopeDevMain)
    const prodRows = await listConfigs(target, scopeProdMain)
    writeEvidence('freeze-migration', {
      sourceState: srcStatus.state,
      targetUnlocked: (await scopeStatus(target, scopeProdMain)).state,
      prodConfigCount: prodRows.length,
    })
    expect(srcStatus.state).toBe('locked')
    expect(prodRows.length, '仅预览不得产生目标配置').toBe(prodRowsBefore.length)

    await unlockScopeFromCurrentRevision(target, scopeDevMain, token)
  })
})
