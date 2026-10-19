import { test } from '@playwright/test'
import { targetA, writeEvidence } from './support/env'
import { createConfig, ensureScope, scopeDevMain, setupSentinelScopes } from './support/scopes'
import { configsByAlias } from './support/db'
import { expectConfigRow, gotoPage, openEditDialog, selectEnvPipelineOnConfigPage } from './support/pages'

const ALIAS = 'EDIT_MULTI'

test.describe('T23 多人编辑 @t23', () => {
  test('两个上下文读同一配置:后写者是否静默覆盖 @t23', async ({ browser }) => {
    test.setTimeout(90_000)
    const target = targetA()
    await ensureScope(target, scopeDevMain)
    await setupSentinelScopes(target, 't23')

    const existing = configsByAlias(target, scopeDevMain.env, scopeDevMain.pipeline, ALIAS)[0]
    if (!existing) {
      await createConfig(target, scopeDevMain, { alias: ALIAS, name: '多人编辑样本', type: 'text', content: 'v1' })
    }

    const contextA = await browser.newContext()
    const contextB = await browser.newContext()
    const pageA = await contextA.newPage()
    const pageB = await contextB.newPage()
    const requestLog: { from: string; at: string; payload: string }[] = []
    pageA.on('request', (request) => {
      if (request.url().includes('/api/v1/config/update')) {
        requestLog.push({ from: 'A', at: new Date().toISOString(), payload: (request.postData() ?? '').slice(0, 200) })
      }
    })
    pageB.on('request', (request) => {
      if (request.url().includes('/api/v1/config/update')) {
        requestLog.push({ from: 'B', at: new Date().toISOString(), payload: (request.postData() ?? '').slice(0, 200) })
      }
    })

    try {
      await gotoPage(pageA, target, '/config')
      await selectEnvPipelineOnConfigPage(pageA, 'QA qa_dev (qa_dev)', 'QA main (main)')
      await gotoPage(pageB, target, '/config')
      await selectEnvPipelineOnConfigPage(pageB, 'QA qa_dev (qa_dev)', 'QA main (main)')

      const dialogA = await openEditDialog(pageA, ALIAS)
      const dialogB = await openEditDialog(pageB, ALIAS)
      // 双方都读到旧值 v1
      const contentSeenByA = await dialogA.locator('#content').inputValue()
      const contentSeenByB = await dialogB.locator('#content').inputValue()

      await dialogA.locator('#content').fill('v2-from-A')
      await dialogA.getByRole('button', { name: '保存' }).click()
      await expectConfigRow(pageA, ALIAS)

      // B 仍持有旧值,继续保存
      await dialogB.locator('#content').fill('v3-from-B')
      await dialogB.getByRole('button', { name: '保存' }).click()
      await expectConfigRow(pageB, ALIAS)

      const finalRows = configsByAlias(target, scopeDevMain.env, scopeDevMain.pipeline, ALIAS)
      const conflictSignals = {
        errorToastOnB: await pageB.locator('[data-sonner-toast]').filter({ hasText: '失败' }).count(),
        warningOnB: await pageB.locator('text=冲突').count(),
      }

      writeEvidence('t23-multi-editor', {
        contentSeenByA,
        contentSeenByB,
        requestLog,
        finalContent: finalRows.map((row) => row.content),
        conflictSignals,
        verdict: '无冲突提示且后写覆盖 = 静默覆盖(OBSERVED)；后续能力依据',
      })
    } finally {
      await contextA.close()
      await contextB.close()
    }
  })
})