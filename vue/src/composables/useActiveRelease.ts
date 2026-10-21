import { ref } from 'vue'
import { ApiError } from '@/lib/api/error'
import {
  exportShownSnapshot,
  fetchActiveReleaseRaw,
  type RuntimeReleaseRawConfig,
} from '@/lib/api/release'

/**
 * 首页"当前生效配置"的唯一状态来源:
 * - 一次读取成功即页面事实,不做多请求拼接、不回退编辑区、不生成替身下载;
 * - 所有异步写入都校验"发起时的范围 + 批次号",A→B→A 的旧响应与旧 finally 都不能改动当前状态。
 */
export type ActiveReleaseState = 'idle' | 'loading' | 'ready' | 'not-active' | 'incomplete' | 'error'

export interface ActiveReleaseView {
  environmentKey: string
  pipelineKey: string
  version: string
  snapshotId: string
  sourceFreezeRevision: string
  payloadSha256: string
  activeRevision: string
  configs: RuntimeReleaseRawConfig[]
  activatedAt: string
}

export function useActiveRelease() {
  const state = ref<ActiveReleaseState>('idle')
  const view = ref<ActiveReleaseView | null>(null)
  const errorMessage = ref('')
  const errorType = ref('')
  const exporting = ref(false)
  const exportError = ref('')

  let batchId = 0
  let currentScope = { environmentKey: '', pipelineKey: '' }

  const isCurrentScope = (environmentKey: string, pipelineKey: string) =>
    currentScope.environmentKey === environmentKey && currentScope.pipelineKey === pipelineKey

  const applyError = (error: unknown) => {
    if (error instanceof ApiError) {
      errorType.value = error.errorType
      if (error.errorType === 'RELEASE_NOT_ACTIVE') {
        state.value = 'not-active'
        errorMessage.value = '尚无生效版本,请先创建版本并切换生效'
        return
      }
      if (error.errorType === 'RELEASE_INCOMPLETE') {
        state.value = 'incomplete'
        errorMessage.value = '生效版本不完整,暂时无法读取'
        return
      }
      state.value = 'error'
      errorMessage.value = error.message || '读取生效配置失败'
      return
    }
    state.value = 'error'
    errorMessage.value = error instanceof Error ? error.message : '读取生效配置失败'
  }

  /** 加载指定范围的生效版本;范围或批次变化后旧结果一律丢弃。 */
  const load = async (environmentKey: string, pipelineKey: string): Promise<void> => {
    batchId += 1
    const batch = batchId
    currentScope = { environmentKey, pipelineKey }
    exportError.value = ''
    // 新批次作废旧导出:旧请求的 finally 不再影响当前状态,界面也不会卡在导出中
    exporting.value = false
    if (!environmentKey || !pipelineKey) {
      state.value = 'idle'
      view.value = null
      errorMessage.value = ''
      errorType.value = ''
      return
    }
    state.value = 'loading'
    view.value = null
    errorMessage.value = ''
    errorType.value = ''
    try {
      const data = await fetchActiveReleaseRaw(environmentKey, pipelineKey)
      if (batch !== batchId) return
      view.value = {
        environmentKey,
        pipelineKey,
        version: data.version,
        snapshotId: data.snapshot_id,
        sourceFreezeRevision: data.source_freeze_revision,
        payloadSha256: data.payload_sha256,
        activeRevision: data.active_revision,
        configs: data.configs,
        activatedAt: data.activated_at || '',
      }
      state.value = 'ready'
    } catch (error) {
      if (batch !== batchId) return
      applyError(error)
    }
  }

  const reset = () => {
    batchId += 1
    currentScope = { environmentKey: '', pipelineKey: '' }
    state.value = 'idle'
    view.value = null
    errorMessage.value = ''
    errorType.value = ''
    exporting.value = false
    exportError.value = ''
  }

  /** 导出当前展示的那份快照;期间切范围/刷新/卸载都不触发下载。 */
  const exportShown = async (): Promise<void> => {
    const snapshot = view.value
    if (!snapshot) return
    const batch = batchId
    const target = {
      environmentKey: snapshot.environmentKey,
      pipelineKey: snapshot.pipelineKey,
      version: snapshot.version,
      snapshotId: snapshot.snapshotId,
    }
    exporting.value = true
    exportError.value = ''
    try {
      const pkg = await exportShownSnapshot(
        target.environmentKey,
        target.pipelineKey,
        target.version,
        target.snapshotId,
      )
      if (batch !== batchId || !isCurrentScope(target.environmentKey, target.pipelineKey)) return
      const url = URL.createObjectURL(pkg.blob)
      try {
        const anchor = document.createElement('a')
        anchor.href = url
        anchor.download = pkg.fileName
        anchor.click()
      } finally {
        URL.revokeObjectURL(url)
      }
    } catch (error) {
      if (batch !== batchId) return
      exportError.value =
        error instanceof ApiError
          ? error.errorType === 'RELEASE_INCOMPLETE'
            ? '该版本的资源不完整,无法导出'
            : error.message || '导出失败'
          : error instanceof Error
            ? error.message
            : '导出失败'
    } finally {
      if (batch === batchId) {
        exporting.value = false
      }
    }
  }

  return {
    state,
    view,
    errorMessage,
    errorType,
    exporting,
    exportError,
    load,
    reset,
    exportShown,
  }
}
