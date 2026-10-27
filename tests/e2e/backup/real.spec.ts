import { test, expect } from '@playwright/test'
import { spawn } from 'node:child_process'
import { join } from 'node:path'
import { writeFile } from 'node:fs/promises'
import JSZip from 'jszip'

test('临时SQLite实例：UI备份下载→预览取消→连续两次确认、重启重登恢复', async ({ page, baseURL }) => {
  const username = process.env.RB_QA_ADMIN_USERNAME!
  const password = process.env.RB_QA_ADMIN_PASSWORD!
  const instance = process.env.RB_BACKUP_UI_INSTANCE!
  if (!username || !password || !instance) throw new Error('Isolated credentials and instance required')
  const endpoint = (path: string) => new URL('api/v1/' + path, baseURL).href
  async function login() {
    await page.getByLabel('账号', { exact: true }).fill(username)
    await page.getByLabel('密码', { exact: true }).fill(password)
    await page.getByRole('button', { name: '登录', exact: true }).click()
    await expect(page.getByRole('heading', { name: '备份与恢复', exact: true })).toBeVisible()
  }
  async function me() {
    const response = await page.request.get(endpoint('auth/me'))
    expect(response.status()).toBe(200)
    return (await response.json()).data
  }
  let identity: Awaited<ReturnType<typeof me>>
  async function post(path: string, data: unknown) {
    const response = await page.request.post(endpoint(path), { data, headers: { Origin: new URL(baseURL!).origin, 'X-CSRF-Token': identity.csrf_token } })
    const body = await response.json()
    if (!response.ok() || ![0,200,202].includes(body.code) || (body.operate_response && ![0,200].includes(body.operate_response.code))) throw new Error(`${path}: HTTP ${response.status()}, code ${body.code}, ${body.error_type}`)
    return body.data
  }
  await page.goto('backups')
  await login()
  identity = await me()
  await expect(page.getByTestId('rollback-retention')).toContainText('最近 3 次恢复前的数据')
  await expect(page.getByRole('button', { name: '创建备份', exact: true })).toBeEnabled()
  const env = 'backup_ui_qa'
  await post('environment/create', { environment: { environment_key: env, environment_name: '备份前端验收', is_active: true } })
  const environment = (await (await page.request.get(endpoint('runtime/overview'))).json()).data.list.find((item: { environment_key: string }) => item.environment_key === env)
  const createdPipeline = await post('pipeline/create', { expected_environment_id: environment.environment_id, environment_key: env, pipeline: { pipeline_key: 'web', pipeline_name: '网页', is_active: true } })
  const scopeId = createdPipeline.pipeline.scope_id
  expect(scopeId).toBeTruthy()
  await post('config/create', { expected_scope_id: scopeId, config: { environment_key: env, pipeline_key: 'web', alias: 'BACKUP_UI_SENTINEL', name: '恢复核对', type: 'object', content: '{"value":"before-backup","integer":900719925474099312345}' } })
  const listUrl = endpoint('config/list') + '?' + new URLSearchParams({ environment_key: env, pipeline_key: 'web', scope_id: scopeId, page: '1', size: '100' })
  async function config() { return (await (await page.request.get(listUrl)).json()).data.list.find((item: { alias: string }) => item.alias === 'BACKUP_UI_SENTINEL') }
  const beforeConfig = await config()
  const original = beforeConfig.content
  const changed = '{"value":"changed-after-backup"}'
  expect(original).toContain('900719925474099312345')
  // An unreferenced original asset must also survive the backup.
  const asset = await page.request.post(endpoint('asset/upload'), { headers: { Origin: new URL(baseURL!).origin, 'X-CSRF-Token': identity.csrf_token }, multipart: {
    environment_key: env, pipeline_key: 'web', expected_scope_id: scopeId, file: { name: 'backup-ui.txt', mimeType: 'text/plain', buffer: Buffer.from('backup-ui-resource') },
  } })
  expect(asset.status()).toBe(200)
  const assetId = (await asset.json()).data.asset.file_id
  await page.getByRole('button', { name: '创建备份', exact: true }).click()
  await page.getByLabel('备注（选填）').fill('前端真实闭环')
  const createResponse = page.waitForResponse(response => response.url().endsWith('/backup/create'))
  await page.getByRole('button', { name: '开始备份', exact: true }).click()
  expect((await createResponse).status()).toBe(202)
  await expect(page.getByRole('heading', { name: '备份完成', exact: true })).toBeVisible({ timeout: 60000 })
  const downloadPromise = page.waitForEvent('download', { timeout: 15000 })
  const downloadResponsePromise = page.waitForResponse(response => response.url().includes('/backup/download?'))
  await page.getByRole('button', { name: /^下载备份 / }).click()
  const downloadResponse = await downloadResponsePromise
  expect(downloadResponse.status()).toBe(200)
  expect(downloadResponse.headers()['x-rainbow-backup-sha256']).toMatch(/^[a-f0-9]{64}$/)
  const download = await downloadPromise
  const packagePath = join(instance, 'frontend-downloaded.zip')
  await download.saveAs(packagePath)
  const zip = await JSZip.loadAsync(await (await import('node:fs/promises')).readFile(packagePath))
  expect(zip.file('manifest.json')).not.toBeNull()
  await post('config/update', { expected_scope_id: scopeId, config: { ...beforeConfig, content: changed }, expected_edit_revision: beforeConfig.edit_revision })
  expect((await config()).content).toBe(changed)

  await page.getByLabel('业务备份包', { exact: true }).setInputFiles(packagePath)
  await page.getByRole('button', { name: '上传并校验', exact: true }).click()
  await expect(page.getByText('校验通过', { exact: true })).toBeVisible({ timeout: 60000 })
  async function prepare() {
    await page.getByRole('button', { name: '核对并恢复', exact: true }).click()
    await page.getByRole('checkbox', { name: '我已核对预览，确认覆盖全部业务数据' }).check()
    const execute = page.waitForResponse(response => response.url().endsWith('/backup/restore/execute'))
    await page.getByRole('button', { name: '确认覆盖并准备恢复', exact: true }).click()
    const response = await execute
    expect(response.status()).toBe(202)
    const jobId = (await response.json()).data.job.job_id
    await expect(page.getByRole('heading', { name: '等待服务重启', exact: true })).toBeVisible({ timeout: 60000 })
    return jobId
  }
  await prepare()
  expect((await config()).content).toBe(changed)
  const blocked = await page.request.post(endpoint('config/update'), { headers: { Origin: new URL(baseURL!).origin, 'X-CSRF-Token': identity.csrf_token }, data: { config: { ...beforeConfig, content: 'blocked' }, expected_edit_revision: beforeConfig.edit_revision } })
  expect(blocked.status()).toBe(503)
  expect((await blocked.json()).error_type).toBe('BACKUP_MAINTENANCE')
  await page.getByRole('button', { name: '取消待恢复计划', exact: true }).click()
  await page.getByRole('dialog').getByRole('button', { name: '取消恢复计划', exact: true }).click()
  await expect(page.getByRole('heading', { name: '恢复已取消' })).toBeVisible()
  expect((await config()).content).toBe(changed)
  const restoreJobId = await prepare()
  const oldCookies = await page.context().cookies()
  // Do not inherit capture pipes into the long-lived Windows server process.
  async function restart() { await new Promise<void>((resolve, reject) => {
    const child = spawn('powershell.exe', ['-NoProfile', '-File', join(process.cwd(), 'backup', 'instance.ps1'), '-Mode', 'Restart'], { windowsHide: true, stdio: 'ignore' })
    const timer = setTimeout(() => { child.kill(); reject(new Error('Temporary instance restart timed out')) }, 60000)
    child.once('error', error => { clearTimeout(timer); reject(error) })
    child.once('exit', code => { clearTimeout(timer); code === 0 ? resolve() : reject(new Error(`Temporary instance restart failed (${code})`)) })
  }) }
  await restart()
  await page.getByRole('button', { name: '查询恢复结果', exact: true }).click()
  await expect(page.getByRole('heading', { name: '登录工作台', exact: true })).toBeVisible()
  await login()
  identity = await me()
  await expect(page.getByRole('heading', { name: '恢复完成', exact: true })).toBeVisible({ timeout: 30000 })
  const restored = await config()
  expect(restored.content).toBe(original)
  expect(restored.edit_revision).not.toBe(beforeConfig.edit_revision)
  const assetResponse = await page.request.get(endpoint('asset/file/' + assetId))
  expect(assetResponse.status()).toBe(200)
  expect(await assetResponse.text()).toBe('backup-ui-resource')
  const oldSession = await page.request.get(endpoint('auth/me'), { headers: { Cookie: oldCookies.map(cookie => `${cookie.name}=${cookie.value}`).join('; ') } })
  expect(oldSession.status()).toBe(401)
  const status = await (await page.request.get(endpoint('backup/status') + '?job_id=' + restoreJobId)).json()
  expect(status.data.job.state).toBe('succeeded')
  // Successful retained copies must not add an operator cleanup prerequisite.
  await expect(page.getByLabel('业务备份包', { exact: true })).toBeEnabled()
  await expect(page.getByRole('heading', { name: '恢复预览', exact: true })).toHaveCount(0)
  await page.getByLabel('业务备份包', { exact: true }).setInputFiles(packagePath)
  await page.getByRole('button', { name: '上传并校验', exact: true }).click()
  await expect(page.getByText('校验通过', { exact: true })).toBeVisible({ timeout: 60000 })
  const secondRestoreJobId = await prepare()
  expect(secondRestoreJobId).not.toBe(restoreJobId)
  await restart()
  await page.getByRole('button', { name: '查询恢复结果', exact: true }).click()
  await expect(page.getByRole('heading', { name: '登录工作台', exact: true })).toBeVisible()
  await login()
  identity = await me()
  await expect(page.getByRole('heading', { name: '恢复完成', exact: true })).toBeVisible({ timeout: 30000 })
  const secondStatus = await (await page.request.get(endpoint('backup/status') + '?job_id=' + secondRestoreJobId)).json()
  expect(secondStatus.data.job.state).toBe('succeeded')
  expect((await config()).content).toBe(original)
  // No session, password, or raw configuration in the persisted evidence.
  await writeFile(join(instance, 'frontend-evidence.json'), JSON.stringify({ createHTTP: 202, restoreHTTP: 202, originalRestored: true, revisionChanged: true, originalAssetHTTP: 200, oldSessionHTTP: 401, restoreState: status.data.job.state, secondRestoreState: secondStatus.data.job.state, rollbackKeepCount: 3, backupEntries: Object.keys(zip.files).length, downloadContentType: downloadResponse.headers()['content-type'] }, null, 2))
})
