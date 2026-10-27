import type { Page, Route } from '@playwright/test'
import { mockApi } from './mock'
export function ok(route: Route, data: unknown) { return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ code: 200, msg: 'OK', data }) }) }

export const counts = { environments: 2, pipelines: 3, configs: 12, assets: 4, versions: 5, deleted_versions: 1, freeze_events: 6, release_events: 8, configs_deleted: 2, assets_deleted: 1 }
export const backupItem = { backup_id: '11111111-1111-4111-8111-111111111111', sha256: 'a'.repeat(64), size: 8192, consistent_at: '2026-10-25T02:00:01Z', created_at: '2026-10-25T02:00:00Z', counts, actor: 'admin', reason: '升级前备份' }
export function job(kind = 'create', state = 'queued', extra: Record<string, unknown> = {}) {
  return { job_id: '22222222-2222-4222-8222-222222222222', request_id: '33333333-3333-4333-8333-333333333333', kind, state, phase: 'waiting',
    created_at: '2026-10-25T02:00:00Z', updated_at: '2026-10-25T02:00:01Z', actor: 'admin', reason: '', backup_id: backupItem.backup_id,
    preview_id: null, result: null, error: null, ...extra }
}
export const preview = { preview_id: '44444444-4444-4444-8444-444444444444', job_id: '22222222-2222-4222-8222-222222222222', valid: true,
  package_sha256: 'b'.repeat(64), backup: { ...backupItem, files: 12, schema_version: 1, producer: { app_version: 'dev', git_commit: '' } },
  warnings: ['external_references_not_included:1'], issues: [] as string[], expires_at: '2099-10-26T02:00:00Z', target_instance_id: '55555555-5555-4555-8555-555555555555', created_at: '2026-10-25T02:00:00Z' }
export const packageFile = { name: 'business-backup.zip', mimeType: 'application/zip', buffer: Buffer.from('mock backup') }
export function accepted(route: Route, value: unknown) { return route.fulfill({ status: 202, contentType: 'application/json', body: JSON.stringify({ code: 202, msg: 'OK', data: { job: value } }) }) }
export async function backupFixture(page: Page) {
  const mock = await mockApi(page, true)
  const state = { supported: true, retention: undefined as { keep_count: number; managed: boolean } | undefined,
    items: [] as typeof backupItem[], currentJob: job(), preview: structuredClone(preview) }
  mock.overrides.set('backup/capabilities', route => ok(route, { supported: state.supported,
    reasons: state.supported ? [] : ['database_driver_not_supported', 'storage_not_local', 'redis_enabled'],
    limits: { package_bytes: 2 ** 31, max_backups: 10 }, restore_requires_restart: true, mode: 'business_full',
    ...(state.retention ? { rollback_retention: state.retention } : {}) }))
  mock.overrides.set('backup/list', route => ok(route, { items: state.items, has_more: false, next_before_id: '' }))
  mock.overrides.set('backup/create', route => accepted(route, state.currentJob))
  mock.overrides.set('backup/status', route => ok(route, { job: state.currentJob }))
  mock.overrides.set('backup/restore/preview', route => route.request().method() === 'POST'
    ? accepted(route, job('validate', 'succeeded', { preview_id: preview.preview_id, phase: 'validated' })) : ok(route, state.preview))
  mock.overrides.set('backup/restore/execute', route => {
    state.currentJob = job('restore', 'restart_required', { phase: 'waiting_for_restart' })
    return accepted(route, state.currentJob)
  })
  mock.overrides.set('backup/restore/cancel', route => { state.currentJob = job('restore', 'cancelled', { phase: 'cancelled' }); return ok(route, { cancelled: true }) })
  mock.overrides.set('backup/delete', route => { state.items = []; return ok(route, { deleted: true }) })
  return { mock, state }
}
