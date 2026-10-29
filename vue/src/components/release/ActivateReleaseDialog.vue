<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { noteTooLong, displayNote } from '@/lib/operation-note'
import { useDialogAttempt } from '@/composables/useDialogAttempt'
import { toast } from 'vue-sonner'
import { AlertCircle, ArrowRightLeft } from 'lucide-vue-next'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'

import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { useReleaseStore } from '@/stores/release'
import type { ReleaseActivatePayload } from '@/lib/api/release'
import { useAuthStore } from '@/stores/auth'
import { ApiError } from '@/lib/api/error'
import { createRequestId } from '@/lib/request-id'

const props = defineProps<{
  open: boolean
  environmentKey: string
  pipelineKey: string
  scopeId: string
  targetVersion: string
}>()

const emit = defineEmits<{
  (e: 'update:open', value: boolean): void
  (e: 'activated', version: string): void
}>()

const releaseStore = useReleaseStore()

const scopeSnapshot = ref({ scopeId: '', environmentKey: '', pipelineKey: '' })
const reason = ref('')
const auth = useAuthStore()
const submitting = ref(false)
const errorMessage = ref('')
const latestActive = ref<{ version: string; revision: string } | null>(null)
/** 首次提交的整份请求:确认已提交结果必须原样重发,业务 payload 与请求标识保持不变。 */
const pendingPayload = ref<ReleaseActivatePayload | null>(null)
const retryAvailable = ref(false)
const targetDeleted = ref(false)

const currentVersion = computed(() => releaseStore.activeVersion || '未设置')
const isSwitchBack = computed(() => releaseStore.isOlderThanActive(props.targetVersion))
const expectedRevision = computed(() => {
  if (latestActive.value) return latestActive.value.revision
  return releaseStore.active?.revision ?? '0'
})

const startAttempt = useDialogAttempt(() => [props.open, props.scopeId, props.environmentKey, props.pipelineKey, props.targetVersion], () => { submitting.value = false; errorMessage.value = ''; pendingPayload.value = null; retryAvailable.value = false; targetDeleted.value = false })

/** 表单是否仍然等于首次提交的请求(目标/原因/范围未改)。 */
const retryMatchesPending = computed(() => {
  const pending = pendingPayload.value
  if (!pending) return false
  return (
    reason.value.trim() === pending.reason &&
    scopeSnapshot.value.environmentKey === pending.environment_key &&
    scopeSnapshot.value.pipelineKey === pending.pipeline_key &&
    props.targetVersion === pending.version
  )
})
/** 只有未改动原请求时才提供“确认结果”;改了字段即视为新操作。 */
const showRetry = computed(() => retryAvailable.value && retryMatchesPending.value)

watch(
  () => props.open,
  (open) => {
    if (open) {
      scopeSnapshot.value = { scopeId: props.scopeId, environmentKey: props.environmentKey, pipelineKey: props.pipelineKey }
      reason.value = ''
      submitting.value = false
      errorMessage.value = ''
      latestActive.value = null
      pendingPayload.value = null
      retryAvailable.value = false
      releaseStore.refreshActive(props.environmentKey, props.pipelineKey)
    } else {
      reason.value = ''
    }
  }
)

const submit = async (reuseRequestId: boolean) => {
  if (submitting.value || targetDeleted.value || !auth.authenticated) return
  errorMessage.value = ''
  const current = startAttempt()
  submitting.value = true
  retryAvailable.value = false
  try {
    let payload: ReleaseActivatePayload
    if (reuseRequestId && pendingPayload.value) {
      // 原样重发首次提交的整份请求(含当时的 expected revision),不重新读取当前状态
      payload = pendingPayload.value
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
        version: props.targetVersion,
        expected_active_revision: expectedRevision.value,
        request_id: createRequestId(),
        reason: trimmedReason,
      }
      pendingPayload.value = payload
    }
    const result = await releaseStore.activate(payload)
    if (!current()) return
    toast.success(
      result.operation.replayed
        ? `已确认此前切换，当前生效版本：${result.active?.version || '未设置'}`
        : `已切换生效版本:${result.active?.version || props.targetVersion}`,
    )
    emit('activated', props.targetVersion)
    emit('update:open', false)
  } catch (error) {
    if (!current()) return
    if (error instanceof ApiError) {
      const payload = (error.payload || {}) as { latest_active?: { version: string; revision: string } }
      switch (error.errorType) {
        case 'SCOPE_INVALID_REQUEST':
        case 'SCOPE_IDENTITY_CONFLICT':
        case 'SCOPE_DELETED':
          targetDeleted.value = true; pendingPayload.value = null; retryAvailable.value = false
          errorMessage.value = error.message
          break
        case 'RELEASE_ACTIVE_REVISION_CONFLICT': {
          const latest = payload.latest_active
          latestActive.value = latest ? { version: latest.version, revision: latest.revision } : null
          errorMessage.value = `生效版本已变化，当前为 ${latest?.version || '未知'}，请核对后重试`
          // 原请求未提交:下次提交按最新 revision 组成新请求
          pendingPayload.value = null
          await releaseStore.refreshActive(scopeSnapshot.value.environmentKey, scopeSnapshot.value.pipelineKey)
          break
        }
        case 'RELEASE_ACTIVE_UNCHANGED':
          errorMessage.value = '目标版本已是当前生效版本'
          pendingPayload.value = null
          await releaseStore.refreshActive(scopeSnapshot.value.environmentKey, scopeSnapshot.value.pipelineKey)
          break
        case 'RELEASE_VERSION_NOT_FOUND':
          errorMessage.value = '目标版本不存在，请刷新列表'
          break
        case 'RELEASE_INCOMPLETE':
          errorMessage.value = `目标版本完整性校验失败:${error.message}`
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
        default:
          errorMessage.value = error.message || '切换失败'
          if (error.type === 'network' || (typeof error.code === 'number' && error.code >= 500)) {
            retryAvailable.value = true
          }
      }
    } else {
      retryAvailable.value = Boolean(pendingPayload.value)
      errorMessage.value = error instanceof Error ? error.message : '切换失败'
    }
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
          <ArrowRightLeft class="w-5 h-5" />
          {{ isSwitchBack ? '切回此版本' : '切换生效版本' }}
        </DialogTitle>
        <DialogDescription class="text-muted-foreground">
          跟随生效版本的读取将改用目标版本；编辑区和冻结状态不变。
        </DialogDescription>
      </DialogHeader>

      <div class="space-y-4 py-2">
        <div class="rounded-md border bg-muted/40 px-3 py-2 text-sm space-y-1">
          <div class="text-xs text-muted-foreground">生效版本切换</div>
          <div class="font-mono">{{ currentVersion }} → {{ targetVersion }}</div>
          <div class="text-xs text-muted-foreground">
            {{ scopeSnapshot.environmentKey }} / {{ scopeSnapshot.pipelineKey }}
          </div>
        </div>

        <div v-if="showRetry" class="rounded-md border bg-muted/40 px-3 py-2 text-xs text-muted-foreground" data-testid="restore-pending">
          切换结果尚未确认。点击“确认结果”核对首次提交；重新切换将视为新操作。备注：{{ displayNote(pendingPayload?.reason) }}。
        </div>

        <div v-if="errorMessage" class="p-3 rounded-md bg-destructive/10 border border-destructive/20">
          <div class="flex items-center gap-2 text-destructive font-medium text-sm">
            <AlertCircle class="w-4 h-4" />
            {{ errorMessage }}
          </div>
        </div>

        <div class="space-y-2">
          <Label for="activate-reason" class="text-sm font-medium">备注（选填）</Label>
          <Textarea id="activate-reason" :rows="2" class="resize-none" v-model="reason" placeholder="例如:切换到新验收版本" />
        </div>

      </div>

      <DialogFooter class="pt-2 border-t border-border">
        <Button variant="outline" class="h-10" @click="emit('update:open', false)">取消</Button>
        <Button v-if="showRetry" variant="secondary" class="h-10" :disabled="targetDeleted || submitting || !auth.authenticated" @click="submit(true)">
          确认结果
        </Button>
        <Button class="h-10" :disabled="targetDeleted || submitting || !auth.authenticated" @click="submit(false)">
          {{ submitting ? '提交中...' : showRetry ? '按当前输入重新切换' : isSwitchBack ? '切回此版本' : '切换生效版本' }}
        </Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>
</template>
