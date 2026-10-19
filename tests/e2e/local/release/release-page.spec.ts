import { expect, test } from '@playwright/test'
import { freezeToken, targetA, writeEvidence } from '../support/env'
import { createConfig, deleteConfig, ensureScope, listConfigs, scopeDevBeta, scopeDevMain } from '../support/scopes'
import { expectPageTitle, gotoPage, selectEnvPipelineOnConfigPage } from '../support/pages'
import { lockScopeFromCurrentRevision, scopeStatus, unlockScopeFromCurrentRevision } from '../freeze/support'
import { postJson, request } from '../support/net'

/**
 * R21/R22/R23:版本页完整流程(创建不自动生效、首次生效、切换、切回)、
 * 两类读取地址、范围切换不串状态、令牌不出现在页面内容中。
 */
test.describe('版本发布页面流程 @release', () => {
  test('创建/查看/首次生效/切新版本/切回旧版与接入说明 @release', async ({ page }) => {
    test.setTimeout(240_000)
    const target = targetA()
    const token = freezeToken()
    await ensureScope(target, scopeDevMain)
    await ensureScope(target, scopeDevBeta)

    // 前置:编辑区锁定,准备两种内容(用于验证切换后内容不同)
    const initial = await scopeStatus(target, scopeDevMain)
    if (initial.state === 'locked') {
      await unlockScopeFromCurrentRevision(target, scopeDevMain, token)
    }
    const rows = await listConfigs(target, scopeDevMain)
    if (!rows.find((item) => item.alias === 'REL_UI_TEXT')) {
      await createConfig(target, scopeDevMain, { alias: 'REL_UI_TEXT', name: '版本页样本', type: 'text', content: 'ui-v1' })
    }
    await lockScopeFromCurrentRevision(target, scopeDevMain, token)

    // 打开版本页并选择范围
    await page.goto(`${target.origin}${target.prefix}/releases`, { waitUntil: 'domcontentloaded' })
    await expectPageTitle(page, '版本发布')
    await selectEnvPipelineOnConfigPage(page, 'QA qa_dev (qa_dev)', 'QA main (main)')

    // 双状态条:编辑区已冻结 + 生效版本未设置,且两类地址文案存在
    await expect(page.getByText('已冻结', { exact: true }).first()).toBeVisible()
    await expect(page.getByText('未设置', { exact: true }).first()).toBeVisible()
    await expect(page.getByText('跟随生效版本(推荐,配合切换/切回)')).toBeVisible()
    await expect(page.getByText('固定某版本(不跟随切换)')).toBeVisible()
    await expect(page.getByText(/旧接口/).first()).toBeVisible()
    // 页面不提供版本修改/删除按钮
    await expect(page.getByRole('button', { name: /删除版本|修改版本|覆盖版本/ })).toHaveCount(0)

    // 创建 v1.0:必须提示“尚未切换生效”
    await page.getByRole('button', { name: '创建版本' }).click()
    const createDialog = page.getByRole('dialog').filter({ has: page.locator('#release-version') })
    await expect(createDialog.getByText(/来源 revision/)).toBeVisible()
    await createDialog.locator('#release-version').fill('v1.0')
    await createDialog.locator('#release-reason').fill('浏览器创建 v1.0')
    await createDialog.locator('#release-token').fill(token)
    await createDialog.getByRole('button', { name: '保存版本' }).click()
    await expect(page.locator('[data-sonner-toast]').filter({ hasText: '版本已保存,尚未切换生效' }).first()).toBeVisible()
    await expect(page.getByText('v1.0', { exact: true }).first()).toBeVisible()
    // 创建后仍未生效
    await expect(page.getByText('未设置', { exact: true }).first()).toBeVisible()

    // 查看详情:只读,包含来源冻结 revision 与资源清单区域
    await page.getByRole('button', { name: '查看' }).first().click()
    const detailDialog = page.getByRole('dialog').filter({ hasText: '只读快照' })
    await expect(detailDialog.getByText('来源冻结 revision:')).toBeVisible()
    await expect(detailDialog.getByText('资源清单(')).toBeVisible()
    await page.keyboard.press('Escape')

    // 首次生效
    await page.getByRole('button', { name: '切换生效版本' }).first().click()
    const activateDialog = page.getByRole('dialog').filter({ has: page.locator('#activate-token') })
    await expect(activateDialog.getByText(/未设置 → v1\.0/)).toBeVisible()
    await activateDialog.locator('#activate-reason').fill('首次生效')
    await activateDialog.locator('#activate-token').fill(token)
    await activateDialog.getByRole('button', { name: '切换生效版本' }).click()
    await expect(page.locator('[data-sonner-toast]').filter({ hasText: '已切换生效版本:v1.0' }).first()).toBeVisible()
    await expect(page.getByText('生效中').first()).toBeVisible()

    // 新接口读取跟随生效版本
    const activeRuntime = await request(target, `/api/v1/runtime/release`, {
      method: 'GET', headers: { 'x-environment': scopeDevMain.env, 'x-pipeline': scopeDevMain.pipeline },
    })
    expect(activeRuntime.status).toBe(200)
    const activeJSON = JSON.parse(activeRuntime.text)
    expect(activeJSON.data.selection_mode).toBe('active')
    expect(activeJSON.data.configs.some((c: any) => c.alias === 'REL_UI_TEXT' && c.content === 'ui-v1')).toBe(true)

    // 修改编辑区(解锁),创建 v1.1,切换,再切回
    const switchPromise = (async () => {
      await unlockScopeFromCurrentRevision(target, scopeDevMain, token)
      const current = await listConfigs(target, scopeDevMain)
      const row = current.find((item) => item.alias === 'REL_UI_TEXT')
      if (row) {
        await postJson(target, '/api/v1/config/update', { config: { resource_key: row.resource_key, environment_key: scopeDevMain.env, pipeline_key: scopeDevMain.pipeline, alias: row.alias, name: row.name, type: row.type, content: 'ui-v2' } })
      }
      await lockScopeFromCurrentRevision(target, scopeDevMain, token)
    })()
    await switchPromise
    await page.getByRole('button', { name: '刷新' }).click()
    await expect(page.getByText('已冻结', { exact: true }).first()).toBeVisible()

    await page.getByRole('button', { name: '创建版本' }).click()
    const createDialog2 = page.getByRole('dialog').filter({ has: page.locator('#release-version') })
    await createDialog2.locator('#release-version').fill('v1.1')
    await createDialog2.locator('#release-reason').fill('浏览器创建 v1.1')
    await createDialog2.locator('#release-token').fill(token)
    await createDialog2.getByRole('button', { name: '保存版本' }).click()
    await expect(page.locator('[data-sonner-toast]').filter({ hasText: '版本已保存,尚未切换生效' }).first()).toBeVisible()

    // 切换 v1.1(此时按钮文案为“切换生效版本”)
    const v11Row = page.locator('tr').filter({ hasText: 'v1.1' }).first()
    await v11Row.getByRole('button', { name: /切换生效版本|切回此版本/ }).click()
    const activateDialog2 = page.getByRole('dialog').filter({ has: page.locator('#activate-token') })
    await expect(activateDialog2.getByText(/v1\.0 → v1\.1/)).toBeVisible()
    await activateDialog2.locator('#activate-reason').fill('切到 v1.1')
    await activateDialog2.locator('#activate-token').fill(token)
    await activateDialog2.getByRole('button', { name: '切换生效版本' }).click()
    await expect(page.locator('[data-sonner-toast]').filter({ hasText: '已切换生效版本:v1.1' }).first()).toBeVisible()

    // 切回 v1.0(按钮文案“切回此版本”)
    const v10Row = page.locator('tr').filter({ hasText: 'v1.0' }).first()
    await v10Row.getByRole('button', { name: /切换生效版本|切回此版本/ }).click()
    const activateDialog3 = page.getByRole('dialog').filter({ has: page.locator('#activate-token') })
    await expect(activateDialog3.getByText(/v1\.1 → v1\.0/)).toBeVisible()
    await activateDialog3.locator('#activate-reason').fill('切回 v1.0')
    await activateDialog3.locator('#activate-token').fill(token)
    await activateDialog3.getByRole('button', { name: '切回此版本' }).click()
    await expect(page.locator('[data-sonner-toast]').filter({ hasText: '已切换生效版本:v1.0' }).first()).toBeVisible()

    // 切回后 active 与固定版本读取分别正确
    const runtimeAfterBack = JSON.parse((await request(target, `/api/v1/runtime/release`, {
      method: 'GET', headers: { 'x-environment': scopeDevMain.env, 'x-pipeline': scopeDevMain.pipeline },
    })).text)
    const fixedV11 = JSON.parse((await request(target, `/api/v1/runtime/release?version=v1.1`, {
      method: 'GET', headers: { 'x-environment': scopeDevMain.env, 'x-pipeline': scopeDevMain.pipeline },
    })).text)
    const oldRuntime = JSON.parse((await request(target, `/api/v1/runtime/config`, {
      method: 'GET', headers: { 'x-environment': scopeDevMain.env, 'x-pipeline': scopeDevMain.pipeline },
    })).text)

    // 范围切换不串状态:切到另一渠道后列表清空(无版本)
    await page.getByRole('combobox').nth(1).click()
    await page.getByRole('option', { name: 'QA beta' }).click()
    await expect(page.getByText('暂无版本:先锁版,再点击“创建版本”')).toBeVisible()
    await page.getByRole('combobox').nth(1).click()
    await page.getByRole('option', { name: 'QA main' }).click()
    await expect(page.getByText('v1.0', { exact: true }).first()).toBeVisible()

    // 令牌不进入页面内容
    expect((await page.content()).includes(token), '管理令牌不得出现在页面内容中').toBe(false)

    writeEvidence('release-page', {
      activeAfterBack: runtimeAfterBack.data?.version,
      fixedV11Version: fixedV11.data?.version,
      activeMode: runtimeAfterBack.data?.selection_mode,
      fixedMode: fixedV11.data?.selection_mode,
      oldRuntimeReadsEditor: oldRuntime.data?.configs?.some((c: any) => c.alias === 'REL_UI_TEXT' && c.content === 'ui-v2'),
    })
    expect(runtimeAfterBack.data.version).toBe('v1.0')
    expect(fixedV11.data.selection_mode).toBe('fixed')
    expect(fixedV11.data.version).toBe('v1.1')
    expect(oldRuntime.data?.configs?.some((c: any) => c.alias === 'REL_UI_TEXT' && c.content === 'ui-v2'), '旧接口仍读编辑区').toBe(true)

    // 清理:解锁并删除样本配置(保留版本,版本不可删由设计决定)
    await unlockScopeFromCurrentRevision(target, scopeDevMain, token)
    const finalRows = await listConfigs(target, scopeDevMain)
    for (const row of finalRows) {
      if (row.alias === 'REL_UI_TEXT') {
        await deleteConfig(target, scopeDevMain, row.resource_key)
      }
    }
  })
})