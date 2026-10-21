import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import {
  releaseApi,
  type ReleaseActivatePayload,
  type ReleaseActiveInfo,
  type ReleaseCreatePayload,
  type ReleaseDetailData,
  type ReleaseHistoryData,
  type ReleaseListItem,
  type ReleaseDeletePayload,
} from '@/lib/api/release'

/**
 * 发布版本状态:
 * - 每个异步请求都带上"范围键 + 批次号",只有当前范围且当前批次的响应才能写入状态;
 *   范围变化与创建/切换成功后的刷新都会开启新批次,旧响应(含 A→B→A 的迟到响应)一律丢弃。
 * - loading 用进行中计数,避免旧请求提前结束或卡住新请求的加载态。
 */
export const useReleaseStore = defineStore('release', () => {
  const scopeKey = ref('')
  const batchId = ref(0)
  const pending = ref(0)
  const items = ref<ReleaseListItem[]>([])
  const hasMore = ref(false)
  const nextBeforeId = ref('')
  const active = ref<ReleaseActiveInfo | null>(null)
  const error = ref('')
  const activeError = ref('')
  const listLoaded = ref(false)
  const activeLoaded = ref(false)
  const listedActive = ref<ReleaseActiveInfo | null>(null)
  const contentEpoch = ref(0)
  let scopeEpoch = 0

  const keyOf = (environmentKey: string, pipelineKey: string) => `${environmentKey}/${pipelineKey}`
  const isCurrentScope = (environmentKey: string, pipelineKey: string) =>
    keyOf(environmentKey, pipelineKey) === scopeKey.value
  /** 请求是否仍属于当前范围与当前批次。 */
  const isCurrentRequest = (environmentKey: string, pipelineKey: string, batch: number) =>
    batch === batchId.value && isCurrentScope(environmentKey, pipelineKey)
  const beginBatch = (): number => {
    batchId.value += 1
    // 新批次作废旧批次的在途请求:加载态只反映当前批次
    pending.value = 0
    return batchId.value
  }

  const activeVersion = computed(() => active.value?.version ?? '')
  const loading = computed(() => pending.value > 0)
  const deletionStateKnown = computed(() => {
    if (loading.value || error.value || activeError.value || !listLoaded.value || !activeLoaded.value) return false
    if (active.value && (!active.value.snapshot_id || !active.value.version || !active.value.revision)) return false
    const sameActive = listedActive.value?.snapshot_id === active.value?.snapshot_id &&
      listedActive.value?.revision === active.value?.revision
    return sameActive && !items.value.some(row => row.is_active &&
      row.meta.snapshot_id !== active.value?.snapshot_id)
  })
  const canDelete = (item: ReleaseListItem) => deletionStateKnown.value && Boolean(item.meta.snapshot_id) &&
    !item.is_active && item.meta.snapshot_id !== active.value?.snapshot_id && item.meta.version !== active.value?.version

  /**
   * 目标版本是否早于当前生效版本(列表按创建倒序,序号更大表示更早),仅决定按钮文案:
   * 切到更新的版本说“切换生效版本”,切到更早的版本才说“切回此版本”。
   */
  const isOlderThanActive = (version: string): boolean => {
    const activeIndex = items.value.findIndex((item) => item.is_active)
    const targetIndex = items.value.findIndex((item) => item.meta.version === version)
    if (activeIndex < 0 || targetIndex < 0) return false
    return targetIndex > activeIndex
  }

  const reset = () => {
    items.value = []
    hasMore.value = false
    nextBeforeId.value = ''
    active.value = null
    error.value = ''
    activeError.value = ''
    listLoaded.value = false
    activeLoaded.value = false
    listedActive.value = null
  }

  /** 切换范围:清空并开启新批次加载列表与 active。 */
  const switchScope = async (environmentKey: string, pipelineKey: string): Promise<void> => {
    scopeEpoch++
    scopeKey.value = keyOf(environmentKey, pipelineKey)
    const batch = beginBatch()
    reset()
    if (!environmentKey || !pipelineKey) return
    await Promise.all([
      loadFirstPage(environmentKey, pipelineKey, batch),
      refreshActive(environmentKey, pipelineKey, batch),
    ])
  }

  const loadFirstPage = async (environmentKey: string, pipelineKey: string, batch: number): Promise<void> => {
    if (!isCurrentRequest(environmentKey, pipelineKey, batch)) return
    pending.value += 1
    error.value = ''
    try {
      const data = await releaseApi.list(environmentKey, pipelineKey)
      if (!isCurrentRequest(environmentKey, pipelineKey, batch)) return
      items.value = data.items ?? []
      hasMore.value = data.has_more ?? false
      nextBeforeId.value = data.next_before_id ?? ''
      listedActive.value = data.active ?? null
      listLoaded.value = true
    } catch (e) {
      if (!isCurrentRequest(environmentKey, pipelineKey, batch)) return
      error.value = e instanceof Error ? e.message : '版本列表加载失败'
    } finally {
      // 旧批次的请求已被新批次作废(beginBatch 归零计数),不得再扣减当前批次的计数
      if (isCurrentRequest(environmentKey, pipelineKey, batch)) {
        pending.value = Math.max(0, pending.value - 1)
      }
    }
  }

  /** 加载更多:使用发起时的批次,刷新或切范围后到达的旧分页被丢弃。 */
  const loadMore = async (environmentKey: string, pipelineKey: string): Promise<void> => {
    if (!hasMore.value || !nextBeforeId.value) return
    const batch = batchId.value
    if (!isCurrentRequest(environmentKey, pipelineKey, batch)) return
    pending.value += 1
    try {
      const data = await releaseApi.list(environmentKey, pipelineKey, nextBeforeId.value)
      if (!isCurrentRequest(environmentKey, pipelineKey, batch)) return
      items.value = [...items.value, ...(data.items ?? [])]
      hasMore.value = data.has_more ?? false
      nextBeforeId.value = data.next_before_id ?? ''
    } catch (e) {
      if (!isCurrentRequest(environmentKey, pipelineKey, batch)) return
      error.value = e instanceof Error ? e.message : '版本列表加载失败'
    } finally {
      if (isCurrentRequest(environmentKey, pipelineKey, batch)) {
        pending.value = Math.max(0, pending.value - 1)
      }
    }
  }

  /** 刷新 active;不开启新批次,但旧批次的迟到响应不会覆盖当前状态。 */
  const refreshActive = async (environmentKey: string, pipelineKey: string, batch = batchId.value): Promise<void> => {
    if (!isCurrentRequest(environmentKey, pipelineKey, batch)) return
    pending.value += 1
    activeLoaded.value = false
    activeError.value = ''
    try {
      const data = await releaseApi.active(environmentKey, pipelineKey)
      if (!isCurrentRequest(environmentKey, pipelineKey, batch)) return
      active.value = data
      activeLoaded.value = true
    } catch (cause) {
      if (!isCurrentRequest(environmentKey, pipelineKey, batch)) return
      active.value = null
      activeError.value = cause instanceof Error ? cause.message : '生效状态加载失败'
    } finally {
      if (isCurrentRequest(environmentKey, pipelineKey, batch)) pending.value = Math.max(0, pending.value - 1)
    }
  }

  /** 刷新当前范围:开启新批次,旧的在途响应随即作废。 */
  const refreshScope = async (environmentKey: string, pipelineKey: string): Promise<void> => {
    if (!isCurrentScope(environmentKey, pipelineKey)) return
    const batch = beginBatch()
    listLoaded.value = false
    hasMore.value = false
    nextBeforeId.value = ''
    await Promise.all([
      loadFirstPage(environmentKey, pipelineKey, batch),
      refreshActive(environmentKey, pipelineKey, batch),
    ])
  }

  const detail = async (environmentKey: string, pipelineKey: string, version: string): Promise<ReleaseDetailData> =>
    releaseApi.detail(environmentKey, pipelineKey, version)

  const history = async (environmentKey: string, pipelineKey: string, beforeEventId = ''): Promise<ReleaseHistoryData> =>
    releaseApi.history(environmentKey, pipelineKey, beforeEventId)

  /** 创建版本:成功后开启新批次刷新列表与双状态(创建不切换生效)。 */
  const create = async (payload: ReleaseCreatePayload) => {
    const originalScope = scopeEpoch
    const result = await releaseApi.create(payload)
    if (originalScope === scopeEpoch && isCurrentScope(payload.environment_key, payload.pipeline_key)) {
      const batch = beginBatch()
      await Promise.all([
        loadFirstPage(payload.environment_key, payload.pipeline_key, batch),
        refreshActive(payload.environment_key, payload.pipeline_key, batch),
      ])
    }
    return result
  }

  /** 切换生效版本:成功后开启新批次刷新列表与 active。 */
  const activate = async (payload: ReleaseActivatePayload) => {
    const originalScope = scopeEpoch
    const result = await releaseApi.activate(payload)
    if (originalScope === scopeEpoch && isCurrentScope(payload.environment_key, payload.pipeline_key)) {
      const batch = beginBatch()
      await Promise.all([
        loadFirstPage(payload.environment_key, payload.pipeline_key, batch),
        refreshActive(payload.environment_key, payload.pipeline_key, batch),
      ])
    }
    return result
  }

  const deleteVersion = async (payload: ReleaseDeletePayload) => {
    const originalScope = scopeEpoch
    const result = await releaseApi.delete(payload)
    if (originalScope !== scopeEpoch || !isCurrentScope(payload.environment_key, payload.pipeline_key)) {
      return { result, refreshFailed: false }
    }
    contentEpoch.value++
    await refreshScope(payload.environment_key, payload.pipeline_key)
    return { result, refreshFailed: Boolean(error.value || activeError.value) }
  }

  return {
    resetSession: () => { scopeEpoch++; contentEpoch.value++; scopeKey.value = ''; beginBatch(); reset() },
    canDelete,
    activeError,
    contentEpoch,
    deleteVersion,
    scopeKey,
    items,
    hasMore,
    nextBeforeId,
    active,
    activeVersion,
    isOlderThanActive,
    loading,
    error,
    switchScope,
    refreshScope,
    loadMore,
    refreshActive,
    detail,
    history,
    create,
    activate,
    reset,
    keyOf,
  }
})
