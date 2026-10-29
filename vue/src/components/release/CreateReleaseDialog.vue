<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { noteTooLong, displayNote } from '@/lib/operation-note'
import { useDialogAttempt } from '@/composables/useDialogAttempt'
import { toast } from 'vue-sonner'
import { AlertCircle, Lock, PackagePlus } from 'lucide-vue-next'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { useFreezeStore } from '@/stores/freeze'
import { useReleaseStore } from '@/stores/release'
import { useAuthStore } from '@/stores/auth'
import { ApiError } from '@/lib/api/error'
import { createRequestId } from '@/lib/request-id'
import { operationMessage } from '@/lib/api/messages'
import { pendingReleaseKey, readPendingRelease, writePendingRelease } from '@/composables/releaseReviewPending'
import type { ReleaseCreatePayload, ReleaseResourceIssue } from '@/lib/api/release'

const props = defineProps<{
  open: boolean
  environmentKey: string
  pipelineKey: string
  scopeId: string
  environmentName?: string
  pipelineName?: string
}>()

const emit = defineEmits<{
  (e: 'update:open', value: boolean): void
  (e: 'created', version: string): void
}>()

const freezeStore = useFreezeStore()
const releaseStore = useReleaseStore()

const scopeSnapshot = ref({ scopeId: '', environmentKey: '', pipelineKey: '', environmentName: '', pipelineName: '' })
const reason = ref('')
const auth = useAuthStore()
const submitting = ref(false)
const errorMessage = ref('')
const issues = ref<ReleaseResourceIssue[]>([])
const limitInfo = ref<{ item: string; limit: number; actual: number } | null>(null)
/** 首次提交的整份请求:确认已提交结果必须原样重发,业务 payload 与请求标识保持不变。 */
const pendingPayload = ref<ReleaseCreatePayload | null>(null)
const retryAvailable = ref(false)
const targetDeleted = ref(false)
let storageKey = ''
function savePending(payload: ReleaseCreatePayload | null) {
  writePendingRelease(storageKey, payload ? { kind: 'create', payload } : null)
  pendingPayload.value = payload
}

const freezeStatus = computed(() => freezeStore.statusFor(scopeSnapshot.value.environmentKey, scopeSnapshot.value.pipelineKey))
const frozen = computed(() => freezeStatus.value?.state === 'locked')
const freezeRevision = computed(() => freezeStatus.value?.revision ?? '0')
const scopeLine = computed(() => {
  const { environmentKey, pipelineKey, environmentName, pipelineName } = scopeSnapshot.value
  return `${environmentName || environmentKey}(${environmentKey}) / ${pipelineName || pipelineKey}(${pipelineKey})`
})
const startAttempt = useDialogAttempt(() => [props.open, props.scopeId, props.environmentKey, props.pipelineKey], () => { submitting.value = false; errorMessage.value = ''; pendingPayload.value = null; retryAvailable.value = false; targetDeleted.value = false })

/** 表单是否仍然等于首次提交的请求(范围/版本/原因未改)。 */
const retryMatchesPending = computed(() => {
  const pending = pendingPayload.value
  if (!pending) return false
  return (
    reason.value.trim() === pending.reason &&
    scopeSnapshot.value.environmentKey === pending.environment_key &&
    scopeSnapshot.value.pipelineKey === pending.pipeline_key
  )
})
/** 只有未改动原请求时才提供“确认结果”;改了字段即视为新操作。 */
const showRetry = computed(() => retryAvailable.value && retryMatchesPending.value)

const resetAttempt = () => {
  reason.value = ''
  errorMessage.value = ''
  issues.value = []
  limitInfo.value = null
  pendingPayload.value = null
  retryAvailable.value = false
}

watch(
  () => props.open,
  (open) => {
    if (open) {
      scopeSnapshot.value = {
        scopeId: props.scopeId,
        environmentKey: props.environmentKey,
        pipelineKey: props.pipelineKey,
        environmentName: props.environmentName || '',
        pipelineName: props.pipelineName || '',
      }
      resetAttempt()
      storageKey = pendingReleaseKey(String(auth.user?.id ?? ''), props.scopeId)
      try {
        const saved = readPendingRelease(storageKey, props.scopeId)
        if (saved?.kind === 'create' && saved.payload.environment_key === props.environmentKey && saved.payload.pipeline_key === props.pipelineKey) {
          pendingPayload.value = saved.payload; reason.value = saved.payload.reason; retryAvailable.value = true
        } else if (saved) {
          targetDeleted.value = true; errorMessage.value = '该范围有未确认的发布操作，请前往“发布配置”确认结果。'
        }
      } catch { targetDeleted.value = true; errorMessage.value = '未确认操作无法读取，请刷新版本列表核对结果。' }
      freezeStore.loadStatus(props.environmentKey, props.pipelineKey)
    } else {
      // 关闭后清除本次操作说明
      reason.value = ''
    }
  }
)

const describeIssue = (value: string) => {
  switch (value) {
    case 'missing_metadata':
      return '资源元数据缺失'
    case 'file_missing':
      return '资源文件缺失或不可读'
    case 'cross_scope_reference':
      return '跨范围资源引用'
    case 'unresolvable_reference':
      return '无法安全定位的资源引用(拼接文本中)'
    case 'external_reference':
      return '外部地址(请先改为受管资源)'
    case 'internal_marker':
      return '包含内部保留标记'
    default:
      return value
  }
}

const submit = async (reuseRequestId: boolean) => {
  if (submitting.value || targetDeleted.value || !auth.authenticated) return
  errorMessage.value = ''
  issues.value = []
  limitInfo.value = null
  // 确认已提交结果不依赖当前冻结状态;只有新建才要求已锁定
  const replaying = reuseRequestId && pendingPayload.value !== null
  if (!replaying && !frozen.value) {
    errorMessage.value = '该范围尚未锁定;请先在配置页使用“锁版”完成验收锁定'
    return
  }
  const current = startAttempt()
  submitting.value = true
  retryAvailable.value = false
  try {
    let payload: ReleaseCreatePayload
    if (replaying) {
      // 原样重发首次提交的整份请求(含当时的冻结 revision),不重新读取来源状态
      payload = pendingPayload.value as ReleaseCreatePayload
    } else {
      const trimmedReason = reason.value.trim()
      if (noteTooLong(trimmedReason)) {
        errorMessage.value = '备注最多500字'
        return
      }
      payload = {
        environment_key: scopeSnapshot.value.environmentKey,
        pipeline_key: scopeSnapshot.value.pipelineKey,
        expected_scope_id: scopeSnapshot.value.scopeId,
        expected_freeze_revision: freezeRevision.value,
        request_id: createRequestId(),
        reason: trimmedReason,
      }
    }
    savePending(payload)
    const result = await releaseStore.create(payload)
    if (!current()) return
    if (!result.meta?.version || !result.meta.snapshot_id || result.meta.scope_id !== props.scopeId ||
        (payload.version && payload.version !== result.meta.version)) throw new Error('创建响应不完整，请确认原请求结果')
    savePending(null)
    toast.success(result.replayed ? '已确认：版本已创建' : '版本已创建，尚未生效')
    emit('created', result.meta.version)
    emit('update:open', false)
  } catch (error) {
    if (!current()) return
    if (error instanceof ApiError) {
      const payload = (error.payload || {}) as {
        resource_issues?: ReleaseResourceIssue[]
        limits?: { item: string; limit: number; actual: number }
        latest_state?: string
      }
      switch (error.errorType) {
        case 'RELEASE_VERSION_AUTO_ASSIGNED':
        case 'RELEASE_VERSION_SEQUENCE_EXHAUSTED':
          errorMessage.value = operationMessage(error.errorType, error.message)
          savePending(null)
          break
        case 'SCOPE_INVALID_REQUEST':
        case 'SCOPE_IDENTITY_CONFLICT':
        case 'SCOPE_DELETED':
          targetDeleted.value = true; pendingPayload.value = null; retryAvailable.value = false
          errorMessage.value = error.message
          break
        case 'RELEASE_SOURCE_NOT_FROZEN':
          errorMessage.value = '该范围尚未锁定或已解锁;请重新锁版后再创建'
          pendingPayload.value = null
          await freezeStore.loadStatus(scopeSnapshot.value.environmentKey, scopeSnapshot.value.pipelineKey)
          break
        case 'RELEASE_SOURCE_REVISION_CONFLICT':
          errorMessage.value = '锁版状态已变化，请核对后重试'
          pendingPayload.value = null
          await freezeStore.loadStatus(scopeSnapshot.value.environmentKey, scopeSnapshot.value.pipelineKey)
          break
        case 'RELEASE_VERSION_EXISTS':
          errorMessage.value = '版本号已使用，已删版本号也不能复用'
          pendingPayload.value = null
          break
        case 'RELEASE_VERSION_DELETED':
          targetDeleted.value = true
          pendingPayload.value = null
          retryAvailable.value = false
          errorMessage.value = '该版本已被删除'
          await releaseStore.refreshScope(scopeSnapshot.value.environmentKey, scopeSnapshot.value.pipelineKey)
          break
        case 'RELEASE_REQUEST_CONFLICT':
          errorMessage.value = '提交内容冲突，请重新发起操作'
          pendingPayload.value = null
          break
        case 'RELEASE_RESOURCE_INVALID':
          issues.value = payload.resource_issues || []
          errorMessage.value = '请先处理以下资源问题'
          break
        case 'RELEASE_LIMIT_EXCEEDED':
          limitInfo.value = payload.limits || null
          errorMessage.value = error.message
          break
        case 'RELEASE_STORAGE_UNSUPPORTED':
          errorMessage.value = '当前源存储为 MinIO,本版不支持创建快照'
          break
        default:
          errorMessage.value = error.message || '创建失败'
          // 网络结果不确定或写协调 503:允许以同一请求标识确认结果,不悄悄重做
          if (error.type === 'network' || (typeof error.code === 'number' && error.code >= 500)) {
            retryAvailable.value = true
          }
      }
    } else {
      retryAvailable.value = Boolean(pendingPayload.value)
      errorMessage.value = error instanceof Error ? error.message : '创建失败'
    }
    if (current() && error instanceof ApiError && error.code >= 400 && error.code < 500) savePending(null)
  } finally {
    if (current()) submitting.value = false
  }
}
</script>

<template>
  <Dialog :open="open" @update:open="(value) => emit('update:open', value)">
    <DialogContent class="max-w-lg rounded-lg border-0 shadow-2xl">
      <DialogHeader>
        <DialogTitle class="text-xl font-semibold flex items-center gap-2">
          <PackagePlus class="w-5 h-5" />
          创建版本
        </DialogTitle>
        <DialogDescription class="text-muted-foreground">
          保存当前冻结内容，创建后需切换生效。
        </DialogDescription>
      </DialogHeader>

      <div class="space-y-4 py-2">
        <div class="rounded-md border bg-muted/40 px-3 py-2 text-sm">
          <div class="text-xs text-muted-foreground">目标范围</div>
          <div class="font-mono">{{ scopeLine }}</div>
          <div class="text-xs text-muted-foreground mt-1 flex items-center gap-1">
            <Lock v-if="frozen" class="w-3 h-3" />
            {{ frozen ? '编辑区已冻结' : '编辑区未冻结：请先在配置页锁版' }}
          </div>
        </div>

        <div v-if="errorMessage" class="p-3 rounded-md bg-destructive/10 border border-destructive/20">
          <div class="flex items-center gap-2 text-destructive font-medium text-sm">
            <AlertCircle class="w-4 h-4" />
            {{ errorMessage }}
          </div>
        </div>

        <div v-if="limitInfo" class="text-xs text-muted-foreground">
          超出限额:{{ limitInfo.item }}(上限 {{ limitInfo.limit }},实际 {{ limitInfo.actual }})
        </div>

        <div v-if="issues.length" class="rounded-md border p-3 text-sm space-y-1">
          <div class="font-medium">资源问题({{ issues.length }})</div>
          <ul class="list-disc list-inside text-muted-foreground space-y-0.5">
            <li v-for="(issue, index) in issues" :key="index">
              {{ issue.alias || issue.resource_key || issue.file_id }}:{{ describeIssue(issue.reason) }}
            </li>
          </ul>
        </div>

        <div v-if="showRetry" class="rounded-md border bg-muted/40 px-3 py-2 text-xs text-muted-foreground" data-testid="restore-pending">
          创建结果尚未确认。点击“确认结果”核对首次提交。{{ pendingPayload?.version ? `原版本：${pendingPayload.version}；` : '版本号由系统生成；' }}备注：{{ displayNote(pendingPayload?.reason) }}。
        </div>

        <p class="text-sm text-muted-foreground">版本号由系统生成。</p>

        <div class="space-y-2">
          <Label for="release-reason" class="text-sm font-medium">备注（选填）</Label>
          <Textarea id="release-reason" :rows="2" class="resize-none" v-model="reason" placeholder="例如:上线前验收完成" />
        </div>

      </div>

      <DialogFooter class="pt-2 border-t border-border">
        <Button variant="outline" class="h-10" @click="emit('update:open', false)">取消</Button>
        <Button v-if="showRetry" variant="secondary" class="h-10" :disabled="targetDeleted || submitting || !auth.authenticated" @click="submit(true)">
          确认结果
        </Button>
        <Button class="h-10" :disabled="targetDeleted || submitting || !auth.authenticated || (!frozen && !showRetry)" @click="submit(false)">
          {{ submitting ? '提交中...' : showRetry ? '按当前输入重新创建' : '保存版本' }}
        </Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>
</template>
