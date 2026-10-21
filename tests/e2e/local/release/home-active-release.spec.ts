import { existsSync } from 'node:fs'
import { copyFile, rm } from 'node:fs/promises'
import path from 'node:path'
import { expect, test, type Page } from '@playwright/test'
import { releaseSnapshotsDir, freezeToken, targetA, writeEvidence } from '../support/env'
import {
  TINY_TXT,
  createConfig,
  deleteConfig,
  ensureScope,
  listConfigs,
  scopeDevMain,
  updateConfig,
  uploadAsset,
  type Scope,
} from '../support/scopes'
import { postJson, request } from '../support/net'
import { expectPageTitle, gotoPage, selectEnvPipelineOnConfigPage, waitToast } from '../support/pages'
import { lockScopeFromCurrentRevision, scopeStatus, unlockScopeFromCurrentRevision } from '../freeze/support'

/**
 * 首页(RB-HOME-RELEASE-01):只展示生效版本、不拿编辑区顶替、导出固定页面展示的快照。
 * 用独立范围 qa_home/* 与 qa_home_empty,避免改动其他规格使用的 qa_dev/qa_prod/qa_resilience。
 */
const scopeHome: Scope = { env: 'qa_homepage', pipeline: 'main', label: 'qa_homepage/main', sentinel: 'KEEP_HOME' }
const scopeHomeEmpty: Scope = { env: 'qa_home_empty', pipeline: 'main', label: 'qa_home_empty/main', sentinel: 'KEEP_HOME_EMPTY' }

const HOME_ENV_OPTION = 'QA qa_homepage'
const HOME_PIPELINE_OPTION = 'QA main'
const EMPTY_ENV_OPTION = 'QA qa_home_empty'

type Target = ReturnType<typeof targetA>

interface ReleaseRow {
  meta: { snapshot_id: string; version: string; config_count: number }
  assets: { asset_id: string; file_name: string }[]
}

async function createVersion(target: Target, token: string, scope: Scope, version: string, reason: string) {
  const status = await scopeStatus(target, scope)
  const response = await postJson(
    target,
    '/api/v1/release/create',
    {
      environment_key: scope.env,
      pipeline_key: scope.pipeline,
      version,
      expected_freeze_revision: status.revision,
      request_id: crypto.randomUUID(),
      reason,
    },
    { Authorization: `Bearer ${token}` },
  )
  expect(response.status, `创建 ${version}: ${response.text.slice(0, 300)}`).toBe(200)
  return response
}

async function activateVersion(target: Target, token: string, scope: Scope, version: string, reason: string) {
  const activeResponse = await request(
    target,
    `/api/v1/release/active?environment_key=${scope.env}&pipeline_key=${scope.pipeline}`,
    { method: 'GET' },
  )
  const active = JSON.parse(activeResponse.text).data.active as { revision: string } | null
  const response = await postJson(
    target,
    '/api/v1/release/activate',
    {
      environment_key: scope.env,
      pipeline_key: scope.pipeline,
      version,
      expected_active_revision: active?.revision ?? '0',
      request_id: crypto.randomUUID(),
      reason,
    },
    { Authorization: `Bearer ${token}` },
  )
  expect(response.status, `切换 ${version}: ${response.text.slice(0, 300)}`).toBe(200)
  return response
}

async function releaseRow(target: Target, scope: Scope, version: string): Promise<ReleaseRow> {
  const response = await request(
    target,
    `/api/v1/release/detail?environment_key=${scope.env}&pipeline_key=${scope.pipeline}&version=${encodeURIComponent(version)}`,
    { method: 'GET' },
  )
  expect(response.status, response.text.slice(0, 200)).toBe(200)
  return JSON.parse(response.text).data as ReleaseRow
}

/** 打开首页并选中范围,等待生效版本渲染完成。 */
async function openHome(target: Target, page: Page, envOption: string, pipelineOption: string): Promise<void> {
  await gotoPage(page, target, '/')
  await expectPageTitle(page, '项目概览')
  await selectEnvPipelineOnConfigPage(page, envOption, pipelineOption)
}

function trackRequests(page: Page): string[] {
  const seen: string[] = []
  page.on('request', (req) => seen.push(req.url()))
  return seen
}

test.describe('首页生效配置与静态导出 @release', () => {
  test('首页只展示生效版本:编辑区与未生效版本不改变首页,切换后刷新才变化 @release', async ({ page }) => {
    test.setTimeout(240_000)
    const target = targetA()
    const token = freezeToken()
    await ensureScope(target, scopeHome)
    await ensureScope(target, scopeDevMain)

    // 基线:两条配置 + v1.0 并生效
    let status = await scopeStatus(target, scopeHome)
    if (status.state === 'locked') {
      await unlockScopeFromCurrentRevision(target, scopeHome, token)
      status = await scopeStatus(target, scopeHome)
    }
    const rows = await listConfigs(target, scopeHome)
    if (!rows.find((item) => item.alias === 'HOME_A')) {
      await createConfig(target, scopeHome, { alias: 'HOME_A', name: '首页样本', type: 'text', content: 'home-v1' })
    }
    if (!rows.find((item) => item.alias === 'HOME_B')) {
      await createConfig(target, scopeHome, { alias: 'HOME_B', name: '首页样本二', type: 'text', content: 'keep' })
    }
    await lockScopeFromCurrentRevision(target, scopeHome, token)
    await createVersion(target, token, scopeHome, 'v1.0', '首页基线')
    await activateVersion(target, token, scopeHome, 'v1.0', '首页基线生效')
    const v10 = await releaseRow(target, scopeHome, 'v1.0')

    const requests = trackRequests(page)
    await openHome(target, page, HOME_ENV_OPTION, HOME_PIPELINE_OPTION)

    // H01:展示生效版本的版本号、配置数与条目
    await expect(page.getByText('当前生效配置')).toBeVisible()
    await expect(page.getByText('v1.0', { exact: true }).first()).toBeVisible()
    await expect(page.getByText('配置 2 项')).toBeVisible()
    await expect(page.getByRole('cell', { name: 'HOME_A', exact: true })).toBeVisible()
    await expect(page.getByText('home-v1')).toBeVisible()
    await expect(page.getByText('本次加载:')).toBeVisible()

    // H02:编辑区新增未发布项、修改已发布项 → 首页刷新仍为 v1.0 与旧内容
    await unlockScopeFromCurrentRevision(target, scopeHome, token)
    await createConfig(target, scopeHome, { alias: 'EDITOR_ONLY', name: '仅编辑区', type: 'text', content: 'not-published' })
    const current = await listConfigs(target, scopeHome)
    const homeA = current.find((item) => item.alias === 'HOME_A')
    if (homeA) {
      await updateConfig(target, scopeHome, {
        resource_key: homeA.resource_key,
        alias: homeA.alias,
        name: homeA.name,
        type: homeA.type,
        content: 'home-v2',
      })
    }
    await lockScopeFromCurrentRevision(target, scopeHome, token)
    await createVersion(target, token, scopeHome, 'v1.1', '未生效版本')

    await page.getByRole('button', { name: '刷新' }).click()
    await expect(page.getByText('配置 2 项')).toBeVisible()
    await expect(page.getByText('v1.0', { exact: true }).first()).toBeVisible()
    await expect(page.getByText('home-v1')).toBeVisible()
    await expect(page.getByRole('cell', { name: 'EDITOR_ONLY', exact: true })).toHaveCount(0)
    await expect(page.getByText('not-published')).toHaveCount(0)

    // 旧接口兼容(显式验证):首页显示 v1.0 的同时,/runtime/config 仍返回编辑区内容
    const legacyRead = await request(target, '/api/v1/runtime/config', {
      method: 'GET',
      headers: { 'x-environment': scopeHome.env, 'x-pipeline': scopeHome.pipeline },
    })
    expect(legacyRead.status, legacyRead.text.slice(0, 200)).toBe(200)
    const legacyConfigs = (JSON.parse(legacyRead.text).data.configs ?? []) as { alias: string; content: string }[]
    expect(legacyConfigs.some((item) => item.alias === 'EDITOR_ONLY'), '旧接口应读编辑区').toBe(true)
    expect(legacyConfigs.find((item) => item.alias === 'HOME_A')?.content).toBe('home-v2')

    // H03:切换生效到 v1.1 → 刷新后版本、快照与正文一起变化
    await activateVersion(target, token, scopeHome, 'v1.1', '切换到 v1.1')
    await page.getByRole('button', { name: '刷新' }).click()
    await expect(page.getByText('v1.1', { exact: true }).first()).toBeVisible()
    await expect(page.getByText('home-v2')).toBeVisible()
    await expect(page.getByRole('cell', { name: 'EDITOR_ONLY', exact: true })).toBeVisible()
    await expect(page.getByText('配置 3 项')).toBeVisible()

    // 页面只读生效读取:没有旧 config/static 读取
    const legacy = requests.filter((url) => url.includes('/api/v1/runtime/config') || url.includes('/api/v1/runtime/static'))
    expect(legacy, `首页不得请求旧接口: ${legacy.join(', ')}`).toEqual([])

    writeEvidence('home-active-basic', {
      activeSnapshot: v10.meta.snapshot_id,
      requests: requests.filter((url) => url.includes('/api/v1/runtime/')),
    })
  })

  test('从未发布的编辑区不替代:明确尚无生效版本并提供发布页入口 @release', async ({ page }) => {
    test.setTimeout(180_000)
    const target = targetA()
    const token = freezeToken()
    await ensureScope(target, scopeHomeEmpty)

    const status = await scopeStatus(target, scopeHomeEmpty)
    if (status.state === 'locked') {
      await unlockScopeFromCurrentRevision(target, scopeHomeEmpty, token)
    }
    const rows = await listConfigs(target, scopeHomeEmpty)
    if (!rows.find((item) => item.alias === 'HOME_EDITOR_ONLY')) {
      await createConfig(target, scopeHomeEmpty, { alias: 'HOME_EDITOR_ONLY', name: '编辑区样本', type: 'text', content: 'editor-only' })
    }

    const requests = trackRequests(page)
    await openHome(target, page, EMPTY_ENV_OPTION, HOME_PIPELINE_OPTION)

    await expect(page.getByTestId('not-active-row')).toContainText('尚无生效版本')
    await expect(page.getByText('editor-only')).toHaveCount(0)
    await expect(page.getByRole('button', { name: /导出此版本静态包/ })).toBeDisabled()
    const publishLink = page.getByTestId('not-active-row').getByRole('link', { name: /前往版本发布/ })
    await expect(publishLink).toBeVisible()
    const href = await publishLink.getAttribute('href')
    expect(href).toContain('/releases')
    expect(href).toContain(`env=${scopeHomeEmpty.env}`)
    expect(href).toContain(`pipeline=${scopeHomeEmpty.pipeline}`)

    const legacy = requests.filter((url) => url.includes('/api/v1/runtime/config'))
    expect(legacy, `未发布时不得回退编辑区: ${legacy.join(', ')}`).toEqual([])
  })

  test('空版本:页面显示本版本没有配置,并允许导出空版本静态包 @release', async ({ page }) => {
    test.setTimeout(180_000)
    const target = targetA()
    const token = freezeToken()
    const scopeHomeBlank: Scope = { env: 'qa_home_blank', pipeline: 'main', label: 'qa_home_blank/main', sentinel: '' }
    await ensureScope(target, scopeHomeBlank)
    const status = await scopeStatus(target, scopeHomeBlank)
    if (status.state === 'locked') {
      await unlockScopeFromCurrentRevision(target, scopeHomeBlank, token)
    }
    // 保证为空:删除既有配置
    for (const row of await listConfigs(target, scopeHomeBlank)) {
      await deleteConfig(target, scopeHomeBlank, row.resource_key)
    }
    await lockScopeFromCurrentRevision(target, scopeHomeBlank, token)
    await createVersion(target, token, scopeHomeBlank, 'v1.0', '空版本')
    await activateVersion(target, token, scopeHomeBlank, 'v1.0', '空版本生效')
    const row = await releaseRow(target, scopeHomeBlank, 'v1.0')

    await openHome(target, page, 'QA qa_home_blank', HOME_PIPELINE_OPTION)
    await expect(page.getByTestId('empty-version-row')).toContainText('本版本没有配置')
    await expect(page.getByText('配置 0 项')).toBeVisible()

    const exportButton = page.getByRole('button', { name: /导出此版本静态包/ })
    await expect(exportButton).toBeEnabled()
    const [download] = await Promise.all([
      page.waitForEvent('download'),
      exportButton.click(),
    ])
    expect(download.suggestedFilename()).toBe(`release-${row.meta.snapshot_id}-v1.0.zip`)
    const saved = await download.path()
    expect(saved, '下载文件应落盘').toBeTruthy()
    expect(existsSync(saved as string)).toBe(true)
    await waitToast(page, '已导出版本 v1.0 的静态包')

    writeEvidence('home-active-empty-export', {
      snapshotId: row.meta.snapshot_id,
      fileName: download.suggestedFilename(),
    })
  })

  test('生效版本资源缺失:错误态且旧结果不可导出,补回后恢复 @release', async ({ page }) => {
    test.setTimeout(240_000)
    const target = targetA()
    const token = freezeToken()
    const scopeHomeAsset: Scope = { env: 'qa_home_asset', pipeline: 'main', label: 'qa_home_asset/main', sentinel: '' }
    await ensureScope(target, scopeHomeAsset)
    const status = await scopeStatus(target, scopeHomeAsset)
    if (status.state === 'locked') {
      await unlockScopeFromCurrentRevision(target, scopeHomeAsset, token)
    }
    const upload = await uploadAsset(target, scopeHomeAsset, {
      fileName: 'home-asset.txt',
      contentType: 'text/plain',
      bytes: TINY_TXT,
    })
    const rows = await listConfigs(target, scopeHomeAsset)
    if (!rows.find((item) => item.alias === 'HOME_FILE')) {
      await createConfig(target, scopeHomeAsset, { alias: 'HOME_FILE', name: '资源样本', type: 'file', content: upload.reference })
    }
    await lockScopeFromCurrentRevision(target, scopeHomeAsset, token)
    await createVersion(target, token, scopeHomeAsset, 'v1.0', '资源版本')
    await activateVersion(target, token, scopeHomeAsset, 'v1.0', '资源版本生效')
    const row = await releaseRow(target, scopeHomeAsset, 'v1.0')
    expect(row.assets.length).toBe(1)

    const assetPath = path.join(
      releaseSnapshotsDir(target),
      row.meta.snapshot_id,
      'assets',
      `${row.assets[0].asset_id}.bin`,
    )
    expect(existsSync(assetPath), `快照资源应存在: ${assetPath}`).toBe(true)
    const backupPath = `${assetPath}.bak`
    await copyFile(assetPath, backupPath)

    try {
      await rm(assetPath, { force: true })
      await openHome(target, page, 'QA qa_home_asset', HOME_PIPELINE_OPTION)
      await expect(page.getByTestId('error-row')).toContainText('生效版本不完整')
      await expect(page.getByRole('button', { name: /导出此版本静态包/ })).toBeDisabled()
      await expect(page.getByRole('cell', { name: 'HOME_FILE', exact: true })).toHaveCount(0)

      // 补回文件后重试即可恢复
      await copyFile(backupPath, assetPath)
      await page.getByRole('button', { name: '重试' }).click()
      await expect(page.getByRole('cell', { name: 'HOME_FILE', exact: true })).toBeVisible()
      await expect(page.getByRole('button', { name: /导出此版本静态包/ })).toBeEnabled()
    } finally {
      await copyFile(backupPath, assetPath).catch(() => undefined)
      await rm(backupPath, { force: true })
    }
  })

  test('读取失败与不兼容响应:独立错误态、可重试、不回退旧接口 @release', async ({ page }) => {
    test.setTimeout(240_000)
    const target = targetA()
    await ensureScope(target, scopeHome)

    const requests = trackRequests(page)
    await openHome(target, page, HOME_ENV_OPTION, HOME_PIPELINE_OPTION)
    await expect(page.getByText('v1.1', { exact: true }).first()).toBeVisible({ timeout: 15_000 })

    // 网络失败
    await page.route('**/api/v1/runtime/release?*', (route) => route.abort('failed'))
    await page.getByRole('button', { name: '刷新' }).click()
    await expect(page.getByTestId('error-row')).toBeVisible()
    await expect(page.getByRole('button', { name: /导出此版本静态包/ })).toBeDisabled()
    await expect(page.getByRole('cell', { name: 'HOME_A', exact: true })).toHaveCount(0)
    await page.unroute('**/api/v1/runtime/release?*')

    // 200 HTML(旧服务器忽略 raw 参数或静态兜底)
    await page.route('**/api/v1/runtime/release?*', (route) =>
      route.fulfill({ status: 200, contentType: 'text/html', body: '<!doctype html><html><body>index</body></html>' }),
    )
    await page.getByRole('button', { name: '重试' }).click()
    await expect(page.getByTestId('error-row')).toBeVisible()
    await page.unroute('**/api/v1/runtime/release?*')

    // 结构不符:selection_mode 不是 active
    await page.route('**/api/v1/runtime/release?*', (route) =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          code: 200,
          msg: 'OK',
          data: {
            selection_mode: 'fixed',
            snapshot_id: 'x',
            version: 'v1.0',
            environment: { environment_key: scopeHome.env, pipeline_key: scopeHome.pipeline },
            configs: [],
          },
        }),
      }),
    )
    await page.getByRole('button', { name: '重试' }).click()
    await expect(page.getByTestId('error-row')).toBeVisible()
    await page.unroute('**/api/v1/runtime/release?*')

    // 恢复:重试后回到成功态
    await page.getByRole('button', { name: '重试' }).click()
    await expect(page.getByRole('cell', { name: 'HOME_A', exact: true })).toBeVisible()

    const legacy = requests.filter((url) => url.includes('/api/v1/runtime/config'))
    expect(legacy, `失败时不得回退旧接口: ${legacy.join(', ')}`).toEqual([])
    writeEvidence('home-active-errors', { legacyRequests: legacy })
  })

  test('原始精度:大整数原样展示与复制 @release', async ({ page, context }) => {
    test.setTimeout(180_000)
    const target = targetA()
    const token = freezeToken()
    const scopeHomeBig: Scope = { env: 'qa_home_big', pipeline: 'main', label: 'qa_home_big/main', sentinel: '' }
    await ensureScope(target, scopeHomeBig)
    const status = await scopeStatus(target, scopeHomeBig)
    if (status.state === 'locked') {
      await unlockScopeFromCurrentRevision(target, scopeHomeBig, token)
    }
    const bigLiteral = '9007199254740993'
    const rows = await listConfigs(target, scopeHomeBig)
    if (!rows.find((item) => item.alias === 'HOME_BIG')) {
      await createConfig(target, scopeHomeBig, {
        alias: 'HOME_BIG',
        name: '大整数样本',
        type: 'object',
        content: `{"id":${bigLiteral},"long":0.10000000000000000001}`,
      })
    }
    await lockScopeFromCurrentRevision(target, scopeHomeBig, token)
    await createVersion(target, token, scopeHomeBig, 'v1.0', '精度版本')
    await activateVersion(target, token, scopeHomeBig, 'v1.0', '精度版本生效')

    await context.grantPermissions(['clipboard-read', 'clipboard-write'])
    await openHome(target, page, 'QA qa_home_big', HOME_PIPELINE_OPTION)
    const cell = page.getByRole('cell', { name: /9007199254740993/ })
    await expect(cell).toBeVisible()
    await expect(cell).toContainText('0.10000000000000000001')

    await page.getByRole('row', { name: /HOME_BIG/ }).getByRole('button').click()
    const clip = await page.evaluate(() => navigator.clipboard.readText())
    expect(clip).toContain(bigLiteral)
    expect(clip).toContain('0.10000000000000000001')
    expect(clip).not.toContain('9007199254740992')
    writeEvidence('home-active-precision', { clipboard: clip })
  })

  test('导出固定页面展示的快照:后台切版不影响,刷新后跟随 @release', async ({ page }) => {
    test.setTimeout(240_000)
    const target = targetA()
    const token = freezeToken()
    await ensureScope(target, scopeHome)

    await openHome(target, page, HOME_ENV_OPTION, HOME_PIPELINE_OPTION)
    await expect(page.getByText('v1.1', { exact: true }).first()).toBeVisible({ timeout: 15_000 })
    const shown = await releaseRow(target, scopeHome, 'v1.1')

    // 后台把生效版本切回 v1.0(页面不刷新)
    await activateVersion(target, token, scopeHome, 'v1.0', '后台切回 v1.0')

    const exportResponse = page.waitForResponse((resp) => resp.url().includes('/api/v1/runtime/release/static'))
    const [download, response] = await Promise.all([
      page.waitForEvent('download'),
      page.getByRole('button', { name: /导出此版本静态包/ }).click().then(() => exportResponse),
    ])
    expect(response.status()).toBe(200)
    expect(response.headers()['x-rainbow-snapshot-id']).toBe(shown.meta.snapshot_id)
    expect(response.headers()['x-rainbow-version']).toBe('v1.1')
    expect(download.suggestedFilename()).toBe(`release-${shown.meta.snapshot_id}-v1.1.zip`)

    // 刷新后页面跟随新的生效版本,导出也随之为 v1.0
    await page.getByRole('button', { name: '刷新' }).click()
    await expect(page.getByText('v1.0', { exact: true }).first()).toBeVisible()
    const afterRefresh = await releaseRow(target, scopeHome, 'v1.0')
    const secondResponse = page.waitForResponse((resp) => resp.url().includes('/api/v1/runtime/release/static'))
    await Promise.all([
      page.waitForEvent('download'),
      page.getByRole('button', { name: /导出此版本静态包/ }).click().then(() => secondResponse),
    ])
    const second = await secondResponse
    expect(second.headers()['x-rainbow-snapshot-id']).toBe(afterRefresh.meta.snapshot_id)
    expect(second.headers()['x-rainbow-version']).toBe('v1.0')

    writeEvidence('home-active-export-fixed', {
      shown: shown.meta.snapshot_id,
      exportedWhileSwitched: response.headers()['x-rainbow-snapshot-id'],
      exportedAfterRefresh: second.headers()['x-rainbow-snapshot-id'],
    })
  })

  test('导出失败不另存 JSON;导出途中切范围不触发旧下载 @release', async ({ page }) => {
    test.setTimeout(240_000)
    const target = targetA()
    await ensureScope(target, scopeHome)

    let downloads = 0
    page.on('download', () => {
      downloads += 1
    })
    await openHome(target, page, HOME_ENV_OPTION, HOME_PIPELINE_OPTION)
    await expect(page.getByText('v1.0', { exact: true }).first()).toBeVisible({ timeout: 15_000 })

    // H17:导出 503 → 只显示错误,不创建下载、不另存 JSON
    await page.route('**/api/v1/runtime/release/static*', (route) =>
      route.fulfill({
        status: 503,
        contentType: 'application/json',
        body: JSON.stringify({ code: 503, msg: 'error', error: 'release asset file is missing', error_type: 'RELEASE_INCOMPLETE' }),
      }),
    )
    await page.getByRole('button', { name: /导出此版本静态包/ }).click()
    await expect(page.getByTestId('export-error')).toContainText('资源不完整')
    expect(downloads, '导出失败不得创建下载').toBe(0)
    await expect(page.locator('[data-sonner-toast]').filter({ hasText: /已导出为 JSON|已导出版本/ })).toHaveCount(0)
    await page.unroute('**/api/v1/runtime/release/static*')

    // H18:导出未返回时切范围 → 不触发旧下载与旧提示
    await page.route('**/api/v1/runtime/release/static*', async (route) => {
      await new Promise((resolve) => setTimeout(resolve, 1200))
      await route.continue()
    })
    await page.getByRole('button', { name: /导出此版本静态包/ }).click()
    await page.getByRole('combobox').first().click()
    await page.getByRole('option', { name: EMPTY_ENV_OPTION }).click()
    await page.waitForTimeout(2000)
    expect(downloads, '切范围后旧导出不得触发下载').toBe(0)
    await expect(page.getByTestId('export-error')).toHaveCount(0)
    await expect(page.getByRole('button', { name: /导出此版本静态包/ })).toBeDisabled()

    writeEvidence('home-active-export-failure', { downloads })
  })
})
