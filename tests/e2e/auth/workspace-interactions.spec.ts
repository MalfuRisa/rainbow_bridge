import { test, expect, type Page } from '@playwright/test'
import { mkdir } from 'node:fs/promises'
import { join } from 'node:path'
import { mockApi, ok, fail, config } from './mock'

async function openConfig(page: Page) {
  const mock = await mockApi(page, true)
  await page.goto('config?env=qa&pipeline=web')
  await page.getByRole('button', { name: '新增配置', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: '新增配置', exact: true })
  await expect(dialog.getByLabel('名称 *', { exact: true })).toBeFocused()
  await expect(page.locator('.workspace-dialog-overlay')).toBeVisible()
  return { mock, dialog }
}

async function capture(page: Page, name: string) {
  if (!process.env.RB_QA_VISUAL_DIR) return
  await mkdir(process.env.RB_QA_VISUAL_DIR, { recursive: true })
  await page.screenshot({ path: join(process.env.RB_QA_VISUAL_DIR, name + '.png'), animations: 'disabled' })
}

test('配置校验定位首个错误，Enter 提交且等待期间阻止重复保存和关闭', async ({ page }) => {
  const { mock, dialog } = await openConfig(page)
  await dialog.getByRole('button', { name: '关闭弹窗', exact: true }).focus()
  await page.keyboard.press('Tab')
  await expect(dialog.locator('#name')).toBeFocused()
  await capture(page, 'config-dialog-1440-light')
  await dialog.getByLabel('名称 *', { exact: true }).press('Enter')
  await expect(dialog.getByText('名称不能为空', { exact: true })).toBeVisible()
  await expect(dialog.locator('#name')).toHaveAttribute('aria-invalid', 'true')
  await expect(dialog.locator('#name')).toBeFocused()
  expect(mock.count('config/create')).toBe(0)
  await capture(page, 'config-validation-light')
  await dialog.locator('#name').fill('API 基础地址')
  await dialog.locator('#alias').fill('API_BASE_URL')
  await dialog.locator('#content').fill('https://example.test/api')
  let release!: () => void
  const pending = new Promise<void>(resolve => { release = resolve })
  mock.overrides.set('config/create', async route => { await pending; await ok(route, {}) })
  await dialog.locator('#content').press('Enter')
  await expect(dialog.getByRole('button', { name: '保存中...' })).toBeDisabled()
  await expect(dialog.getByRole('button', { name: '取消', exact: true })).toBeDisabled()
  await expect(dialog.getByRole('button', { name: '关闭弹窗', exact: true })).toBeDisabled()
  await page.keyboard.press('Escape')
  await page.mouse.click(12, 12)
  await expect(dialog).toBeVisible()
  expect(mock.count('config/create')).toBe(1)
  const payload = mock.writes('config/create')[0].postDataJSON()
  expect(payload.config).toMatchObject({ name: 'API 基础地址', alias: 'API_BASE_URL', type: 'text', content: 'https://example.test/api', environment_key: 'qa', pipeline_key: 'web' })
  release()
  await expect(dialog).toBeHidden()
  await expect(page.getByRole('button', { name: '新增配置', exact: true })).toBeFocused()
})

test('保存失败保留原输入，误点遮罩不丢草稿，显式关闭可重新开始', async ({ page }) => {
  const { mock, dialog } = await openConfig(page)
  await dialog.locator('#name').fill('保留草稿')
  await dialog.locator('#alias').fill('DRAFT')
  await dialog.locator('#content').fill('未保存内容')
  await page.mouse.click(12, 12)
  await expect(dialog).toBeVisible()
  mock.overrides.set('config/create', route => fail(route, 503, 'TEMPORARY_FAILURE'))
  await dialog.getByRole('button', { name: '保存', exact: true }).click()
  await expect(dialog.getByRole('alert')).toBeVisible()
  await expect(dialog.locator('#name')).toHaveValue('保留草稿')
  await expect(dialog.locator('#content')).toHaveValue('未保存内容')
  await expect(dialog.getByRole('button', { name: '保存', exact: true })).toBeEnabled()
  await page.keyboard.press('Escape')
  await expect(dialog).toBeHidden()
  await page.getByRole('button', { name: '新增配置', exact: true }).click()
  await expect(dialog.locator('#name')).toHaveValue('')
  await expect(dialog.getByRole('alert')).toHaveCount(0)
})

test('长配置只滚动字段区，标题与保存操作在桌面视口内可见', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 720 })
  const { dialog } = await openConfig(page)
  await dialog.getByRole('combobox', { name: '配置类型' }).click()
  const menu = page.getByRole('listbox')
  await expect.poll(async () => {
    const bounds = await menu.boundingBox()
    return bounds ? bounds.y >= 0 && bounds.y + bounds.height <= 720 : false
  }).toBe(true)
  await capture(page, 'config-types-1280-light')
  await page.getByRole('option', { name: '对象 JSON对象', exact: true }).click()
  await dialog.locator('#content').fill('{"revision":9007199254740993}')
  const body = dialog.locator('.spectrum-config-dialog-body')
  await body.evaluate(element => { element.scrollTop = element.scrollHeight })
  expect(await body.evaluate(element => element.scrollTop)).toBeGreaterThan(0)
  const box = await dialog.boundingBox()
  const header = await dialog.getByRole('heading', { name: '新增配置', exact: true }).boundingBox()
  const save = await dialog.getByRole('button', { name: '保存', exact: true }).boundingBox()
  expect(box!.y).toBeGreaterThanOrEqual(30)
  expect(box!.y + box!.height).toBeLessThanOrEqual(690)
  expect(header!.y).toBeGreaterThanOrEqual(box!.y)
  expect(save!.y + save!.height).toBeLessThan(box!.y + box!.height)
  await capture(page, 'config-object-1280-light')
  await page.evaluate(() => document.documentElement.classList.add('dark'))
  await capture(page, 'config-object-1280-dark')
  await page.keyboard.press('Escape')
  await expect(dialog).toBeHidden()
})

test('颜色输入保持同步，选择器 Escape 不关闭父弹窗', async ({ page }) => {
  const { mock, dialog } = await openConfig(page)
  await dialog.locator('#name').fill('主题色')
  await dialog.locator('#alias').fill('THEME_COLOR')
  await dialog.getByRole('combobox', { name: '配置类型' }).click()
  await page.getByRole('option', { name: '色彩 颜色选择器', exact: true }).click()
  await dialog.getByLabel('颜色值', { exact: true }).fill('#B45AF7')
  await expect(dialog.getByLabel('选择配置颜色', { exact: true })).toHaveValue('#b45af7')
  await dialog.getByRole('combobox', { name: '配置类型' }).click()
  await page.keyboard.press('Escape')
  await expect(page.getByRole('listbox')).toBeHidden()
  await expect(dialog).toBeVisible()
  await capture(page, 'config-color-1440-light')
  await dialog.getByRole('button', { name: '保存', exact: true }).click()
  await expect(dialog).toBeHidden()
  expect(mock.writes('config/create')[0].postDataJSON().config.content).toBe('#B45AF7')
})

test('上传可用键盘触发，上传期间提供反馈并阻止保存', async ({ page }) => {
  const { mock, dialog } = await openConfig(page)
  await dialog.locator('#name').fill('配置文件')
  await dialog.locator('#alias').fill('SETTINGS')
  await dialog.getByRole('combobox', { name: '配置类型' }).click()
  await page.getByRole('option', { name: '文件 文件上传', exact: true }).click()
  await expect(page.getByRole('listbox')).toBeHidden()
  let release!: () => void
  const pending = new Promise<void>(resolve => { release = resolve })
  mock.overrides.set('asset/upload', async route => {
    await pending
    await ok(route, { asset: { file_id: 'a1', file_name: 'fixture.txt', content_type: 'text/plain', file_size: 4, url: '/rainbow-bridge/api/v1/asset/file/a1' } })
  })
  await dialog.getByRole('button', { name: '上传文件', exact: true }).focus()
  const chooser = page.waitForEvent('filechooser')
  await page.keyboard.press('Enter')
  await (await chooser).setFiles({ name: 'fixture.txt', mimeType: 'text/plain', buffer: Buffer.from('data') })
  await expect(dialog.getByRole('status')).toHaveText('上传中…')
  await expect(dialog.getByRole('button', { name: '保存', exact: true })).toBeDisabled()
  await expect(dialog.getByRole('button', { name: '上传文件', exact: true })).toBeDisabled()
  release()
  await expect(dialog.getByRole('status')).toBeHidden()
  await expect(dialog.getByRole('button', { name: '上传文件', exact: true })).toContainText('/api/v1/asset/file/a1')
  await dialog.getByRole('button', { name: '保存', exact: true }).click()
  await expect(dialog).toBeHidden()
  expect(mock.writes('config/create')[0].postDataJSON().config.content).toBe('/api/v1/asset/file/a1')
})

test('减少动态效果偏好对传送到页面根部的弹窗和下拉菜单生效', async ({ page }) => {
  await page.emulateMedia({ reducedMotion: 'reduce' })
  const { dialog } = await openConfig(page)
  expect(await dialog.evaluate(element => getComputedStyle(element).animationName)).toBe('none')
  await dialog.getByRole('combobox', { name: '配置类型' }).click()
  expect(await page.getByRole('listbox').evaluate(element => getComputedStyle(element).animationName)).toBe('none')
  await page.keyboard.press('Escape')
  await page.keyboard.press('Escape')
  await expect(dialog).toBeHidden()
})

for (const [type, content] of [
  ['number', '900719925474099312345'],
  ['keyvalue', '{"large":900719925474099312345,"flag":true,"empty":null}'],
]) {
  test(`编辑 ${type} 时保留原始数值和别名，失败后仍可重试`, async ({ page }) => {
    const mock = await mockApi(page, true)
    mock.overrides.set('config/list', route => ok(route, { list: [{ ...config, type, content }], total: 1 }))
    await page.goto('config?env=qa&pipeline=web')
    const origin = page.getByRole('button', { name: type === 'keyvalue' ? '编辑配置' : '编辑 站点标题', exact: true })
    await origin.click()
    const dialog = page.getByRole('dialog', { name: '编辑配置', exact: true })
    await expect(dialog.locator('#alias')).toBeDisabled()
    await expect(dialog.getByRole('combobox', { name: '配置类型' })).toBeDisabled()
    if (type === 'number') await expect(dialog.locator('#content')).toHaveValue(content)
    else await expect(dialog.getByLabel('键值 1', { exact: true })).toHaveValue('900719925474099312345')
    await dialog.locator('#name').fill('新的名称')
    mock.overrides.set('config/update', route => fail(route, 503, 'TEMPORARY_FAILURE'))
    await dialog.getByRole('button', { name: '保存', exact: true }).click()
    await expect(dialog.getByRole('alert')).toBeFocused()
    expect(mock.writes('config/update')[0].postDataJSON().config).toMatchObject({ name: '新的名称', alias: 'title', type, content })
    mock.overrides.set('config/update', route => ok(route, {}))
    await dialog.getByRole('button', { name: '保存', exact: true }).click()
    await expect(dialog).toBeHidden()
    await expect(origin).toBeFocused()
  })
}
