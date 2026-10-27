import { test, expect } from '@playwright/test'
import { join } from 'node:path'
import { mkdir, readFile, writeFile } from 'node:fs/promises'
import { gunzipSync } from 'node:zlib'
import JSZip from 'jszip'

test('隔离实例前端：编辑区C / 生效v1=A / 未生效v2=B，导出与指定目标导入', async ({ page, baseURL }) => {
  const instance = process.env.RB_SCOPE_UI_INSTANCE!
  const endpoint = (path: string) => new URL('api/v1/' + path, baseURL).href
  async function capture(name: string) {
    const folder = process.env.RB_QA_VISUAL_DIR
    if (!folder) return
    await mkdir(folder, { recursive: true })
    for (const width of [1280, 1440]) for (const dark of [false, true]) {
      await page.setViewportSize({ width, height: 1000 })
      await page.evaluate(value => document.documentElement.classList.toggle('dark', value), dark)
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
      await page.screenshot({ path: join(folder, `${name}-${width}-${dark ? 'dark' : 'light'}.png`), fullPage: true, animations: 'disabled' })
    }
    await page.setViewportSize({ width: 1440, height: 1000 })
    await page.evaluate(() => document.documentElement.classList.remove('dark'))
  }
  await page.goto('import-export')
  await page.getByLabel('账号', { exact: true }).fill(process.env.RB_QA_ADMIN_USERNAME!)
  await page.getByLabel('密码', { exact: true }).fill(process.env.RB_QA_ADMIN_PASSWORD!)
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await expect(page.getByRole('heading', { name: '导入导出', exact: true })).toBeVisible()
  const identity = (await (await page.request.get(endpoint('auth/me'))).json()).data
  const headers = { Origin: new URL(baseURL!).origin, 'X-CSRF-Token': identity.csrf_token }
  async function post(path: string, data: unknown) {
    const response = await page.request.post(endpoint(path), { data, headers })
    const body = await response.json()
    if (!response.ok() || ![0,200].includes(body.code)) throw new Error(`${path}: HTTP ${response.status()}, ${body.error_type || body.code}`)
    return body.data
  }
  async function get(path: string) { return (await (await page.request.get(endpoint(path))).json()).data }
  await post('environment/create', { environment: { environment_key: 'ui_source', environment_name: '界面来源', is_active: true } })
  await post('environment/create', { environment: { environment_key: 'ui_target', environment_name: '界面目标', is_active: true } })
  const overview = await get('runtime/overview')
  const sourceEnv = overview.list.find((item: { environment_key: string }) => item.environment_key === 'ui_source')
  const source = sourceEnv.pipelines.find((item: { pipeline_key: string }) => item.pipeline_key === 'default')
  const scope = { environment_key: 'ui_source', pipeline_key: 'default', expected_scope_id: source.scope_id }
  if (!source.scope_id || !sourceEnv.environment_id) throw new Error('Target instance does not expose the new scope identity contract')
  const resource = await page.request.post(endpoint('asset/upload'), { headers, multipart: {
    environment_key: 'ui_source', pipeline_key: 'default', expected_scope_id: source.scope_id,
    file: { name: 'proof.txt', mimeType: 'text/plain', buffer: Buffer.from('UI-RESOURCE-A') },
  } })
  expect(resource.status()).toBe(200)
  const fileId = (await resource.json()).data.asset.file_id
  const created = await post('config/create', { expected_scope_id: source.scope_id, config: { ...scope, alias: 'UI_VALUE', name: '界面核对', type: 'text', content: 'A' } })
  await post('config/create', { expected_scope_id: source.scope_id, config: { ...scope, alias: 'UI_FILE', name: '界面资源', type: 'file', content: 'asset://' + fileId } })
  let value = created.config
  async function freeze(lock: boolean) {
    const status = await get('freeze/status?' + new URLSearchParams({ environment_key: 'ui_source', pipeline_key: 'default', scope_id: source.scope_id }))
    await post('freeze/' + (lock ? 'lock' : 'unlock'), { ...scope, expected_revision: status.revision, reason: '界面联调' })
    return await get('freeze/status?' + new URLSearchParams({ environment_key: 'ui_source', pipeline_key: 'default', scope_id: source.scope_id }))
  }
  async function update(content: string) {
    value = (await post('config/update', { expected_scope_id: source.scope_id, expected_edit_revision: value.edit_revision, config: { ...value, content } })).config
  }
  const locked = await freeze(true)
  const first = await post('release/create', { ...scope, expected_freeze_revision: locked.revision, request_id: crypto.randomUUID(), reason: '' })
  expect(first.meta.version).toBe('v1')
  await post('release/activate', { ...scope, version: 'v1', expected_active_revision: '0', request_id: crypto.randomUUID(), reason: '' })
  await freeze(false); await update('B')
  const secondLocked = await freeze(true)
  const second = await post('release/create', { ...scope, expected_freeze_revision: secondLocked.revision, request_id: crypto.randomUUID(), reason: '' })
  expect(second.meta.version).toBe('v2')
  await freeze(false); await update('C')
  // All destructive setup is confined to the fresh instance created by the runner.
  await page.goto('import-export')
  await page.getByRole('checkbox', { name: '选择环境 界面来源', exact: true }).check()
  async function download(name: string, expected: string) {
    const waiting = page.waitForEvent('download')
    await page.getByRole('button', { name: '导出数据', exact: true }).click()
    const file = await waiting
    const path = join(instance, name + '.zip'); await file.saveAs(path)
    const archive = await JSZip.loadAsync(await readFile(path))
    const metadata = JSON.parse(await archive.file('configs.json')!.async('string'))
    expect(metadata.business_configs.find((item: { alias: string }) => item.alias === 'UI_VALUE').content).toBe(expected)
    expect(metadata.archive_format).toBe('rainbow-transfer-v2')
    expect(file.suggestedFilename()).toContain('rainbow-transfer-')
    const resourceEntry = Object.values(archive.files).find(entry => entry.name.startsWith('files/') && !entry.dir)!
    expect(await resourceEntry.async('string')).toBe('UI-RESOURCE-A')
    return { path, metadata }
  }
  const editor = await download('editor-C', 'C')
  await page.getByRole('button', { name: /^当前生效版本 当前/ }).click()
  await page.getByRole('combobox', { name: '环境', exact: true }).click()
  await page.getByRole('option', { name: '界面来源 (ui_source)', exact: true }).click()
  const active = await download('active-A', 'A')
  expect(active.metadata.export_source.scopes[0].snapshot_id).toBe(first.meta.snapshot_id)
  await page.getByRole('button', { name: /^指定发布版本 选择/ }).click()
  await page.getByRole('combobox', { name: '发布版本', exact: true }).click()
  await page.getByRole('option', { name: 'v2', exact: true }).click()
  const version = await download('version-B', 'B')
  expect(version.metadata.export_source.scopes[0].snapshot_id).toBe(second.meta.snapshot_id)
  await page.getByRole('button', { name: 'TAR.GZ', exact: true }).click()
  const tarWaiting = page.waitForEvent('download')
  await page.getByRole('button', { name: '导出数据', exact: true }).click()
  const tar = await tarWaiting; const tarPath = join(instance, 'version-B.tar.gz'); await tar.saveAs(tarPath)
  expect(gunzipSync(await readFile(tarPath)).includes(Buffer.from('configs.json'))).toBe(true)
  await page.getByRole('tab', { name: '数据导入', exact: true }).click()
  await page.getByRole('button', { name: '指定目标渠道', exact: true }).click()
  await page.getByRole('combobox', { name: '环境', exact: true }).click()
  await page.getByRole('option', { name: '界面目标 (ui_target)', exact: true }).click()
  await page.locator('#import-file').setInputFiles(version.path)
  await expect(page.getByText('文件中有', { exact: false })).toHaveCount(0)
  await expect(page.getByRole('button', { name: '开始导入', exact: true })).toBeEnabled()
  await capture('import-target-real')
  await page.getByRole('button', { name: '开始导入', exact: true }).click()
  await expect(page.getByText('导入完成，资源恢复完整', { exact: true })).toBeVisible()
  const targetEnv = overview.list.find((item: { environment_key: string }) => item.environment_key === 'ui_target')
  const targetId = targetEnv.pipelines[0].scope_id
  const query = new URLSearchParams({ environment_key: 'ui_target', pipeline_key: 'default', scope_id: targetId })
  const imported = await get('config/list?' + query)
  expect(imported.list.find((item: { alias: string }) => item.alias === 'UI_VALUE').content).toBe('B')
  const targetActive = await get('release/active?' + query)
  expect(targetActive.active).toBeNull()
  expect((await get('release/list?' + query)).items).toHaveLength(0)
  expect((await get('freeze/status?' + query)).state).toBe('unlocked')
  const importedReference = imported.list.find((item: { alias: string }) => item.alias === 'UI_FILE').content
  const importedFile = /\/asset\/file\/([^?#]+)/.exec(importedReference)?.[1] || importedReference.replace('asset://', '')
  expect(importedFile).not.toBe(fileId)
  const restoredAsset = await page.request.get(endpoint('asset/file/' + importedFile))
  expect(restoredAsset.status()).toBe(200); expect(await restoredAsset.text()).toBe('UI-RESOURCE-A')
  // Exercise the actual management components with identities, not just setup APIs.
  await page.goto('config?env=ui_target&pipeline=default')
  await page.getByRole('button', { name: '编辑 界面核对', exact: true }).click()
  await expect(page.getByRole('dialog').locator('#content')).toHaveValue('B')
  await page.getByRole('dialog').locator('#content').fill('UI-EDITED')
  const editResponse = page.waitForResponse(response => response.url().endsWith('/config/update'))
  await page.getByRole('dialog').getByRole('button', { name: '保存', exact: true }).click()
  const edit = await editResponse
  expect(edit.status()).toBe(200)
  expect(edit.request().postDataJSON().expected_scope_id).toBe(targetId)
  expect(edit.request().postDataJSON().expected_edit_revision).toBeTruthy()
  await expect(page.getByRole('dialog')).toBeHidden()
  await page.getByRole('button', { name: '锁版', exact: true }).click()
  await page.getByRole('dialog').getByRole('button', { name: '确认锁定', exact: true }).click()
  await expect(page.getByRole('dialog')).toBeHidden()
  await page.goto('releases?env=ui_target&pipeline=default')
  await page.getByRole('button', { name: '创建版本', exact: true }).click()
  await expect(page.getByRole('dialog').getByLabel('版本号', { exact: true })).toHaveCount(0)
  await page.getByRole('dialog').getByRole('button', { name: '保存版本', exact: true }).click()
  await expect(page.getByRole('dialog')).toBeHidden()
  await page.getByRole('button', { name: '切换生效版本', exact: true }).click()
  await page.getByRole('dialog').getByRole('button', { name: '确认生效 v1', exact: true }).click()
  await expect(page.getByRole('dialog').getByRole('heading', { name: '版本 v1 已生效', exact: true })).toBeVisible()
  await page.getByRole('dialog').getByRole('button', { name: '完成', exact: true }).click()
  await expect(page.getByRole('dialog')).toBeHidden()
  expect((await get('release/active?' + query)).active.version).toBe('v1')
  await page.goto('environments')
  await page.locator('.spectrum-env-select').filter({ hasText: '界面来源' }).click()
  await page.getByRole('button', { name: '删除渠道 默认渠道', exact: true }).click()
  await capture('delete-scope-real')
  await page.getByRole('dialog').getByRole('button', { name: '确认删除', exact: true }).click()
  await expect(page.getByRole('dialog')).toBeHidden()
  await expect(page.getByText('暂无渠道，请添加', { exact: true })).toBeVisible()
  await page.getByRole('button', { name: '新增渠道', exact: true }).click()
  await page.locator('#pipeline-key').fill('default'); await page.locator('#pipeline-name').fill('默认渠道')
  await page.getByRole('dialog').getByRole('button', { name: '保存', exact: true }).click()
  await expect(page.getByRole('dialog')).toBeHidden()
  const rebuilt = (await get('runtime/overview')).list.find((item: { environment_key: string }) => item.environment_key === 'ui_source').pipelines[0]
  expect(rebuilt.scope_id).not.toBe(source.scope_id)
  const rebuiltQuery = new URLSearchParams({ environment_key: 'ui_source', pipeline_key: 'default', scope_id: rebuilt.scope_id })
  expect((await get('config/list?' + rebuiltQuery)).list || []).toHaveLength(0)
  expect((await get('release/list?' + rebuiltQuery)).items || []).toHaveLength(0)
  await page.goto('config?env=ui_source&pipeline=default')
  await expect(page.getByRole('heading', { name: '当前环境和渠道下暂无配置', exact: true })).toBeVisible()
  await writeFile(join(instance, 'frontend-evidence.json'), JSON.stringify({ editor: 'C', active: 'A', version: 'B', sourceKinds: [editor.metadata.export_source.kind, active.metadata.export_source.kind, version.metadata.export_source.kind], importedEditor: 'B', importedActive: null, importedVersions: 0, importedLocked: false, importedResource: 'verified', tarGzipDownloaded: true, uiEditFreezeCreateActivate: true, uiUnlockedHistoricalScopeDeleteRecreate: true }, null, 2))
})
