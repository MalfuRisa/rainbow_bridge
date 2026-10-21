import { randomUUID } from 'node:crypto'
import { expect, test } from '@playwright/test'
import { freezeToken, targetA, writeEvidence } from '../support/env'
import {
  createConfig,
  deleteConfig,
  listConfigs,
  resolveUrl,
  scopeDevMain,
  scopeProdMain,
  setupSentinelScopes,
  TINY_PNG,
  updateConfig,
  uploadAsset,
} from '../support/scopes'
import { expectPageTitle, gotoPage, selectEnvPipelineOnConfigPage } from '../support/pages'
import { postJson, sha256Hex } from '../support/net'
import { lockScopeFromCurrentRevision, scopeStatus, unlockScopeFromCurrentRevision } from './support'

/** 按 alias 幂等写入源配置(重复运行不产生重复项)。 */
async function upsertSourceConfig(
  target: ReturnType<typeof targetA>,
  payload: { alias: string; name: string; type: string; content: string; description: string },
): Promise<void> {
  const existing = (await listConfigs(target, scopeDevMain)).find((item) => item.alias === payload.alias)
  if (existing) {
    await updateConfig(target, scopeDevMain, { ...payload, resource_key: existing.resource_key })
    return
  }
  await createConfig(target, scopeDevMain, payload)
}

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

  /**
   * RB-MIGRATE-01(页面链路):真实上传图片 → 页面迁移 → 目标引用换新且字节一致 →
   * 页面锁版成功 → 创建版本不再报 cross_scope_reference;缺资源项由页面给出部分失败反馈。
   */
  test('图片资源迁移后引用换新,页面锁版成功 @freeze', async ({ page, request }) => {
    test.setTimeout(240_000)
    const target = targetA()
    const token = freezeToken()
    await setupSentinelScopes(target, 'freeze-migration-image')

    if ((await scopeStatus(target, scopeProdMain)).state === 'locked') {
      await unlockScopeFromCurrentRevision(target, scopeProdMain, token)
    }

    // 源:真实上传图片资源并保存图片配置;另加一条缺资源配置用于失败反馈
    const upload = await uploadAsset(target, scopeDevMain, {
      fileName: 'mig-pic.png',
      contentType: 'image/png',
      bytes: TINY_PNG,
    })
    await upsertSourceConfig(target, {
      alias: 'MIG_IMAGE',
      name: '迁移图片',
      type: 'image',
      content: upload.reference,
      description: 'qa image migration',
    })
    await upsertSourceConfig(target, {
      alias: 'MIG_BROKEN',
      name: '迁移缺资源',
      type: 'image',
      content: 'asset://missing-page-asset-1',
      description: 'qa broken reference',
    })

    // 目标保持干净:重复运行时清理上次结果,避免走冲突勾选分支
    for (const alias of ['MIG_IMAGE', 'MIG_BROKEN']) {
      const stale = (await listConfigs(target, scopeProdMain)).find((item) => item.alias === alias)
      if (stale) await deleteConfig(target, scopeProdMain, stale.resource_key)
    }

    // 页面迁移:qa_dev/main → qa_prod/main
    await gotoPage(page, target, '/migration')
    await expectPageTitle(page, '配置迁移')
    const combos = page.getByRole('combobox')
    await combos.nth(0).click()
    await page.getByRole('option', { name: 'QA qa_dev' }).click()
    await combos.nth(1).click()
    await page.getByRole('option', { name: 'QA main' }).click()
    await combos.nth(2).click()
    await page.getByRole('option', { name: 'QA qa_prod' }).click()
    await combos.nth(3).click()
    await page.getByRole('option', { name: 'QA main' }).click()

    try {
      await page.getByRole('button', { name: '预览变更' }).click()
      await expect(page.getByText('MIG_IMAGE').first()).toBeVisible()
      const migrateButton = page.getByRole('button', { name: /^迁移 \d+ 个配置$/ })
      await expect(migrateButton).toBeEnabled()
      await migrateButton.click()

      // 页面失败反馈:部分失败提示与结果面板(逐项消息在接口层核对)
      await expect(page.locator('[data-sonner-toast]').filter({ hasText: '部分配置迁移失败' }).first()).toBeVisible()
      await expect(page.getByText('迁移未完成，请核对结果')).toBeVisible()

      // 接口层核对逐项结果:失败项定位到 alias/file_id/类别,成功项引用换新
      const prodRows = await listConfigs(target, scopeProdMain)
      const migrated = prodRows.find((item) => item.alias === 'MIG_IMAGE')
      expect(migrated, '目标必须存在迁移结果').toBeTruthy()
      expect(prodRows.find((item) => item.alias === 'MIG_BROKEN'), '缺资源项不得写入目标').toBeFalsy()
      const match = /\/api\/v1\/asset\/file\/([^/?#]+)\/([^/?#]+)/.exec(migrated?.content ?? '')
      expect(match, `目标内容应为受管 URL: ${migrated?.content}`).toBeTruthy()
      expect(match?.[1], '目标引用必须换成新资源').not.toBe(upload.fileId)

      const download = await request.get(resolveUrl(target, migrated!.content))
      expect(download.status()).toBe(200)
      const downloaded = Buffer.from(await download.body())
      expect(sha256Hex(downloaded), '目标资源字节应与源一致').toBe(upload.sha256)

      // 页面锁版:迁移后的目标必须可以成功锁版
      await gotoPage(page, target, '/config')
      await selectEnvPipelineOnConfigPage(page, 'QA qa_prod (qa_prod)', 'QA main (main)')
      await expect(page.getByRole('button', { name: '锁版' })).toBeEnabled()
      await page.getByRole('button', { name: '锁版' }).click()
      const lockDialog = page.getByRole('dialog').filter({ has: page.locator('#freeze-reason') })
      await lockDialog.locator('#freeze-reason').fill('迁移后锁版验收')
      await lockDialog.getByRole('button', { name: '确认锁定' }).click()
      await expect(page.getByText('已冻结', { exact: true }).first()).toBeVisible()
      await expect(page.locator('[data-sonner-toast]').filter({ hasText: '跨范围' })).toHaveCount(0)

      // 版本创建:不得再报 cross_scope_reference
      const release = await postJson(target, '/api/v1/release/create', {
        environment_key: scopeProdMain.env,
        pipeline_key: scopeProdMain.pipeline,
        version: 'v1',
        expected_freeze_revision: (await scopeStatus(target, scopeProdMain)).revision,
        request_id: randomUUID(),
        reason: '',
      })
      expect(release.status, JSON.stringify(release.json).slice(0, 300)).toBe(200)
      expect(JSON.stringify(release.json)).not.toContain('cross_scope_reference')

      writeEvidence('freeze-migration-image', {
        sourceAssetId: upload.fileId,
        targetContent: migrated!.content,
        targetAssetId: match?.[1] ?? '',
        targetSha256: sha256Hex(downloaded),
        releaseVersion: release.json?.data?.meta?.version,
        targetLockState: (await scopeStatus(target, scopeProdMain)).state,
      })
    } finally {
      // 先解除目标冻结,再清理源失败样本与目标结果:
      // 缺资源样本会命中锁版资源预检,不能留给其他用例
      if ((await scopeStatus(target, scopeProdMain)).state === 'locked') {
        await unlockScopeFromCurrentRevision(target, scopeProdMain, token)
      }
      for (const [scope, alias] of [
        [scopeDevMain, 'MIG_BROKEN'],
        [scopeDevMain, 'MIG_IMAGE'],
        [scopeProdMain, 'MIG_BROKEN'],
        [scopeProdMain, 'MIG_IMAGE'],
      ] as const) {
        const row = (await listConfigs(target, scope)).find((item) => item.alias === alias)
        if (row) await deleteConfig(target, scope, row.resource_key)
      }
    }
  })
})
