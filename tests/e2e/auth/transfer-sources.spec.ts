import { test, expect, type Page } from '@playwright/test'
import { mkdirSync } from 'node:fs'
import { mkdir } from 'node:fs/promises'
import { join, resolve } from 'node:path'
import { gzipSync, gunzipSync } from 'node:zlib'
import JSZip from 'jszip'
import { mockApi, ok, fail, meta, config, fixtureIdentity, CSRF } from './mock'

const downloads = resolve(process.cwd(), '../../output/transfer-ui-mock-downloads')
mkdirSync(downloads, { recursive: true })
test.use({ launchOptions: { downloadsPath: downloads } })
const scopeId = fixtureIdentity('qa', 'web')
const envId = fixtureIdentity('qa')
const snapshots = { v1: fixtureIdentity('snap-v1'), v2: fixtureIdentity('snap-v2') }
async function setup(page: Page) {
  const mock = await mockApi(page, true)
  const state = { active: 'v1' as 'v1' | 'v2', downloadError: '', previewError: '', noActive: false, huge: false }
  mock.overrides.set('release/active', route => ok(route, { active_revision: state.noActive ? '0' : '2', active: state.noActive ? null : { snapshot_id: snapshots[state.active], version: state.active, revision: '2', scope_id: scopeId } }))
  mock.overrides.set('release/list', route => ok(route, { items: ['v2', 'v1'].map(version => ({ meta: { ...meta, version, snapshot_id: snapshots[version as 'v1' | 'v2'], scope_id: scopeId }, is_active: version === state.active })), active: null, has_more: false }))
  mock.overrides.set('transfer/export-preview', route => {
    if (state.previewError) return fail(route, 503, state.previewError)
    const body = route.request().postDataJSON()
    return ok(route, { resolved_source: { kind: body.source, captured_at: '2026-10-26T00:00:00Z', scope_id: body.source === 'editor' ? null : scopeId,
      snapshot_id: body.expected_snapshot_id, version: body.source === 'active' ? state.active : body.version,
      scopes: [{ environment_key: 'qa', environment_id: envId, pipeline_key: 'web', scope_id: scopeId }] },
      counts: { configs: 1, assets: 1, bytes: state.huge ? '900719925474099312345' : '1024' }, estimated_bytes: state.huge ? '900719925474099312345' : '1024', warnings: ['external_references_not_included:1'] })
  })
  mock.overrides.set('transfer/export', async route => {
    const body = route.request().postDataJSON()
    if (state.downloadError) return fail(route, state.downloadError === 'RELEASE_VERSION_DELETED' ? 410 : state.downloadError === 'TRANSFER_ACTIVE_CHANGED' ? 409 : 503, state.downloadError)
    if (body.source === 'active' && body.expected_snapshot_id !== snapshots[state.active]) return fail(route, 409, 'TRANSFER_ACTIVE_CHANGED')
    const version = body.source === 'active' ? state.active : body.version
    const value = body.source === 'editor' ? 'C' : version === 'v1' ? 'A' : 'B'
    const content = JSON.stringify({ archive_format: 'rainbow-transfer-v2', export_source: { kind: body.source }, business_configs: [{ ...config, content: value }] })
    const binary = body.format === 'tar.gz' ? gzipSync(content) : await new JSZip().file('configs.json', content).generateAsync({ type: 'nodebuffer' })
    return route.fulfill({ contentType: body.format === 'tar.gz' ? 'application/gzip' : 'application/x-zip-compressed', headers: {
      'Content-Disposition': `attachment; filename="server-${body.source}-${version || 'editor'}.${body.format}"`,
      'X-Rainbow-Export-Source': body.source, ...(body.source === 'editor' ? {} : { 'X-Rainbow-Snapshot-Id': body.expected_snapshot_id, 'X-Rainbow-Version': version }),
    }, body: binary })
  })
  return { mock, state }
}
async function choose(page: Page, source: 'editor' | 'active' | 'version') {
  const names = { editor: /^编辑区 尚未/, active: /^当前生效版本 当前/, version: /^指定发布版本 选择/ }
  await page.getByRole('button', { name: names[source] }).click()
  if (source === 'editor') await page.getByRole('checkbox', { name: '选择环境 演示环境', exact: true }).check()
  if (source === 'version') {
    await page.getByRole('combobox', { name: '发布版本', exact: true }).click()
    await page.getByRole('option', { name: 'v2', exact: true }).click()
  }
  await expect(page.getByRole('button', { name: '导出数据', exact: true })).toBeEnabled()
}
async function download(page: Page, tar = false) {
  const waiting = page.waitForEvent('download')
  await page.getByRole('button', { name: '导出数据', exact: true }).click()
  const file = await waiting
  const stream = await file.createReadStream()
  if (!stream) throw new Error('No downloaded archive')
  const chunks: Buffer[] = []; for await (const chunk of stream) chunks.push(Buffer.from(chunk))
  const bytes = Buffer.concat(chunks)
  const content = tar ? gunzipSync(bytes).toString() : await (await JSZip.loadAsync(bytes)).file('configs.json')!.async('string')
  return { file, content: JSON.parse(content), bytes }
}

for (const [source, value] of [['editor', 'C'], ['active', 'A'], ['version', 'B']] as const) {
  test('三种来源取各自内容且预览下载参数一致：' + source, async ({ page }) => {
    const { mock } = await setup(page)
    await page.goto('import-export'); await choose(page, source)
    const result = await download(page)
    expect(result.content.business_configs[0].content).toBe(value)
    expect(result.content.export_source.kind).toBe(source)
    expect(result.file.suggestedFilename()).toMatch(new RegExp('^server-' + source))
    const preview = mock.writes('transfer/export-preview').at(-1)!.postDataJSON()
    const actual = mock.writes('transfer/export')[0].postDataJSON()
    expect(actual).toEqual(preview)
    if (source === 'editor') expect(actual.selections[0].expected_environment_id).toBe(envId)
    else { expect(actual.expected_scope_id).toBe(scopeId); expect(actual.expected_snapshot_id).toBe(snapshots[source === 'active' ? 'v1' : 'v2']) }
    if (source === 'active') expect(actual).not.toHaveProperty('version')
    expect(mock.writes('transfer/export')[0].headers()['x-csrf-token']).toBe(CSRF)
  })
}

test('生效版本切换后不自动改目标下载，刷新核对后才能再次导出', async ({ page }) => {
  const { mock, state } = await setup(page)
  await page.goto('import-export'); await choose(page, 'active')
  state.active = 'v2'
  let downloads = 0; page.on('download', () => downloads++)
  await page.getByRole('button', { name: '导出数据', exact: true }).click()
  await expect(page.getByRole('alert').filter({ hasText: '生效版本已变化' })).toBeVisible()
  expect(downloads).toBe(0); expect(mock.writes('transfer/export')).toHaveLength(1)
  await page.getByRole('button', { name: '刷新并核对来源', exact: true }).click()
  await expect(page.getByRole('button', { name: '导出数据', exact: true })).toBeEnabled()
  expect(mock.writes('transfer/export')).toHaveLength(1)
  const result = await download(page)
  expect(result.content.business_configs[0].content).toBe('B')
  expect(mock.writes('transfer/export')[1].postDataJSON().expected_snapshot_id).toBe(snapshots.v2)
})

test('无生效版本时不可导出，也不会回退编辑区', async ({ page }) => {
  const { mock, state } = await setup(page); state.noActive = true
  await page.goto('import-export')
  await page.getByRole('button', { name: /^当前生效版本 当前/ }).click()
  await expect(page.getByText('当前范围尚无生效版本，请先在版本发布中切换生效。')).toBeVisible()
  await expect(page.getByRole('button', { name: '导出数据', exact: true })).toBeDisabled()
  expect(mock.writes('transfer/export-preview')).toHaveLength(0)
  expect(mock.writes('transfer/export')).toHaveLength(0)
})

test('解析文件期间切换导入目标，旧预览不能恢复或提交到新目标', async ({ page }) => {
  const { mock } = await setup(page)
  mock.overrides.set('runtime/overview', route => ok(route, { list: [
    { environment_key: 'qa', environment_name: '演示环境', pipelines: [{ pipeline_key: 'web', pipeline_name: '网页渠道' }] },
    { environment_key: 'target', environment_name: '目标环境', pipelines: [{ pipeline_key: 'web', pipeline_name: '目标渠道' }] },
  ], total: 2 }))
  let release!: () => void
  const gate = new Promise<void>(resolve => { release = resolve })
  mock.overrides.set('transfer/import-preview', async route => {
    await gate
    await ok(route, { format: 'zip', environments: [{ environment_key: 'qa', environment_name: '演示环境', pipelines: [] }], summary: { total_configs: 1, conflict_count: 0 } })
  })
  await page.goto('import-export')
  await page.getByRole('tab', { name: '数据导入', exact: true }).click()
  await page.getByRole('button', { name: '指定目标渠道', exact: true }).click()
  await expect(page.getByRole('combobox', { name: '环境', exact: true })).toContainText('演示环境')
  await page.locator('#import-file').setInputFiles({ name: 'version.zip', mimeType: 'application/zip', buffer: Buffer.from('fixture') })
  await expect.poll(() => mock.count('transfer/import-preview')).toBe(1)
  await page.getByRole('combobox', { name: '环境', exact: true }).click()
  await page.getByRole('option', { name: '目标环境 (target)', exact: true }).click()
  release()
  await expect(page.getByText('导入目标已变化，请重新解析文件并核对。', { exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: '开始导入', exact: true })).toHaveCount(0)
  await expect(page.getByRole('button', { name: '重新解析文件', exact: true })).toBeEnabled()
  expect(mock.writes('transfer/import')).toHaveLength(0)
  expect(mock.writes('transfer/import-selective')).toHaveLength(0)
})

for (const kind of ['RELEASE_VERSION_DELETED', 'TRANSFER_RESOURCE_INCOMPLETE']) {
  test('版本删除或资源失败不产生假成功：' + kind, async ({ page }) => {
    const { state } = await setup(page)
    await page.goto('import-export'); await choose(page, 'version')
    state.downloadError = kind
    let downloads = 0; page.on('download', () => downloads++)
    await page.getByRole('button', { name: '导出数据', exact: true }).click()
    await expect(page.getByRole('alert')).toBeVisible()
    await expect(page.getByRole('button', { name: '导出数据', exact: true })).toBeDisabled()
    expect(downloads).toBe(0)
    await expect(page.getByText('已发起迁移包下载')).toHaveCount(0)
  })
}

test('字符串大小保留精度、TAR.GZ 按同一来源下载，桌面深浅主题可读', async ({ page }) => {
  const { mock, state } = await setup(page); state.huge = true
  await page.goto('import-export'); await choose(page, 'version')
  await expect(page.locator('.spectrum-export-counts')).toContainText('838860800000.0 GiB')
  for (const width of [1280, 1440]) for (const dark of [false, true]) {
    await page.setViewportSize({ width, height: 1000 })
    await page.evaluate(value => document.documentElement.classList.toggle('dark', value), dark)
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    if (process.env.RB_QA_VISUAL_DIR) {
      await mkdir(process.env.RB_QA_VISUAL_DIR, { recursive: true })
      await page.screenshot({ path: join(process.env.RB_QA_VISUAL_DIR, `transfer-source-${width}-${dark ? 'dark' : 'light'}.png`), fullPage: true, animations: 'disabled' })
    }
  }
  await page.getByRole('button', { name: 'TAR.GZ', exact: true }).click()
  await expect(page.getByRole('button', { name: '导出数据', exact: true })).toBeEnabled()
  const result = await download(page, true)
  expect(result.content.business_configs[0].content).toBe('B')
  expect(mock.writes('transfer/export')[0].postDataJSON().format).toBe('tar.gz')
})
