<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { noteTooLong, displayNote } from '@/lib/operation-note'
import { useDialogAttempt } from '@/composables/useDialogAttempt'
import { toast } from 'vue-sonner'
import { AlertCircle, ShieldAlert } from 'lucide-vue-next'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'

import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { useFreezeStore } from '@/stores/freeze'
import { useAuthStore } from '@/stores/auth'
import { scopeIdFor } from '@/lib/scope'
import { ApiError } from '@/lib/api/error'
import type { FreezeResourceIssue, FreezeStatusData } from '@/lib/api/freeze'

const props = defineProps<{
  open: boolean
  mode: 'lock' | 'unlock'
  environmentKey: string
  pipelineKey: string
  environmentName?: string
  pipelineName?: string
  status: FreezeStatusData | null
}>()

const emit = defineEmits<{
  (e: 'update:open', value: boolean): void
}>()

const freezeStore = useFreezeStore()

// 提交前再次确认的目标范围快照:打开时固定,避免用户切换选择后锁错渠道
const scopeSnapshot = ref({ scopeId: '', environmentKey: '', pipelineKey: '', environmentName: '', pipelineName: '' })
const reason = ref('')
const auth = useAuthStore()
const submitting = ref(false)
const errorMessage = ref('')
const invalidScope = ref(false)
const latestState = ref<{ state: string; revision: string } | null>(null)
const resourceIssues = ref<FreezeResourceIssue[]>([])
const externalReferences = ref<string[]>([])

const startAttempt = useDialogAttempt(() => [props.open, props.environmentKey, props.pipelineKey, props.mode], () => { submitting.value = false; errorMessage.value = ''; reason.value = ''; invalidScope.value = false })

const isLock = computed(() => props.mode === 'lock')
const title = computed(() => (isLock.value ? '锁定配置范围' : '解除配置冻结'))
const expectedRevision = computed(() => {
  if (latestState.value) return latestState.value.revision
  return props.status?.revision ?? '0'
})
const scopeLine = computed(() => {
  const { environmentKey, pipelineKey, environmentName, pipelineName } = scopeSnapshot.value
  const envLabel = environmentName ? `${environmentName}(${environmentKey})` : environmentKey
  const pipelineLabel = pipelineName ? `${pipelineName}(${pipelineKey})` : pipelineKey
  return `${envLabel} / ${pipelineLabel}`
})

watch(
  () => props.open,
  (open) => {
    if (open) {
      scopeSnapshot.value = {
        scopeId: scopeIdFor(props.environmentKey, props.pipelineKey),
        environmentKey: props.environmentKey,
        pipelineKey: props.pipelineKey,
        environmentName: props.environmentName || '',
        pipelineName: props.pipelineName || '',
      }
      reason.value = ''
      submitting.value = false
      errorMessage.value = ''
      latestState.value = null
      resourceIssues.value = []
      externalReferences.value = []
      // 打开时刷新一次最新 revision,降低过期冲突
      freezeStore.loadStatus(props.environmentKey, props.pipelineKey)
    } else {
      // 关闭后清除本次操作说明
      reason.value = ''
    }
  }
)

const close = () => emit('update:open', false)

const describeIssueReason = (value: string) => {
  switch (value) {
    case 'missing_metadata':
      return '资源元数据缺失'
    case 'file_missing':
      return '资源文件缺失或不可读'
    case 'cross_scope_reference':
      return '跨范围资源引用'
    case 'unresolvable_reference':
      return '无法定位的资源引用'
    default:
      return value
  }
}

const handleSubmit = async () => {
  if (submitting.value || invalidScope.value || !auth.authenticated) return
  errorMessage.value = ''
  const trimmedReason = reason.value.trim()
  if (noteTooLong(trimmedReason)) {
    errorMessage.value = '备注最多500字'
    return
  }
  const payload = {
    environment_key: scopeSnapshot.value.environmentKey,
    pipeline_key: scopeSnapshot.value.pipelineKey,
    expected_scope_id: scopeSnapshot.value.scopeId,
    reason: trimmedReason,
    expected_revision: expectedRevision.value,
  }
  const current = startAttempt()
  submitting.value = true
  try {
    if (isLock.value) {
      await freezeStore.lock(payload)
      if (!current()) return
      toast.success('已锁定该范围')
    } else {
      await freezeStore.unlock(payload)
      if (!current()) return
      toast.success('已解除冻结')
    }
    close()
  } catch (error) {
    if (!current()) return
    if (error instanceof ApiError) {
      switch (error.errorType) {
        case 'SCOPE_INVALID_REQUEST':
        case 'SCOPE_IDENTITY_CONFLICT':
        case 'SCOPE_DELETED':
          invalidScope.value = true; errorMessage.value = error.message
          break
        case 'FREEZE_REVISION_CONFLICT':
        case 'FREEZE_STATE_UNCHANGED': {
          const payloadData = (error.payload || {}) as { latest_state?: string; latest_revision?: string }
          latestState.value = {
            state: payloadData.latest_state || '',
            revision: payloadData.latest_revision || '',
          }
          errorMessage.value = `状态已被其他操作更新:${error.message}`
          // 冲突后先重读状态再决定是否重试
          await freezeStore.loadStatus(scopeSnapshot.value.environmentKey, scopeSnapshot.value.pipelineKey)
          break
        }
        case 'FREEZE_RESOURCE_PRECHECK_FAILED': {
          const payloadData = (error.payload || {}) as { resource_issues?: FreezeResourceIssue[]; external_references?: string[] }
          resourceIssues.value = payloadData.resource_issues || []
          externalReferences.value = payloadData.external_references || []
          errorMessage.value = '锁定前资源检查未通过,请先处理下列资源'
          break
        }
        default:
          errorMessage.value = error.message || '操作失败'
      }
    } else {
      errorMessage.value = error instanceof Error ? error.message : '操作失败'
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
          <ShieldAlert v-if="isLock" class="w-5 h-5" />
          {{ title }}
        </DialogTitle>
        <DialogDescription class="text-muted-foreground">
          <template v-if="isLock">
            锁定后不可修改配置、上传资源或编辑渠道；读取和导出不受影响。
          </template>
          <template v-else>
            解除后可继续修改此范围的配置和资源。
          </template>
        </DialogDescription>
      </DialogHeader>

      <div class="space-y-4 py-2">
        <div class="rounded-md border bg-muted/40 px-3 py-2 text-sm">
          <div class="text-xs text-muted-foreground">目标范围</div>
          <div class="font-mono">{{ scopeLine }}</div>
        </div>

        <div v-if="errorMessage" class="p-3 rounded-md bg-destructive/10 border border-destructive/20">
          <div class="flex items-center gap-2 text-destructive font-medium text-sm">
            <AlertCircle class="w-4 h-4" />
            {{ errorMessage }}
          </div>
        </div>

        <div v-if="resourceIssues.length" class="rounded-md border p-3 text-sm space-y-1">
          <div class="font-medium">资源问题({{ resourceIssues.length }})</div>
          <ul class="list-disc list-inside text-muted-foreground space-y-0.5">
            <li v-for="(issue, index) in resourceIssues" :key="index">
              {{ issue.alias || issue.resource_key || issue.file_id }}:{{ describeIssueReason(issue.reason) }}
            </li>
          </ul>
        </div>
        <p v-if="externalReferences.length" class="text-xs text-muted-foreground">
          包含 {{ externalReferences.length }} 条外部链接，锁版不会保存或校验其内容。
        </p>

        <div class="space-y-2">
          <Label for="freeze-reason" class="text-sm font-medium">备注（选填）</Label>
          <Textarea
            id="freeze-reason"
            :rows="2"
            v-model="reason"
            :placeholder="isLock ? '例如:上线前配置验收完成' : '例如:验收后需要调整配置'"
            class="resize-none"
          />
        </div>

      </div>

      <DialogFooter class="pt-2 border-t border-border">
        <Button variant="outline" class="h-10" @click="close">取消</Button>
        <Button class="h-10" :disabled="submitting || invalidScope || !auth.authenticated" @click="handleSubmit">
          {{ submitting ? '提交中...' : isLock ? '确认锁定' : '确认解除' }}
        </Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>
</template>
