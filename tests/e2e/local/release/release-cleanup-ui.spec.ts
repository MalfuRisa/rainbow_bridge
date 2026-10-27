import { test, expect } from '@playwright/test'
import { mkdir } from 'node:fs/promises'
import { join } from 'node:path'
import { base, fixture, row, openDelete, deletionResult, ok, fail } from './cleanup-fixture'

test.describe('版本清理页面 @release', () => {
  test('U01 四项操作空白备注可提交，未填写备注不隐藏实际切换记录', async ({ page }) => {
    const { mock } = await fixture(page)
    await expect(page.getByRole('region', { name: '当前发布状态' })).toContainText('admin')
    await expect(page.getByRole('region', { name: '当前发布状态' })).toContainText('未填写备注')
    await page.goto(base() + 'config?env=qa&pipeline=web')
    for (const lock of [false, true]) {
      await page.getByRole('button', { name: lock ? '锁版' : '解除冻结', exact: true }).click()
      await page.getByLabel('备注（选填）').fill(lock ? ' \n ' : '')
      await page.getByRole('button', { name: lock ? '确认锁定' : '确认解除', exact: true }).click()
      await expect(page.getByRole('dialog')).toBeHidden()
      expect(mock.writes(lock ? 'freeze/lock' : 'freeze/unlock').at(-1)!.postDataJSON().reason).toBe('')
    }
    await page.goto(base() + 'releases?env=qa&pipeline=web')
    await page.getByRole('button', { name: '创建版本', exact: true }).click()
    await page.locator('#release-version').fill('v3')
    await page.getByLabel('备注（选填）').fill(' \n ')
    await page.getByRole('button', { name: '保存版本', exact: true }).click()
    await expect(page.getByRole('dialog')).toBeHidden()
    await row(page).getByRole('button', { name: '切换生效版本', exact: true }).click()
    await page.getByRole('dialog').getByRole('button', { name: '切换生效版本', exact: true }).click()
    await expect(page.getByRole('dialog')).toBeHidden()
    for (const endpoint of ['release/create', 'release/activate']) expect(mock.writes(endpoint)[0].postDataJSON().reason).toBe('')
  })

  for (const character of ['中', '😀']) {
    test('U02 备注按Unicode码点计数 ' + character, async ({ page }) => {
      const { mock } = await fixture(page)
      await openDelete(page)
      await page.getByLabel('备注（选填）').fill(character.repeat(501))
      await page.getByRole('dialog').getByRole('button', { name: '删除版本', exact: true }).click()
      await expect(page.getByRole('alert')).toHaveText('备注最多500字')
      expect(mock.count('release/delete')).toBe(0)
      await page.getByLabel('备注（选填）').fill('  ' + character.repeat(500) + '  ')
      await page.getByRole('dialog').getByRole('button', { name: '删除版本', exact: true }).click()
      await expect(page.getByRole('dialog')).toBeHidden()
      expect(Array.from(mock.writes('release/delete')[0].postDataJSON().reason).length).toBe(500)
    })
  }
  test('U03/U04 非生效可删，生效不可删；取消零请求，成功刷新不改变生效版本', async ({ page }) => {
    const { mock } = await fixture(page)
    await expect(row(page, 'v1').getByRole('button', { name: '删除版本', exact: true })).toHaveCount(0)
    await openDelete(page)
    await expect(page.getByRole('dialog')).toContainText('不会释放磁盘空间')
    if (process.env.RB_QA_VISUAL_DIR) {
      await mkdir(process.env.RB_QA_VISUAL_DIR, { recursive: true })
      for (const width of [1280, 1440]) {
        await page.setViewportSize({ width, height: 900 })
        await page.screenshot({ path: join(process.env.RB_QA_VISUAL_DIR, 'delete-' + width + '.png'), animations: 'disabled' })
      }
      await page.evaluate(() => document.documentElement.classList.add('dark'))
      await page.screenshot({ path: join(process.env.RB_QA_VISUAL_DIR, 'delete-dark.png'), animations: 'disabled' })
      await page.evaluate(() => document.documentElement.classList.remove('dark'))
    }
    await page.getByRole('button', { name: '取消', exact: true }).click()
    await expect(page.getByRole('dialog')).toBeHidden()
    expect(mock.count('release/delete')).toBe(0)
    await openDelete(page)
    await page.getByRole('dialog').getByRole('button', { name: '删除版本', exact: true }).dblclick()
    await expect(page.getByRole('dialog')).toBeHidden()
    expect(mock.count('release/delete')).toBe(1)
    await expect(row(page)).toHaveCount(0)
    await expect(row(page, 'v1')).toContainText('生效中')
    expect(mock.count('freeze/unlock')).toBe(0)
    expect(mock.count('release/activate')).toBe(0)
  })
  test('U03/U04 无生效版本也可删，最后一项成功显示空态', async ({ page }) => {
    const { mock, state } = await fixture(page)
    state.active = null; state.items = state.items.slice(0, 1)
    await page.getByRole('button', { name: '刷新', exact: true }).click()
    await openDelete(page)
    await page.getByRole('dialog').getByRole('button', { name: '删除版本', exact: true }).click()
    await expect(page.getByText('暂无版本:先锁版,再点击“创建版本”', { exact: true })).toBeVisible()
    expect(mock.count('release/delete')).toBe(1)
  })
  test('U03 列表active相互矛盾或active读取失败时不提供删除', async ({ page }) => {
    const { mock, state } = await fixture(page)
    state.active = null
    await page.getByRole('button', { name: '刷新', exact: true }).click()
    await expect(row(page).getByRole('button', { name: '删除版本', exact: true })).toHaveCount(0)
    mock.overrides.set('release/active', route => fail(route, 503, 'RELEASE_BUSY'))
    await page.getByRole('button', { name: '刷新', exact: true }).click()
    await expect(page.getByRole('button', { name: '删除版本', exact: true })).toHaveCount(0)
  })
  test('U05 已提交响应丢失→原样确认，双击无重复事件', async ({ page }) => {
    const { mock, state } = await fixture(page)
    let calls = 0
    mock.overrides.set('release/delete', async route => {
      calls++
      const payload = route.request().postDataJSON()
      state.items = state.items.filter(item => item.meta.version !== 'v2')
      if (calls === 1) return route.abort('failed')
      await ok(route, { ...deletionResult(payload), replayed: true })
    })
    await openDelete(page)
    await page.getByRole('dialog').getByRole('button', { name: '删除版本', exact: true }).click()
    await page.getByRole('button', { name: '确认结果', exact: true }).click()
    await expect(page.getByRole('dialog')).toBeHidden()
    expect(mock.writes('release/delete')[0].postDataJSON()).toEqual(mock.writes('release/delete')[1].postDataJSON())
    expect(calls).toBe(2)
  })
  for (const [code, kind, message] of [
    [409, 'RELEASE_DELETE_ACTIVE', '该版本已生效，请先切换到其他版本'],
    [409, 'RELEASE_SNAPSHOT_MISMATCH', '目标版本信息已变化，请刷新后重新选择'],
    [409, 'RELEASE_REQUEST_CONFLICT', '本次请求信息冲突，请重新发起操作'],
    [410, 'RELEASE_VERSION_DELETED', '该版本已被删除'],
    [404, 'NOT_FOUND', '目标版本或范围不存在，请刷新后重新选择'],
  ] as const) {
    test('U06 ' + kind, async ({ page }) => {
      const { mock } = await fixture(page)
      mock.overrides.set('release/delete', route => fail(route, code, kind))
      await openDelete(page)
      await page.getByRole('dialog').getByRole('button', { name: '删除版本', exact: true }).click()
      await expect(page.getByRole('dialog').getByRole('alert')).toHaveText(message)
      await expect(page.getByRole('button', { name: '确认结果', exact: true })).toHaveCount(0)
      expect(mock.count('release/delete')).toBe(1)
    })
  }
  for (const malformed of ['missing', 'mismatch', 'html']) {
    test('U11 200响应无效不报成功 ' + malformed, async ({ page }) => {
      const { mock } = await fixture(page)
      mock.overrides.set('release/delete', route => malformed === 'html'
        ? route.fulfill({ status: 200, contentType: 'text/html', body: '<html>not json</html>' })
        : ok(route, malformed === 'missing' ? {} : { ...deletionResult(route.request().postDataJSON()), snapshot_id: 'wrong' }))
      await openDelete(page)
      await page.getByRole('dialog').getByRole('button', { name: '删除版本', exact: true }).click()
      await expect(page.getByRole('button', { name: '确认结果', exact: true })).toBeVisible()
      await expect(page.getByText('版本 v2 已删除', { exact: true })).toHaveCount(0)
    })
  }
  test('U11 已删除但列表刷新失败，不重发删除', async ({ page }) => {
    const { mock } = await fixture(page)
    await openDelete(page)
    mock.overrides.set('release/list', route => fail(route, 503, 'RELEASE_BUSY'))
    await page.getByRole('dialog').getByRole('button', { name: '删除版本', exact: true }).click()
    await expect(page.getByRole('dialog')).toBeHidden()
    await expect(page.getByText('版本已删除，列表刷新失败，请刷新', { exact: true }).first()).toBeVisible()
    expect(mock.count('release/delete')).toBe(1)
  })
  test('U09 详情刷新410清空旧配置和资源', async ({ page }) => {
    const { mock } = await fixture(page)
    await row(page).getByRole('button', { name: '查看', exact: true }).click()
    await expect(page.getByRole('dialog')).toContainText('光谱标题')
    mock.overrides.set('release/detail', route => fail(route, 410, 'RELEASE_VERSION_DELETED'))
    await page.getByRole('button', { name: '刷新详情', exact: true }).click()
    await expect(page.getByRole('dialog').getByRole('alert')).toHaveText('该版本已被删除')
    await expect(page.getByRole('dialog').getByText('光谱标题', { exact: true })).toHaveCount(0)
  })
  test('U10 历史显示删除与未知事件，不显示切换revision，空备注统一', async ({ page }) => {
    const { mock } = await fixture(page)
    mock.overrides.set('release/history', route => ok(route, { items: ['delete', 'other', 'constructor'].map((kind, i) => ({
      event_id: String(i), event_type: kind, version: 'v2', reason: '', actor: 'admin', request_id: 'req', created_at: '2026-10-20T00:00:00Z',
    })), has_more: false }))
    await page.getByRole('button', { name: '操作记录', exact: true }).click()
    await expect(page.getByRole('dialog')).toContainText('删除版本')
    await expect(page.getByRole('dialog')).toContainText('未知操作')
    await expect(page.getByRole('dialog').getByText('未知操作', { exact: true })).toHaveCount(2)
    await expect(page.getByRole('dialog')).toContainText('未填写备注')
    await expect(page.getByRole('dialog')).not.toContainText('revision')
  })
})
