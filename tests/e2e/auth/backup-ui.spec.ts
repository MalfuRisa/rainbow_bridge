import { test, expect, type Page } from '@playwright/test'
import { mkdir } from 'node:fs/promises'
import { join } from 'node:path'
import { backupFixture, backupItem, accepted, job, preview, packageFile, ok } from './backup-fixture'
import { fail, CSRF, login } from './mock'

async function upload(page: Page) {
  await page.getByLabel('业务备份包', { exact: true }).setInputFiles(packageFile)
  await page.getByRole('button', { name: '上传并校验', exact: true }).click()
  await expect(page.getByRole('heading', { name: '恢复预览', exact: true })).toBeVisible()
}
async function openRestore(page: Page) { await upload(page); await page.getByRole('button', { name: '核对并恢复', exact: true }).click() }
async function confirmRestore(page: Page) {
  await openRestore(page)
  await page.getByRole('checkbox', { name: '我已核对预览，确认覆盖全部业务数据' }).check()
  await page.getByRole('button', { name: '确认覆盖并准备恢复', exact: true }).click()
  await expect(page.getByRole('heading', { name: '等待服务重启', exact: true })).toBeVisible()
}

for (const conflict of [
  { kind: 'BACKUP_ROLLBACK_REVIEW_REQUIRED', message: 'rollback review required', expected: '恢复前的数据副本存在异常或安全状态无法确认' },
  { kind: 'BACKUP_RESTORE_PENDING', message: 'restore pending', expected: '已有恢复计划等待重启' },
  { kind: 'BACKUP_STATE_CONFLICT', message: 'an earlier rollback copy must be reviewed before another restore', expected: '恢复前的数据副本存在异常或安全状态无法确认' },
  { kind: 'BACKUP_STATE_CONFLICT', message: 'another restore plan is already waiting for a restart', expected: '已有恢复计划等待重启' },
]) test(`恢复拒绝说明真实处理方向：${conflict.kind} / ${conflict.message}`, async ({ page }) => {
  const { mock } = await backupFixture(page)
  mock.overrides.set('backup/restore/execute', route => route.fulfill({
    status: 409, contentType: 'application/json',
    body: JSON.stringify({ code: 409, error_type: conflict.kind, error: conflict.message, msg: conflict.message }),
  }))
  await page.goto('backups')
  await openRestore(page)
  await page.getByRole('checkbox', { name: '我已核对预览，确认覆盖全部业务数据' }).check()
  await page.getByRole('button', { name: '确认覆盖并准备恢复', exact: true }).click()
  const error = page.getByRole('dialog').getByRole('alert')
  await expect(error).toContainText(conflict.expected)
  await expect(error).not.toContainText('任务阶段已变化')
  await expect(page.getByRole('heading', { name: '等待服务重启', exact: true })).toHaveCount(0)
  expect(mock.count('backup/restore/execute')).toBe(1)
})

test('不支持部署保留原因和说明，禁用备份恢复且不请求不可用接口', async ({ page }) => {
  const { state, mock } = await backupFixture(page)
  state.supported = false
  await page.goto('backups')
  await expect(page.getByText('当前数据库不受支持，第一版仅支持 SQLite 文件库。', { exact: true })).toBeVisible()
  await expect(page.getByText('资源需使用本地存储。', { exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: '创建备份', exact: true })).toBeDisabled()
  await expect(page.getByLabel('业务备份包', { exact: true })).toBeDisabled()
  expect(mock.count('backup/list')).toBe(0)
  expect(mock.count('backup/create')).toBe(0)
})

test('创建返回202后显示真实阶段，只提交一次并在成功后刷新列表', async ({ page }) => {
  const { mock, state } = await backupFixture(page)
  let release!: () => void
  const pending = new Promise<void>(resolve => { release = resolve })
  mock.overrides.set('backup/create', async route => { await pending; await accepted(route, state.currentJob) })
  await page.goto('backups')
  await expect(page.getByRole('heading', { name: '尚无业务备份' })).toBeVisible()
  await page.getByRole('button', { name: '创建备份', exact: true }).click()
  await page.getByLabel('备注（选填）').fill('测试备注')
  await page.getByRole('button', { name: '开始备份', exact: true }).click()
  await expect(page.getByRole('dialog').getByRole('button', { name: '关闭弹窗' })).toBeDisabled()
  await page.keyboard.press('Escape')
  await expect(page.getByRole('dialog')).toBeVisible()
  expect(mock.count('backup/create')).toBe(1)
  expect(mock.writes('backup/create')[0].postDataJSON().reason).toBe('测试备注')
  expect(mock.writes('backup/create')[0].headers()['x-csrf-token']).toBe(CSRF)
  release()
  await expect(page.getByRole('heading', { name: '备份等待处理' })).toBeVisible()
  state.currentJob = job('create', 'running', { phase: 'packaging' })
  await expect(page.getByText('正在打包，业务写入已恢复', { exact: true })).toBeVisible()
  state.items = [backupItem]
  state.currentJob = job('create', 'succeeded', { phase: 'completed' })
  await expect(page.getByRole('heading', { name: '备份完成', exact: true })).toBeVisible()
  await expect(page.getByText(backupItem.reason, { exact: true })).toBeVisible()
  expect(mock.count('backup/create')).toBe(1)
})

for (const stateName of ['failed', 'interrupted']) test(`HTTP200的任务${stateName}不显示成功`, async ({ page }) => {
  const { state } = await backupFixture(page)
  state.currentJob = job('create', stateName, { error: stateName === 'failed' ? { error_type: 'BACKUP_SPACE_INSUFFICIENT', msg: 'space' } : null })
  await page.goto('backups')
  await page.getByRole('button', { name: '创建备份', exact: true }).click()
  await page.getByRole('button', { name: '开始备份', exact: true }).click()
  await expect(page.getByRole('heading', { name: stateName === 'failed' ? '备份失败' : '备份已中断', exact: true })).toBeVisible()
  await expect(page.getByRole('heading', { name: '备份完成', exact: true })).toHaveCount(0)
})

test('上传顺序和直接preview信封正确，警告、全部数量与覆盖确认可见', async ({ page }) => {
  const { mock } = await backupFixture(page)
  await page.goto('backups')
  await openRestore(page)
  await expect(page.getByText('有 1 处外部资源引用不包含在包内，恢复后仍需外部地址可用。', { exact: true })).toBeVisible()
  const uploadRequest = mock.writes('backup/restore/preview')[0]
  expect(uploadRequest.postData()!.indexOf('name="request_id"')).toBeLessThan(uploadRequest.postData()!.indexOf('name="file"'))
  expect(uploadRequest.headers()['content-type']).toContain('boundary=')
  expect(uploadRequest.headers()['x-csrf-token']).toBe(CSRF)
  const dialog = page.getByRole('dialog')
  for (const text of ['覆盖全部配置、资源、发布版本及历史。', '保留当前管理员账号和密码。', '准备后暂停业务写入，需部署人员重启服务。', '恢复后旧会话失效，需要重新登录。']) await expect(dialog.getByText(text, { exact: true })).toBeVisible()
  await expect(dialog.getByRole('button', { name: '确认覆盖并准备恢复' })).toBeDisabled()
  expect(mock.count('backup/restore/execute')).toBe(0)
  await dialog.getByRole('checkbox').check()
  await dialog.getByRole('button', { name: '确认覆盖并准备恢复' }).click()
  await expect(page.getByRole('heading', { name: '等待服务重启' })).toBeVisible()
  expect(mock.writes('backup/restore/execute')[0].postDataJSON()).toMatchObject({ preview_id: preview.preview_id, package_sha256: preview.package_sha256,
    target_instance_id: preview.target_instance_id, confirmation: 'RESTORE_ALL_BUSINESS_DATA' })
  await expect(page.getByRole('heading', { name: '恢复完成', exact: true })).toHaveCount(0)
  await expect(page.locator('.backup-task .animate-spin')).toHaveCount(0)
  await page.getByRole('button', { name: '取消待恢复计划', exact: true }).click()
  await page.getByRole('dialog').getByRole('button', { name: '取消恢复计划', exact: true }).click()
  await expect(page.getByRole('heading', { name: '恢复已取消', exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: '创建备份', exact: true })).toBeEnabled()
})

for (const invalid of ['invalid', 'issues', 'expired']) test(`恢复预览${invalid}禁止执行`, async ({ page }) => {
  const { state, mock } = await backupFixture(page)
  if (invalid === 'invalid') state.preview.valid = false
  if (invalid === 'issues') state.preview.issues = ['database_integrity_check_failed']
  if (invalid === 'expired') state.preview.expires_at = '2000-01-01T00:00:00Z'
  await page.goto('backups')
  await upload(page)
  await expect(page.getByRole('button', { name: '核对并恢复', exact: true })).toBeDisabled()
  if (invalid === 'issues') await expect(page.getByText('业务数据完整性校验失败。', { exact: true })).toBeVisible()
  if (invalid === 'expired') await expect(page.getByText('预览已过期，请重新上传备份包。', { exact: true })).toBeVisible()
  expect(mock.count('backup/restore/execute')).toBe(0)
})

test('网络结果不确定时保留原request_id与备注，刷新后显式原样确认', async ({ page }) => {
  const { mock, state } = await backupFixture(page)
  mock.overrides.set('backup/create', route => route.abort())
  await page.goto('backups')
  await page.getByRole('button', { name: '创建备份', exact: true }).click()
  await page.getByLabel('备注（选填）').fill('原始备注')
  await page.getByRole('button', { name: '开始备份', exact: true }).click()
  await expect(page.getByRole('button', { name: '确认上次请求结果', exact: true })).toBeVisible()
  const payload = mock.writes('backup/create')[0].postDataJSON()
  await page.reload()
  await expect(page.getByRole('heading', { name: '上次操作结果尚未确认' })).toBeVisible()
  await expect(page.getByRole('button', { name: '创建备份', exact: true })).toBeDisabled()
  state.currentJob = job('create', 'succeeded')
  mock.overrides.set('backup/create', route => accepted(route, state.currentJob))
  await page.getByRole('button', { name: '确认上次请求结果', exact: true }).click()
  await expect(page.getByRole('heading', { name: '备份完成', exact: true })).toBeVisible()
  expect(mock.writes('backup/create')[1].postDataJSON()).toEqual(payload)
})

test('维护503保留备注，不清会话不伪报成功', async ({ page }) => {
  const { mock } = await backupFixture(page)
  mock.overrides.set('backup/create', route => fail(route, 503, 'BACKUP_MAINTENANCE'))
  await page.goto('backups')
  await page.getByRole('button', { name: '创建备份', exact: true }).click()
  await page.getByLabel('备注（选填）').fill('保留备注')
  await page.getByRole('button', { name: '开始备份', exact: true }).click()
  await expect(page.getByRole('dialog').getByRole('alert')).toContainText('当前输入已保留')
  await expect(page.getByLabel('备注（选填）')).toHaveValue('保留备注')
  await expect(page.getByRole('heading', { name: '备份完成', exact: true })).toHaveCount(0)
  await expect(page).toHaveURL(/\/backups$/)
})

test('占用409不重复排队，校验任务失败时不出现可执行预览', async ({ page }) => {
  const { mock, state } = await backupFixture(page)
  mock.overrides.set('backup/create', route => fail(route, 409, 'BACKUP_BUSY'))
  await page.goto('backups')
  await page.getByRole('button', { name: '创建备份', exact: true }).click()
  await page.getByRole('button', { name: '开始备份', exact: true }).click()
  await expect(page.getByRole('dialog').getByRole('alert')).toContainText('已有备份或恢复任务')
  expect(mock.count('backup/create')).toBe(1)
  await page.getByRole('dialog').getByRole('button', { name: '取消', exact: true }).click()
  state.currentJob = job('validate', 'running', { phase: 'validating_package', backup_id: null })
  mock.overrides.set('backup/restore/preview', route => accepted(route, state.currentJob))
  await page.getByLabel('业务备份包', { exact: true }).setInputFiles(packageFile)
  await page.getByRole('button', { name: '上传并校验', exact: true }).click()
  await expect(page.getByRole('heading', { name: '备份包校验处理中', exact: true })).toBeVisible()
  state.currentJob = job('validate', 'failed', { phase: 'failed', error: { error_type: 'BACKUP_PACKAGE_INVALID', msg: 'invalid' } })
  await expect(page.getByRole('heading', { name: '备份包校验失败', exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: '核对并恢复', exact: true })).toHaveCount(0)
  expect(mock.count('backup/restore/execute')).toBe(0)
})

test('二进制ZIP可下载，HTTP200的JSON错误或非ZIP文件不会触发下载', async ({ page }) => {
  const { state, mock } = await backupFixture(page)
  state.items = [backupItem]
  mock.overrides.set('backup/download', route => route.fulfill({ status: 200, contentType: 'application/octet-stream',
    headers: { 'Content-Disposition': 'attachment; filename="business-qa.zip"' }, body: Buffer.from([0x50,0x4b,3,4,0,0,0,0]) }))
  await page.goto('backups')
  const downloadEvent = page.waitForEvent('download')
  await page.getByRole('button', { name: /^下载备份 / }).click()
  expect((await downloadEvent).suggestedFilename()).toBe('business-qa.zip')
  let unexpected = 0
  page.on('download', () => unexpected++)
  for (const contentType of ['application/json', 'application/octet-stream']) {
    mock.overrides.set('backup/download', route => route.fulfill({ status: 200, contentType, body: '{"error":"not a ZIP"}' }))
    await page.getByRole('button', { name: /^下载备份 / }).click()
    await expect(page.getByRole('alert')).toContainText('不是备份 ZIP')
  }
  expect(unexpected).toBe(0)
})

test('恢复受理结果不确定后只原样确认，不再次要求或更改覆盖合同', async ({ page }) => {
  const { mock, state } = await backupFixture(page)
  mock.overrides.set('backup/restore/execute', route => route.abort())
  await page.goto('backups')
  await openRestore(page)
  await page.getByRole('checkbox').check()
  await page.getByLabel('备注（选填）').fill('恢复备注')
  await page.getByRole('button', { name: '确认覆盖并准备恢复' }).click()
  await expect(page.getByRole('button', { name: '确认上次请求结果', exact: true })).toBeVisible()
  const original = mock.writes('backup/restore/execute')[0].postDataJSON()
  state.currentJob = job('restore', 'restart_required', { phase: 'waiting_for_restart' })
  mock.overrides.set('backup/restore/execute', route => accepted(route, state.currentJob))
  await page.getByRole('button', { name: '确认上次请求结果', exact: true }).click()
  await expect(page.getByRole('heading', { name: '等待服务重启', exact: true })).toBeVisible()
  expect(mock.writes('backup/restore/execute')[1].postDataJSON()).toEqual(original)
})

test('下载错误不会产生ZIP，删除需确认摘要且异常不会假装删除', async ({ page }) => {
  const { state, mock } = await backupFixture(page)
  state.items = [backupItem]
  mock.overrides.set('backup/download', route => fail(route, 503, 'BACKUP_STORAGE_ERROR'))
  mock.overrides.set('backup/delete', route => fail(route, 409, 'BACKUP_IN_USE'))
  let downloads = 0
  page.on('download', () => downloads++)
  await page.goto('backups')
  await page.getByRole('button', { name: `下载备份 ${backupItem.backup_id}`, exact: true }).click()
  await expect(page.getByRole('alert')).toContainText('备份存储不可用')
  expect(downloads).toBe(0)
  await page.getByRole('button', { name: `删除备份 ${backupItem.backup_id}`, exact: true }).click()
  expect(mock.count('backup/delete')).toBe(0)
  await page.getByRole('button', { name: '确认删除备份', exact: true }).click()
  await expect(page.getByRole('dialog').getByRole('alert')).toContainText('暂时不能删除')
  expect(mock.writes('backup/delete')[0].postDataJSON()).toMatchObject({ backup_id: backupItem.backup_id, expected_sha256: backupItem.sha256 })
  mock.overrides.set('backup/delete', route => { state.items = []; return ok(route, { deleted: true }) })
  await page.getByRole('button', { name: '确认删除备份', exact: true }).click()
  await expect(page.getByRole('heading', { name: '尚无业务备份' })).toBeVisible()
})

test('恢复重启401后重新登录仍能查询真实终态，离开页面停止轮询', async ({ page }) => {
  const { state, mock } = await backupFixture(page)
  await page.goto('backups')
  await confirmRestore(page)
  mock.authenticated = false
  mock.overrides.set('backup/status', route => fail(route, 401, 'AUTH_REQUIRED'))
  await page.getByRole('button', { name: '查询恢复结果' }).click()
  await expect(page).toHaveURL(/login\?redirect=\/backups/)
  state.currentJob = job('restore', 'succeeded', { phase: 'committed' })
  mock.overrides.set('backup/status', route => ok(route, { job: state.currentJob }))
  await login(page)
  await expect(page.getByRole('heading', { name: '恢复完成', exact: true })).toBeVisible()
  await page.getByRole('link', { name: '项目概览', exact: true }).click()
  const before = mock.count('backup/status')
  await page.waitForTimeout(1800)
  expect(mock.count('backup/status')).toBe(before)
})

for (const keep of [1, 3, 20]) test(`按实际能力展示恢复前副本保留${keep}份，与备份包数量分开`, async ({ page }) => {
  const { state } = await backupFixture(page)
  state.retention = { managed: true, keep_count: keep }
  await page.goto('backups')
  await expect(page.getByTestId('rollback-retention')).toHaveText(`系统自动保留最近 ${keep} 次恢复前的数据，成功留存的副本不影响再次恢复。`)
  await page.getByText('备份与恢复须知', { exact: true }).click()
  await expect(page.getByText('服务器最多保留 10 份可下载的备份包。重要备份请下载另存。', { exact: true })).toBeVisible()
  await expect(page.getByText(/回退与清理需由部署人员处理/)).toHaveCount(0)
})

for (const policy of [undefined, { managed: false, keep_count: 3 }, { managed: true, keep_count: 0 }]) test(`缺少或无效托管策略不声称自动保留：${JSON.stringify(policy)}`, async ({ page }) => {
  const { state } = await backupFixture(page)
  state.retention = policy
  await page.goto('backups')
  await expect(page.getByTestId('rollback-retention')).toHaveCount(0)
})

test('成功恢复后清除旧预览，允许重新上传并准备下一次恢复', async ({ page }) => {
  const { state, mock } = await backupFixture(page)
  state.retention = { managed: true, keep_count: 3 }
  mock.overrides.set('backup/restore/preview', route => route.request().method() === 'POST'
    ? accepted(route, job('validate', 'succeeded', { job_id: crypto.randomUUID(), preview_id: preview.preview_id, phase: 'validated' }))
    : ok(route, state.preview))
  mock.overrides.set('backup/restore/execute', route => {
    state.currentJob = job('restore', 'restart_required', { job_id: crypto.randomUUID(), phase: 'waiting_for_restart' })
    return accepted(route, state.currentJob)
  })
  await page.goto('backups')
  await confirmRestore(page)
  state.currentJob = job('restore', 'succeeded', { job_id: state.currentJob.job_id, phase: 'committed' })
  await page.getByRole('button', { name: '查询恢复结果', exact: true }).click()
  await expect(page.getByRole('heading', { name: '恢复完成', exact: true })).toBeVisible()
  await expect(page.getByRole('heading', { name: '恢复预览', exact: true })).toHaveCount(0)
  await expect(page.getByLabel('业务备份包', { exact: true })).toBeEnabled()
  const prior = mock.writes('backup/restore/execute')[0].postDataJSON()
  await confirmRestore(page)
  expect(mock.writes('backup/restore/execute')[1].postDataJSON().request_id).not.toBe(prior.request_id)
  await expect(page.getByRole('heading', { name: '等待服务重启', exact: true })).toBeVisible()
})

test('备份桌面布局、预览与重启状态在1280/1440深浅主题可读', async ({ page }) => {
  test.setTimeout(60000)
  const { state } = await backupFixture(page)
  state.items = [backupItem]
  state.retention = { managed: true, keep_count: 3 }
  await page.goto('backups')
  await upload(page)
  for (const width of [1280, 1440]) for (const dark of [false, true]) {
    await page.setViewportSize({ width, height: 900 })
    await page.evaluate(value => document.documentElement.classList.toggle('dark', value), dark)
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    await expect(page.getByRole('link', { name: '备份恢复', exact: true })).toBeVisible()
    if (process.env.RB_QA_VISUAL_DIR) {
      await mkdir(process.env.RB_QA_VISUAL_DIR, { recursive: true })
      await page.evaluate(() => scrollTo({ top: 0, behavior: 'instant' }))
      await page.screenshot({ path: join(process.env.RB_QA_VISUAL_DIR, `backup-preview-${width}-${dark ? 'dark' : 'light'}.png`), fullPage: true, animations: 'disabled' })
    }
  }
  await page.getByRole('button', { name: '核对并恢复', exact: true }).click()
  await page.getByRole('checkbox').check()
  await page.getByRole('button', { name: '确认覆盖并准备恢复' }).click()
  await expect(page.getByRole('heading', { name: '等待服务重启' })).toBeVisible()
  if (process.env.RB_QA_VISUAL_DIR) {
    await page.evaluate(() => { (document.activeElement as HTMLElement | null)?.blur(); scrollTo({ top: 0, behavior: 'instant' }) })
    await page.mouse.move(0, 0)
    await page.screenshot({ path: join(process.env.RB_QA_VISUAL_DIR, 'backup-restart-required.png'), fullPage: true, animations: 'disabled' })
  }
})
