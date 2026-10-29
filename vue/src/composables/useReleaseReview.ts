import { computed, ref, shallowRef, watch } from 'vue'
import { useDialogAttempt } from './useDialogAttempt'
import { useReleaseComparison, type ReviewScope } from './useReleaseComparison'
import { pendingReleaseKey, readPendingRelease, writePendingRelease, type PendingRelease } from './releaseReviewPending'
import { releaseApi, type ReleaseActiveInfo, type ReleaseResourceIssue } from '@/lib/api/release'
import type { CompareTarget } from '@/lib/api/release-compare'
import { freezeApi, type FreezeStatusData } from '@/lib/api/freeze'
import { ApiError } from '@/lib/api/error'
import { assertScope } from '@/lib/scope'
import { noteTooLong } from '@/lib/operation-note'
import { createRequestId } from '@/lib/request-id'
import { useAuthStore } from '@/stores/auth'
import { sessionIdentity } from '@/lib/auth/session'
import { operationMessage } from '@/lib/api/messages'

export function useReleaseReview(open: () => boolean, scope: () => ReviewScope, initial: () => CompareTarget | null, changed: () => void) {
  const auth = useAuthStore()
  const candidate = shallowRef<CompareTarget | null>(null)
  const freeze = shallowRef<FreezeStatusData | null>(null)
  const reason = ref(''), error = ref(''), busy = ref(false), loading = ref(false)
  const issues = ref<ReleaseResourceIssue[]>([])
  const pending = shallowRef<PendingRelease | null>(null)
  const done = ref(false), operationConfirmed = ref(false), activeVerified = ref(false)
  const actualActive = shallowRef<ReleaseActiveInfo | null>(null)
  let comparison: ReturnType<typeof useReleaseComparison>
  let storageKey = ''
  const begin = useDialogAttempt(() => [open(), ...Object.values(scope()), initial()?.snapshot_id], () => {
    candidate.value = null; freeze.value = null; reason.value = ''; error.value = ''
    busy.value = false; loading.value = false; pending.value = null; issues.value = []
    done.value = false; operationConfirmed.value = false; activeVerified.value = false; actualActive.value = null
    comparison?.reset()
  })
  comparison = useReleaseComparison(scope, begin)
  const frozen = computed(() => freeze.value?.state === 'locked')
  const canActivate = computed(() => Boolean(auth.authenticated && comparison.result.value &&
    comparison.result.value.comparison_mode === 'active' && typeof comparison.result.value.active_revision === 'string' &&
    !comparison.result.value.target_is_active && !comparison.loading.value && !comparison.loadingMore.value &&
    !comparison.error.value && !busy.value && !pending.value && !done.value))
  const message = (cause: unknown) => cause instanceof ApiError ? operationMessage(cause.errorType, cause.message) :
    cause instanceof Error ? cause.message : '操作失败，请重试'
  function save(value: PendingRelease | null) {
    writePendingRelease(storageKey, value)
    pending.value = value
  }
  async function refreshPreparation() {
    const valid = begin(), selected = { ...scope() }
    loading.value = true; error.value = ''; freeze.value = null
    try {
      const [status] = await Promise.all([freezeApi.status(selected.environmentKey, selected.pipelineKey),
        releaseApi.activeState(selected.environmentKey, selected.pipelineKey)])
      if (!valid()) return
      assertScope(selected.environmentKey, selected.pipelineKey, selected.scopeId)
      if (status.scope_id !== selected.scopeId || typeof status.revision !== 'string' || !/^\d+$/.test(status.revision)) throw new Error('编辑区状态不完整，请刷新')
      freeze.value = status
    } catch (cause) { if (valid()) error.value = message(cause) }
    finally { if (valid()) loading.value = false }
  }
  watch(() => [open(), scope().scopeId, initial()?.snapshot_id, sessionIdentity.value] as const, async () => {
    if (!open() || !auth.authenticated) return
    const selected = scope()
    storageKey = pendingReleaseKey(String(auth.user?.id ?? ''), selected.scopeId)
    candidate.value = initial() ? { ...initial()! } : null
    try {
      pending.value = readPendingRelease(storageKey, selected.scopeId)
      if (pending.value && (pending.value.payload.environment_key !== selected.environmentKey || pending.value.payload.pipeline_key !== selected.pipelineKey)) {
        throw new Error('未确认操作的范围不匹配，请关闭后刷新列表核对')
      }
      if (pending.value?.kind === 'activate') candidate.value = pending.value.target
    } catch (cause) { error.value = message(cause); return }
    if (candidate.value) await comparison.start(candidate.value)
    else await refreshPreparation()
  }, { immediate: true })

  function captureFailure(cause: unknown) {
    error.value = message(cause)
    if (cause instanceof ApiError) {
      issues.value = ((cause.payload ?? {}) as { resource_issues?: ReleaseResourceIssue[] }).resource_issues ?? []
      if (cause.code >= 400 && cause.code < 500) save(null)
      if (['RELEASE_ACTIVE_REVISION_CONFLICT', 'RELEASE_COMPARE_BASELINE_MISMATCH'].includes(cause.errorType)) {
        comparison.invalidate('生效版本已变化，请重新比较并再次确认。')
      }
      if (['SCOPE_IDENTITY_CONFLICT', 'SCOPE_DELETED', 'RELEASE_SNAPSHOT_MISMATCH', 'RELEASE_VERSION_DELETED'].includes(cause.errorType)) {
        comparison.invalidate(error.value)
      }
    }
  }
  async function execute(intent: PendingRelease, valid: () => boolean) {
    if (intent.kind === 'create') {
      const result = await releaseApi.create(intent.payload)
      if (!valid()) return
      if (result.meta?.scope_id !== scope().scopeId || !result.meta.version ||
          (intent.payload.version && result.meta.version !== intent.payload.version) || !result.meta.snapshot_id) {
        throw new Error('创建响应不完整，请确认原请求结果')
      }
      candidate.value = { version: result.meta.version, snapshot_id: result.meta.snapshot_id }
      save(null); changed()
      await comparison.start(candidate.value)
      return
    }
    const result = await releaseApi.activate(intent.payload)
    if (!valid()) return
    if (result.version !== intent.payload.version || typeof result.operation?.replayed !== 'boolean' ||
        result.operation?.request_id !== intent.payload.request_id || typeof result.operation.applied_revision !== 'string' ||
        !/^\d+$/.test(result.operation.applied_revision)) throw new Error('切换响应不完整，请确认原请求结果')
    operationConfirmed.value = true; done.value = true; save(null); changed()
    // A replay confirms the old operation, not that its target is still active.
    try {
      const state = await releaseApi.activeState(scope().environmentKey, scope().pipelineKey)
      if (!valid()) return
      if (state.active && state.active.scope_id !== scope().scopeId) throw new Error('生效状态的范围不匹配')
      actualActive.value = state.active; activeVerified.value = true
    } catch { if (valid()) error.value = '切换操作已确认，当前生效状态读取失败，请刷新版本列表。' }
  }
  async function prepare() {
    if (busy.value || loading.value || pending.value || !freeze.value || !auth.authenticated) return
    const note = reason.value.trim(), selected = { ...scope() }
    if (noteTooLong(note)) { error.value = '备注最多500字'; return }
    const valid = begin()
    busy.value = true; error.value = ''; issues.value = []
    try {
      // A new intent must have a usable ID before it can freeze the editor.
      const requestId = createRequestId()
      // Always re-read before locking; a failed lock never proceeds to create.
      let status = await freezeApi.status(selected.environmentKey, selected.pipelineKey)
      if (!valid()) return
      assertScope(selected.environmentKey, selected.pipelineKey, selected.scopeId)
      if (status.scope_id !== selected.scopeId || typeof status.revision !== 'string' || !/^\d+$/.test(status.revision)) throw new Error('编辑区状态不完整，请刷新')
      if (status.state !== 'locked') {
        status = await freezeApi.lock({ environment_key: selected.environmentKey, pipeline_key: selected.pipelineKey,
          expected_scope_id: selected.scopeId, expected_revision: status.revision, reason: note })
      }
      if (!valid()) return
      if (status.state !== 'locked' || status.scope_id !== selected.scopeId || typeof status.revision !== 'string' || !/^\d+$/.test(status.revision)) throw new Error('未确认锁版结果，请刷新编辑区状态')
      freeze.value = status
      const intent: PendingRelease = { kind: 'create', payload: {
        environment_key: selected.environmentKey, pipeline_key: selected.pipelineKey, expected_scope_id: selected.scopeId,
        reason: note, request_id: requestId, expected_freeze_revision: status.revision,
      } }
      save(intent)
      await execute(intent, valid)
    } catch (cause) { if (valid()) captureFailure(cause) }
    finally { if (valid()) busy.value = false }
  }
  async function activate() {
    if (!canActivate.value || !candidate.value || !comparison.result.value || comparison.result.value.comparison_mode !== 'active') return
    if (noteTooLong(reason.value.trim())) { error.value = '备注最多500字'; return }
    const valid = begin(), selected = { ...scope() }
    busy.value = true; error.value = ''; issues.value = []
    try {
      const intent: PendingRelease = { kind: 'activate', target: { ...candidate.value }, payload: {
        environment_key: selected.environmentKey, pipeline_key: selected.pipelineKey, expected_scope_id: selected.scopeId,
        version: candidate.value.version, expected_active_revision: comparison.result.value.active_revision,
        request_id: createRequestId(), reason: reason.value.trim(),
      } }
      save(intent)
      await execute(intent, valid)
    } catch (cause) { if (valid()) captureFailure(cause) }
    finally { if (valid()) busy.value = false }
  }
  async function confirmPending() {
    if (!pending.value || busy.value || !auth.authenticated) return
    const intent = pending.value, valid = begin()
    busy.value = true; error.value = ''; issues.value = []
    try { await execute(intent, valid) }
    catch (cause) { if (valid()) captureFailure(cause) }
    finally { if (valid()) busy.value = false }
  }
  async function recompare() {
    if (!candidate.value || busy.value || pending.value) return
    error.value = ''; issues.value = []
    await comparison.start(candidate.value)
  }
  return { candidate, freeze, frozen, reason, error, busy, loading, issues, pending, done,
    operationConfirmed, activeVerified, actualActive, comparison, canActivate, prepare, activate, confirmPending, recompare, refreshPreparation }
}
