import { expect, test } from '@playwright/test'
import { targetA, writeEvidence } from './support/env'
import { configsByAlias } from './support/db'
import { createConfig, getConfigDetail, scopeDevMain, setupSentinelScopes, snapshotAllScopes, updateConfig } from './support/scopes'
import { gotoPage, openEditDialog, selectEnvPipelineOnConfigPage, expectConfigRow } from './support/pages'

test.describe('T04 更新、清空与不可变身份 @t04 @smoke', () => {
  test('描述真清空、alias/resource_key 不可变、重复提交不多建 @t04 @smoke', async ({ page }) => {
    const target = targetA()
    await setupSentinelScopes(target, 't04')
    const alias = 'QA_UPDATE_TARGET'

    const existing = (await configsByAlias(target, scopeDevMain.env, scopeDevMain.pipeline, alias))[0]
    let resourceKey = String(existing?.resource_key ?? '')
    if (!resourceKey) {
      const { config } = await createConfig(target, scopeDevMain, {
        alias,
        name: 'QA 更新目标',
        type: 'text',
        content: 'v1',
        description: '初始描述',
      })
      resourceKey = config!.resource_key
    }

    // 页面改名称
    await gotoPage(page, target, '/config')
    await selectEnvPipelineOnConfigPage(page, 'QA qa_dev (qa_dev)', 'QA main (main)')
    const dialog = await openEditDialog(page, alias)
    await dialog.locator('#name').fill('QA 更新目标-改名后')
    await dialog.getByRole('button', { name: '保存' }).click()
    await expectConfigRow(page, alias)

    // 描述：非空 -> 空 -> 新值(通过 API 验证真实清空；proto JSON 会省略空字符串，按现值为空处理)
    const descEmpty = await updateConfig(target, scopeDevMain, { resource_key: resourceKey, name: 'QA 更新目标-改名后', type: 'text', content: 'v1', description: '' })
    const afterEmpty = await getConfigDetail(target, scopeDevMain, resourceKey)
    expect(afterEmpty.json?.data?.config?.description ?? '', '描述清空后应为空').toBe('')

    await updateConfig(target, scopeDevMain, { resource_key: resourceKey, name: 'QA 更新目标-改名后', type: 'text', content: 'v1', description: '新的描述' })
    const afterNew = await getConfigDetail(target, scopeDevMain, resourceKey)
    expect(afterNew.json?.data?.config?.description).toBe('新的描述')

    // alias 修改尝试：DAO 层直接忽略
    const aliasAttempt = await updateConfig(target, scopeDevMain, {
      resource_key: resourceKey, alias: 'QA_ALIAS_RENAMED', name: 'QA 更新目标-改名后', type: 'text', content: 'v1', description: '新的描述',
    })
    const afterAlias = await getConfigDetail(target, scopeDevMain, resourceKey)
    expect(afterAlias.json?.data?.config?.alias, 'alias 必须保持原值').toBe(alias)
    expect(afterAlias.json?.data?.config?.resource_key).toBe(resourceKey)

    // 重复提交同一更新：不得多建
    await updateConfig(target, scopeDevMain, { resource_key: resourceKey, name: 'QA 更新目标-改名后', type: 'text', content: 'v1', description: '新的描述' })
    await updateConfig(target, scopeDevMain, { resource_key: resourceKey, name: 'QA 更新目标-改名后', type: 'text', content: 'v1', description: '新的描述' })
    const rows = configsByAlias(target, scopeDevMain.env, scopeDevMain.pipeline, alias)
    expect(rows.length, `alias=${alias} 行数`).toBe(1)

    // 哨兵未被改动
    const after = await snapshotAllScopes(target)
    writeEvidence('t04-update', {
      resourceKey,
      nameAfterEdit: afterAlias.json?.data?.config?.name,
      descriptionAfterEmpty: afterEmpty.json?.data?.config?.description,
      descriptionAfterNew: afterNew.json?.data?.config?.description,
      aliasAttemptResponse: aliasAttempt.json,
      rowCount: rows.length,
      scopeSnapshot: after,
    })
  })
})