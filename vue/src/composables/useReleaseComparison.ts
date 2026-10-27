import { computed, ref, shallowRef } from 'vue'
import { compareRelease, type CompareTarget, type ReleaseComparison, type ReleaseComparePayload } from '@/lib/api/release-compare'
import { releaseApi, type ReleaseDetailData } from '@/lib/api/release'
import { assertScope } from '@/lib/scope'
import { ApiError } from '@/lib/api/error'
import { operationMessage } from '@/lib/api/messages'

export interface ReviewScope { environmentKey: string; pipelineKey: string; scopeId: string }
export function useReleaseComparison(scope: () => ReviewScope, current: () => () => boolean) {
  const result = shallowRef<ReleaseComparison | null>(null)
  const error = ref('')
  const loading = ref(false)
  const loadingMore = ref(false)
  const detailsLoading = ref(false)
  const detailsError = ref('')
  const details = shallowRef<{ before: ReleaseDetailData | null; after: ReleaseDetailData } | null>(null)
  let pin: ReleaseComparePayload | null = null
  let epoch = 0
  const beforeConfigs = computed(() => new Map(details.value?.before?.configs.map(item => [item.resource_key, item]) ?? []))
  const afterConfigs = computed(() => new Map(details.value?.after.configs.map(item => [item.resource_key, item]) ?? []))

  function reset() {
    epoch++; pin = null; result.value = null; details.value = null
    error.value = ''; detailsError.value = ''; loading.value = false; loadingMore.value = false; detailsLoading.value = false
  }
  function invalidate(message: string) { reset(); error.value = message }
  const describe = (cause: unknown) => {
    if (cause instanceof ApiError && ['RELEASE_ACTIVE_REVISION_CONFLICT', 'RELEASE_COMPARE_BASELINE_MISMATCH'].includes(cause.errorType)) {
      return '生效版本已变化，请重新比较并确认。'
    }
    const side = cause instanceof ApiError ? cause.payload?.side : undefined
    const prefix = side === 'baseline' ? '基准版本：' : side === 'target' ? '目标版本：' : ''
    return prefix + (cause instanceof ApiError ? operationMessage(cause.errorType, cause.message) : cause instanceof Error ? cause.message : '差异读取失败，请重试')
  }
  async function acceptFirst(payload: ReleaseComparePayload, batch: number, valid: () => boolean) {
    const data = await compareRelease(payload)
    if (!valid() || batch !== epoch) return
    pin = payload; result.value = data
  }
  async function start(target: CompareTarget) {
    reset()
    const batch = epoch, valid = current(), selected = { ...scope() }
    loading.value = true
    try {
      const state = await releaseApi.activeState(selected.environmentKey, selected.pipelineKey)
      if (!valid() || batch !== epoch) return
      assertScope(selected.environmentKey, selected.pipelineKey, selected.scopeId)
      // State parsing checks the mandatory revision and active consistency.
      // A null pointer can have a nonzero revision; never infer zero from null.
      const revision = state.active_revision
      const payload: ReleaseComparePayload = {
        comparison_mode: 'active',
        environment_key: selected.environmentKey, pipeline_key: selected.pipelineKey, expected_scope_id: selected.scopeId,
        version: target.version, expected_snapshot_id: target.snapshot_id, expected_active_revision: revision,
        expected_active_snapshot_id: state.active?.snapshot_id ?? '', offset: 0, limit: 20,
      }
      await acceptFirst(payload, batch, valid)
    } catch (cause) {
      if (!valid() || batch !== epoch) return
      error.value = describe(cause)
    }
    finally { if (valid() && batch === epoch) loading.value = false }
  }
  async function startFixed(baseline: CompareTarget, target: CompareTarget) {
    reset()
    const batch = epoch, valid = current(), selected = { ...scope() }
    loading.value = true
    try {
      // Construct a separate payload: even empty active-only fields are illegal.
      await acceptFirst({ comparison_mode: 'fixed', environment_key: selected.environmentKey,
        pipeline_key: selected.pipelineKey, expected_scope_id: selected.scopeId,
        baseline_version: baseline.version, expected_baseline_snapshot_id: baseline.snapshot_id,
        version: target.version, expected_snapshot_id: target.snapshot_id, offset: 0, limit: 20 }, batch, valid)
    } catch (cause) { if (valid() && batch === epoch) error.value = describe(cause) }
    finally { if (valid() && batch === epoch) loading.value = false }
  }
  async function more() {
    if (!pin || !result.value?.has_more || loadingMore.value) return
    const batch = epoch, valid = current()
    const payload = { ...pin, offset: result.value.next_offset! }
    loadingMore.value = true; error.value = ''
    try {
      const next = await compareRelease(payload)
      if (!valid() || batch !== epoch || !result.value) return
      if (next.baseline?.version !== result.value.baseline?.version) throw new Error('比较基准已变化，请重新比较')
      result.value = { ...next, items: [...result.value.items, ...next.items] }
    } catch (cause) {
      if (!valid() || batch !== epoch) return
      // On a timeout the exact cursor can be retried. Identity/CAS failures
      // invalidate every page, including the previous successful pages.
      if (cause instanceof ApiError && (cause.code === 0 || cause.errorType === 'RELEASE_COMPARE_TIMEOUT')) error.value = describe(cause)
      else invalidate(describe(cause))
    } finally { if (valid() && batch === epoch) loadingMore.value = false }
  }
  async function loadDetails() {
    if (!result.value || detailsLoading.value || details.value) return
    const comparison = result.value, selected = { ...scope() }, batch = epoch, valid = current()
    detailsLoading.value = true; detailsError.value = ''
    const load = async (target: CompareTarget, side: 'baseline' | 'target') => {
      try {
        const data = await releaseApi.detail(selected.environmentKey, selected.pipelineKey, target.version)
        assertScope(selected.environmentKey, selected.pipelineKey, selected.scopeId)
        if (data.meta.scope_id !== selected.scopeId || data.meta.snapshot_id !== target.snapshot_id || data.meta.version !== target.version) {
          throw new ApiError(409, '版本身份已变化，请重新比较')
        }
        return data
      } catch (cause) {
        if (cause instanceof ApiError) cause.payload = { ...cause.payload, side }
        throw cause
      }
    }
    try {
      const [before, after] = await Promise.all([comparison.baseline ? load(comparison.baseline, 'baseline') : Promise.resolve(null), load(comparison.target, 'target')])
      if (valid() && batch === epoch) details.value = { before, after }
    } catch (cause) {
      if (!valid() || batch !== epoch) return
      detailsError.value = describe(cause)
      if (cause instanceof ApiError && [404, 409, 410].includes(cause.code)) invalidate(describe(cause))
    } finally { if (valid() && batch === epoch) detailsLoading.value = false }
  }
  return { result, error, loading, loadingMore, details, detailsLoading, detailsError, beforeConfigs, afterConfigs, start, startFixed, more, loadDetails, reset, invalidate }
}
