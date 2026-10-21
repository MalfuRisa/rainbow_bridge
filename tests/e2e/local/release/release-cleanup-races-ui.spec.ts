import { test, expect } from '@playwright/test'
import { fixture, row, openDelete, deletionResult, ok, fail, base } from './cleanup-fixture'
import { meta } from '../../auth/mock'

test.describe('版本清理乱序与确认 @release', () => {
  for (const operation of ['lock', 'unlock', 'create', 'activate']) {
    test('U02 四种备注输入保留500表情、拒绝501字 ' + operation, async ({ page }) => {
      const { mock } = await fixture(page)
      const endpoint = operation === 'lock' || operation === 'unlock' ? 'freeze/' + operation : 'release/' + operation
      if (operation === 'lock' || operation === 'unlock') {
        mock.locked = operation === 'unlock'
        await page.goto(base() + 'config?env=qa&pipeline=web')
        await page.getByRole('button', { name: operation === 'lock' ? '锁版' : '解除冻结', exact: true }).click()
      } else if (operation === 'create') {
        await page.getByRole('button', { name: '创建版本', exact: true }).click()
        await page.locator('#release-version').fill('v3')
      } else await row(page).getByRole('button', { name: '切换生效版本', exact: true }).click()
      const label = operation === 'lock' ? '确认锁定' : operation === 'unlock' ? '确认解除' : operation === 'create' ? '保存版本' : '切换生效版本'
      await page.getByLabel('备注（选填）').fill('中'.repeat(501))
      await page.getByRole('dialog').getByRole('button', { name: label, exact: true }).click()
      await expect(page.getByRole('dialog')).toContainText('备注最多500字')
      expect(mock.count(endpoint)).toBe(0)
      await page.getByLabel('备注（选填）').fill('😀'.repeat(500))
      await page.getByRole('dialog').getByRole('button', { name: label, exact: true }).click()
      await expect(page.getByRole('dialog')).toBeHidden()
      expect(Array.from(mock.writes(endpoint)[0].postDataJSON().reason).length).toBe(500)
    })
  }
  for (const [status, kind] of [[401, 'AUTH_REQUIRED'], [403, 'AUTH_CSRF_INVALID'], [403, 'AUTH_ORIGIN_DENIED']] as const) {
    test('U06 删除鉴权错误 ' + kind, async ({ page }) => {
      const { mock } = await fixture(page)
      mock.overrides.set('release/delete', route => fail(route, status, kind))
      await openDelete(page)
      await page.getByRole('dialog').getByRole('button', { name: '删除版本', exact: true }).click()
      if (status === 401) await expect(page.getByRole('heading', { name: '登录工作台', exact: true })).toBeVisible()
      else {
        await expect(page.getByRole('dialog').getByRole('alert')).toBeVisible()
        await expect(page.getByRole('button', { name: '确认结果', exact: true })).toHaveCount(0)
      }
      expect(mock.count('release/delete')).toBe(1)
      await expect(page.getByText('版本 v2 已删除', { exact: true })).toHaveCount(0)
    })
  }
  for (const endpoint of ['release/create', 'release/activate']) {
    test('U12 空备注和有备注变更作为新请求，同备注确认保持原payload ' + endpoint, async ({ page }) => {
      const { mock } = await fixture(page)
      mock.overrides.set(endpoint, route => fail(route, 503, 'RELEASE_BUSY'))
      if (endpoint.endsWith('create')) {
        await page.getByRole('button', { name: '创建版本', exact: true }).click()
        await page.locator('#release-version').fill('v3')
      } else await row(page).getByRole('button', { name: '切换生效版本', exact: true }).click()
      const button = page.getByRole('dialog').getByRole('button', { name: endpoint.endsWith('create') ? '保存版本' : '切换生效版本', exact: true })
      await button.click()
      await page.getByRole('button', { name: '确认结果', exact: true }).click()
      await expect.poll(() => mock.count(endpoint)).toBe(2)
      expect(mock.writes(endpoint)[0].postDataJSON()).toEqual(mock.writes(endpoint)[1].postDataJSON())
      await page.getByLabel('备注（选填）').fill('新备注')
      await expect(page.getByRole('button', { name: '确认结果', exact: true })).toHaveCount(0)
      await button.click()
      await expect.poll(() => mock.count(endpoint)).toBe(3)
      expect(mock.writes(endpoint)[2].postDataJSON().request_id).not.toBe(mock.writes(endpoint)[1].postDataJSON().request_id)
      expect(mock.writes(endpoint)[2].postDataJSON().reason).toBe('新备注')
      await page.getByLabel('备注（选填）').fill('')
      await button.click()
      await expect.poll(() => mock.count(endpoint)).toBe(4)
      expect(mock.writes(endpoint)[3].postDataJSON().reason).toBe('')
      expect(mock.writes(endpoint)[3].postDataJSON().request_id).not.toBe(mock.writes(endpoint)[2].postDataJSON().request_id)
    })
    test('410清理创建或切换确认，不继续使用被删除目标 ' + endpoint, async ({ page }) => {
      const { mock } = await fixture(page)
      mock.overrides.set(endpoint, route => fail(route, 410, 'RELEASE_VERSION_DELETED'))
      if (endpoint.endsWith('create')) {
        await page.getByRole('button', { name: '创建版本', exact: true }).click()
        await page.locator('#release-version').fill('v3')
      } else await row(page).getByRole('button', { name: '切换生效版本', exact: true }).click()
      const button = page.getByRole('dialog').getByRole('button', { name: endpoint.endsWith('create') ? '保存版本' : '切换生效版本', exact: true })
      await button.click()
      await expect(page.getByRole('dialog')).toContainText('该版本已被删除')
      await expect(button).toBeDisabled()
      await expect(page.getByTestId('restore-pending')).toHaveCount(0)
    })
  }
  test('U08 A→B→A旧删除完成不能关闭新弹窗或替换备注', async ({ page }) => {
    const { mock } = await fixture(page)
    let release!: () => void
    const gate = new Promise<void>(resolve => { release = resolve })
    let delivered = false
    mock.overrides.set('release/delete', async route => {
      const payload = route.request().postDataJSON()
      await gate
      await ok(route, deletionResult(payload))
      delivered = true
    })
    await openDelete(page)
    await page.getByLabel('备注（选填）').fill('旧操作')
    await page.getByRole('dialog').getByRole('button', { name: '删除版本', exact: true }).click()
    await expect.poll(() => mock.count('release/delete')).toBe(1)
    await page.getByRole('button', { name: '取消', exact: true }).click()
    await expect(page.getByRole('dialog')).toBeHidden()
    // Change the scope on the same mounted component, then come back to A.
    for (const scope of ['other', 'qa']) {
      await page.getByRole('combobox', { name: '环境', exact: true }).click()
      await page.getByRole('option', { name: scope === 'other' ? '另一环境 (other)' : '演示环境 (qa)', exact: true }).click()
      await page.getByRole('combobox', { name: '渠道', exact: true }).click()
      await page.getByRole('option', { name: '网页渠道 (web)', exact: true }).click()
    }
    // Closing and reopening alone also creates a distinct operation generation.
    await openDelete(page)
    await page.getByLabel('备注（选填）').fill('新操作备注')
    release()
    await expect.poll(() => delivered).toBe(true)
    await expect(page.getByRole('dialog')).toBeVisible()
    await expect(page.getByLabel('备注（选填）')).toHaveValue('新操作备注')
    expect(mock.writes('release/delete')[0].postDataJSON().expected_snapshot_id).toBe('s2')
  })
  test('U09 放回关闭前旧详情不能覆盖新410空状态', async ({ page }) => {
    const { mock } = await fixture(page)
    let release!: () => void
    const gate = new Promise<void>(resolve => { release = resolve })
    let call = 0; let delivered = false
    mock.overrides.set('release/detail', async route => {
      call++
      if (call === 1) {
        await gate
        await ok(route, { meta, configs: [{ name: '旧私有内容', resource_key: 'old', content: 'OLD_PRIVATE', type: 'text' }], assets: [] })
        delivered = true
      } else await fail(route, 410, 'RELEASE_VERSION_DELETED')
    })
    await row(page).getByRole('button', { name: '查看', exact: true }).click()
    await expect.poll(() => call).toBe(1)
    await page.keyboard.press('Escape')
    await expect(page.getByRole('dialog')).toBeHidden()
    await row(page).getByRole('button', { name: '查看', exact: true }).click()
    await expect(page.getByRole('dialog')).toContainText('该版本已被删除')
    release()
    await expect.poll(() => delivered).toBe(true)
    await expect(page.getByRole('dialog')).not.toContainText('OLD_PRIVATE')
    await expect(page.getByRole('dialog')).toContainText('该版本已被删除')
  })
  test('U07 删除刷新作废旧分页，分页迟到不恢复已删版本', async ({ page }) => {
    const { mock, state } = await fixture(page)
    let deleteRelease!: () => void; let pageRelease!: () => void
    const deleteGate = new Promise<void>(resolve => { deleteRelease = resolve })
    const pageGate = new Promise<void>(resolve => { pageRelease = resolve })
    let held = false; let delivered = false
    mock.overrides.set('release/list', async route => {
      if (new URL(route.request().url()).searchParams.get('before_id')) {
        held = true; await pageGate
        await ok(route, { items: [{ meta: { ...meta, version: 'v2', snapshot_id: 's2' }, is_active: false }], has_more: false })
        delivered = true
      } else await ok(route, { items: state.items, active: state.active, has_more: !state.deleted, next_before_id: 'old-page' })
    })
    mock.overrides.set('release/delete', async route => {
      await deleteGate; state.deleted = true
      state.items = state.items.filter(item => item.meta.version !== 'v2')
      await ok(route, deletionResult(route.request().postDataJSON()))
    })
    await openDelete(page)
    await page.getByRole('dialog').getByRole('button', { name: '删除版本', exact: true }).click()
    await page.getByRole('button', { name: '取消', exact: true }).click()
    await expect(page.getByRole('dialog')).toBeHidden()
    await page.getByRole('button', { name: '加载更多', exact: true }).click()
    await expect.poll(() => held).toBe(true)
    deleteRelease()
    await expect(row(page)).toHaveCount(0)
    pageRelease()
    await expect.poll(() => delivered).toBe(true)
    await expect(row(page)).toHaveCount(0)
    await expect(row(page, 'v1')).toBeVisible()
  })
})
