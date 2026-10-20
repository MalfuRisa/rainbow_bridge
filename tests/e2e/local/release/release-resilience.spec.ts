import { ensureBrowserSession } from '../support/pages'
import { expect, test, type Route } from '@playwright/test'
import { freezeToken, targetA, writeEvidence } from '../support/env'
import { createConfig, ensureScope, listConfigs, scopeDevBeta, type Scope } from '../support/scopes'
import { postJson, request } from '../support/net'
import { selectEnvPipelineOnConfigPage } from '../support/pages'
import { lockScopeFromCurrentRevision, scopeStatus, unlockScopeFromCurrentRevision } from '../freeze/support'

/**
 * 复核回归(F3/F4):
 * - 创建/切换的响应丢失后,"确认结果"必须原样重发首次提交的整份请求,不被后来的状态变化挡住;
 * - 同范围的迟到响应不得覆盖更新后的页面状态(A→B→A 与刷新乱序)。
 * 使用独立范围 qa_resilience/resilience,避免影响其他规格的 qa_dev/* 与 qa_prod/*。
 */
const scopeResilience: Scope = {
  env: 'qa_resilience',
  pipeline: 'resilience',
  label: 'qa_resilience/resilience',
  sentinel: 'KEEP_RESILIENCE',
}

const ENV_OPTION = 'QA qa_resilience (qa_resilience)'
const PIPELINE_OPTION = 'QA resilience (resilience)'

async function ensureSample(target: ReturnType<typeof targetA>, token: string, alias: string, content: string) {
  const status = await scopeStatus(target, scopeResilience)
  if (status.state === 'locked') {
    await unlockScopeFromCurrentRevision(target, scopeResilience, token)
  }
  const rows = await listConfigs(target, scopeResilience)
  const existing = rows.find((item) => item.alias === alias)
  if (existing) {
    await postJson(target, '/api/v1/config/update', {
      config: {
        resource_key: existing.resource_key,
        environment_key: scopeResilience.env,
        pipeline_key: scopeResilience.pipeline,
        alias: existing.alias,
        name: existing.name,
        type: existing.type,
        content,
      },
    })
  } else {
    await createConfig(target, scopeResilience, { alias, name: alias, type: 'text', content })
  }
  return lockScopeFromCurrentRevision(target, scopeResilience, token)
}

async function activateViaHttp(
  target: ReturnType<typeof targetA>,
  token: string,
  version: string,
  expectedRevision: string,
) {
  const response = await postJson(
    target,
    '/api/v1/release/activate',
    {
      environment_key: scopeResilience.env,
      pipeline_key: scopeResilience.pipeline,
      version,
      expected_active_revision: expectedRevision,
      request_id: crypto.randomUUID(),
      reason: `HTTP 切换 ${version}`,
    },
    { Authorization: `Bearer ${token}` },
  )
  expect(response.status, response.text.slice(0, 300)).toBe(200)
  return response
}

async function scopeStateViaHttp(target: ReturnType<typeof targetA>) {
  const response = await request(
    target,
    `/api/v1/release/active?environment_key=${scopeResilience.env}&pipeline_key=${scopeResilience.pipeline}`,
    { method: 'GET' },
  )
  return JSON.parse(response.text).data.active as { version: string; revision: string } | null
}

test.describe('发布流程韧性复核回归 @release', () => {
  test('创建响应丢失后确认原请求:状态变化仍可确认且原样重发 @release', async ({ page }) => {
    test.setTimeout(240_000)
    const target = targetA()
    const token = freezeToken()
    await ensureScope(target, scopeResilience)
    const revision = await ensureSample(target, token, 'RES_TEXT', 'resilience-v1')

    const bodies: Array<Record<string, unknown>> = []
    let intercepted = false
    await page.route('**/api/v1/release/create', async (route: Route) => {
      bodies.push(JSON.parse(route.request().postData() ?? '{}'))
      if (!intercepted) {
        intercepted = true
        // 服务端已完成提交,但客户端拿不到响应
        await route.fetch()
        await route.abort('failed')
        return
      }
      await route.continue()
    })

    await ensureBrowserSession(page, target)
    await page.goto(`${target.origin}${target.prefix}/releases`, { waitUntil: 'domcontentloaded' })
    await selectEnvPipelineOnConfigPage(page, ENV_OPTION, PIPELINE_OPTION)
    await page.getByRole('button', { name: '创建版本' }).click()
    const dialog = page.getByRole('dialog').filter({ has: page.locator('#release-version') })
    await dialog.locator('#release-version').fill('v1.0')
    await dialog.locator('#release-reason').fill('韧性创建')
    await dialog.getByRole('button', { name: '保存版本' }).click()

    const retry = dialog.getByRole('button', { name: '确认结果(同一请求标识)' })
    await expect(retry).toBeVisible({ timeout: 15_000 })
    await expect(dialog.getByTestId('restore-pending')).toContainText('来源 revision')

    // 后台改变来源状态:解锁后"确认已提交结果"不得被冻结状态挡住
    await unlockScopeFromCurrentRevision(target, scopeResilience, token)

    await retry.click()
    await expect(
      page.locator('[data-sonner-toast]').filter({ hasText: '该请求已提交过:返回原已保存版本' }).first(),
    ).toBeVisible({ timeout: 15_000 })

    expect(bodies.length, '应恰好发送两次(首次 + 确认)').toBe(2)
    expect(bodies[1], '确认必须原样重发首次提交的整份请求').toEqual(bodies[0])
    expect(bodies[0].expected_freeze_revision).toBe(revision)

    const list = await request(
      target,
      `/api/v1/release/list?environment_key=${scopeResilience.env}&pipeline_key=${scopeResilience.pipeline}`,
      { method: 'GET' },
    )
    const items = JSON.parse(list.text).data.items as Array<Record<string, unknown>>
    expect(items.length, '确认后应只有一个版本').toBe(1)

    writeEvidence('release-resilience-create', {
      firstRequest: bodies[0],
      retryRequest: bodies[1],
      versionCount: items.length,
    })
  })

  test('切换响应丢失后确认原请求:revision 变化不改变原请求且不重复切换 @release', async ({ page }) => {
    test.setTimeout(240_000)
    const target = targetA()
    const token = freezeToken()
    await ensureScope(target, scopeResilience)

    // 准备 v1.1(内容变化后重新锁版创建)
    await ensureSample(target, token, 'RES_TEXT', 'resilience-v2')
    const created = await postJson(
      target,
      '/api/v1/release/create',
      {
        environment_key: scopeResilience.env,
        pipeline_key: scopeResilience.pipeline,
        version: 'v1.1',
        expected_freeze_revision: (await scopeStatus(target, scopeResilience)).revision,
        request_id: crypto.randomUUID(),
        reason: '韧性第二版',
      },
      { Authorization: `Bearer ${token}` },
    )
    if (created.status !== 200) {
      expect(created.json?.error_type, created.text.slice(0, 300)).toBe('RELEASE_VERSION_EXISTS')
    }

    // 先把 v1.0 设为生效版本,记录基线 revision
    const beforeState = await scopeStateViaHttp(target)
    if (!beforeState || beforeState.version !== 'v1.0') {
      const expected = beforeState ? beforeState.revision : '0'
      const activated = await activateViaHttp(target, token, 'v1.0', expected)
      expect(JSON.parse(activated.text).data.operation.applied_revision).toBeTruthy()
    }
    const baseState = await scopeStateViaHttp(target)
    expect(baseState?.version).toBe('v1.0')
    const baseRevision = baseState?.revision ?? '0'

    const bodies: Array<Record<string, unknown>> = []
    let intercepted = false
    await page.route('**/api/v1/release/activate', async (route: Route) => {
      bodies.push(JSON.parse(route.request().postData() ?? '{}'))
      if (!intercepted) {
        intercepted = true
        await route.fetch()
        await route.abort('failed')
        return
      }
      await route.continue()
    })

    await ensureBrowserSession(page, target)
    await page.goto(`${target.origin}${target.prefix}/releases`, { waitUntil: 'domcontentloaded' })
    await selectEnvPipelineOnConfigPage(page, ENV_OPTION, PIPELINE_OPTION)
    const v11Row = page.locator('tr').filter({ hasText: 'v1.1' }).first()
    await expect(v11Row).toBeVisible()
    await v11Row.getByRole('button', { name: /切换生效版本|切回此版本/ }).click()
    const dialog = page.getByRole('dialog').filter({ has: page.locator('#activate-reason') })
    await dialog.locator('#activate-reason').fill('韧性切换')
    await dialog.getByRole('button', { name: /切换生效版本|切回此版本/ }).click()

    const retry = dialog.getByRole('button', { name: '确认结果(同一请求标识)' })
    await expect(retry).toBeVisible({ timeout: 15_000 })
    expect(bodies.length).toBe(1)
    expect(bodies[0].expected_active_revision).toBe(baseRevision)

    // 后台再切回 v1.0:服务端 revision 已经前进,而本请求要确认的是首次提交的内容。
    // 说明:弹窗是模态,页面上的“刷新”按钮会被遮罩挡住(点击会顺带关闭弹窗),
    // 因此这里用 HTTP 制造状态漂移;确认请求仍必须原样携带首次的 expected_active_revision。
    const afterLost = await scopeStateViaHttp(target)
    expect(afterLost?.version, '首次切换已提交(v1.1)').toBe('v1.1')
    await activateViaHttp(target, token, 'v1.0', afterLost?.revision ?? '0')
    const drifted = await scopeStateViaHttp(target)
    expect(drifted?.version).toBe('v1.0')
    expect(drifted?.revision, '服务端 revision 已与首次提交不同').not.toBe(baseRevision)

    await retry.click()
    await expect(
      page.locator('[data-sonner-toast]').filter({ hasText: /该请求已提交过:原生效 revision/ }).first(),
    ).toBeVisible({ timeout: 15_000 })

    expect(bodies.length, '应恰好发送两次(首次 + 确认)').toBe(2)
    expect(bodies[1], '确认必须原样重发首次提交的整份请求').toEqual(bodies[0])

    const finalState = await scopeStateViaHttp(target)
    expect(finalState?.version, '确认原请求不得把生效版本再切回 v1.1').toBe('v1.0')

    writeEvidence('release-resilience-activate', {
      firstRequest: bodies[0],
      retryRequest: bodies[1],
      activeAfterConfirm: finalState,
    })
  })

  test('迟到响应不得覆盖同范围最新状态(A→B→A) @release', async ({ page }) => {
    test.setTimeout(180_000)
    const target = targetA()
    await ensureScope(target, scopeResilience)
    await ensureScope(target, scopeDevBeta)

    // 与前面的用例解耦:范围内至少要有一个版本
    const listBefore = await request(
      target,
      `/api/v1/release/list?environment_key=${scopeResilience.env}&pipeline_key=${scopeResilience.pipeline}`,
      { method: 'GET' },
    )
    if ((JSON.parse(listBefore.text).data.items as unknown[]).length === 0) {
      const token = freezeToken()
      const revision = await ensureSample(target, token, 'RES_TEXT', 'resilience-v1')
      const created = await postJson(
        target,
        '/api/v1/release/create',
        {
          environment_key: scopeResilience.env,
          pipeline_key: scopeResilience.pipeline,
          version: 'v1.0',
          expected_freeze_revision: revision,
          request_id: crypto.randomUUID(),
          reason: '韧性基线版本',
        },
        { Authorization: `Bearer ${token}` },
      )
      expect(created.status, created.text.slice(0, 300)).toBe(200)
    }

    const staleBody = JSON.stringify({
      code: 200,
      msg: 'OK',
      data: { items: [], has_more: false, next_before_id: '', active: null },
    })
    let held: Route | null = null
    let listCalls = 0
    await page.route('**/api/v1/release/list**', async (route: Route) => {
      listCalls += 1
      if (listCalls === 1) {
        // 第一次 A 的响应先扣住,稍后用"旧状态"放回
        held = route
        return
      }
      await route.continue()
    })

    await ensureBrowserSession(page, target)
    await page.goto(`${target.origin}${target.prefix}/releases`, { waitUntil: 'domcontentloaded' })
    await selectEnvPipelineOnConfigPage(page, ENV_OPTION, PIPELINE_OPTION)
    const v10Row = page.locator('tr').filter({ hasText: 'v1.0' }).first()
    // 第一次 A 的列表响应被扣住:此时页面仍在加载,先切到 B,再回到 A 取到新数据

    // A → B:切到空的 qa_dev/beta
    await page.getByRole('combobox').first().click()
    await page.getByRole('option', { name: 'QA qa_dev (qa_dev)' }).click()
    await page.getByRole('combobox').nth(1).click()
    await page.getByRole('option', { name: 'QA beta (beta)' }).click()
    await expect(page.getByText('暂无版本:先锁版,再点击“创建版本”')).toBeVisible()

    // B → A:回到 qa_resilience,新请求应正常渲染
    await page.getByRole('combobox').first().click()
    await page.getByRole('option', { name: ENV_OPTION }).click()
    await page.getByRole('combobox').nth(1).click()
    await page.getByRole('option', { name: PIPELINE_OPTION }).click()
    await expect(v10Row).toBeVisible({ timeout: 15_000 })

    // 放回第一次 A 的旧响应(空列表 + 无生效版本):必须被丢弃
    expect(held, '第一次 A 的列表请求应已被扣住').not.toBeNull()
    await held!.fulfill({ status: 200, contentType: 'application/json', body: staleBody })
    await page.waitForTimeout(600)

    await expect(v10Row, '迟到响应不得清空当前列表').toBeVisible()
    await expect(
      page.getByText('暂无版本:先锁版,再点击“创建版本”'),
      '迟到响应不得把页面退回旧状态',
    ).toHaveCount(0)

    writeEvidence('release-resilience-stale', {
      listCalls,
      staleServed: true,
      visibleAfterStale: 'v1.0',
    })
  })

  test('旧批次的响应不得提前结束新批次的加载态 @release', async ({ page }) => {
    test.setTimeout(180_000)
    const target = targetA()
    await ensureScope(target, scopeResilience)
    await ensureScope(target, scopeDevBeta)

    const emptyBody = JSON.stringify({
      code: 200,
      msg: 'OK',
      data: { items: [], has_more: false, next_before_id: '', active: null },
    })
    const held: Route[] = []
    let listCalls = 0
    await page.route('**/api/v1/release/list**', async (route: Route) => {
      listCalls += 1
      if (listCalls <= 2) {
        // 前两次列表请求(A 与 B)都先扣住,由测试决定放回顺序
        held.push(route)
        return
      }
      await route.continue()
    })

    await ensureBrowserSession(page, target)
    await page.goto(`${target.origin}${target.prefix}/releases`, { waitUntil: 'domcontentloaded' })
    await selectEnvPipelineOnConfigPage(page, ENV_OPTION, PIPELINE_OPTION)
    await expect(page.getByRole('button', { name: '刷新' })).toBeVisible()

    // 切到空的 B(qa_dev/beta),其列表请求也被扣住
    await page.getByRole('combobox').first().click()
    await page.getByRole('option', { name: 'QA qa_dev (qa_dev)' }).click()
    await page.getByRole('combobox').nth(1).click()
    await page.getByRole('option', { name: 'QA beta (beta)' }).click()
    await expect.poll(() => held.length, { timeout: 10_000 }).toBe(2)

    const emptyState = page.getByText('暂无版本:先锁版,再点击“创建版本”')
    await expect(emptyState, 'B 尚未返回,不应显示空态').toHaveCount(0)

    // 只放回旧 A 的响应:B 仍在途,加载态必须保持
    await held[0].fulfill({ status: 200, contentType: 'application/json', body: emptyBody })
    await page.waitForTimeout(600)
    await expect(emptyState, '旧批次结束不得提前关闭新批次的加载态').toHaveCount(0)

    // B 真正返回后空态才出现
    await held[1].fulfill({ status: 200, contentType: 'application/json', body: emptyBody })
    await expect(emptyState).toBeVisible({ timeout: 10_000 })

    writeEvidence('release-resilience-loading', {
      listCalls,
      staleReleasedFirst: true,
      emptyStateAfterStale: 0,
      emptyStateAfterCurrent: 1,
    })
  })
})