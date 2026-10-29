<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { toast } from 'vue-sonner'
import { Trash2, Loader2 } from 'lucide-vue-next'
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogFooter } from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { ApiError } from '@/lib/api/error'
import { isSessionError } from '@/lib/api/http'
import { noteTooLong, displayNote } from '@/lib/operation-note'
import { createRequestId } from '@/lib/request-id'
import { useDialogAttempt } from '@/composables/useDialogAttempt'
import { useReleaseStore } from '@/stores/release'
import { useAuthStore } from '@/stores/auth'
import type { ReleaseDeletePayload } from '@/lib/api/release'

export interface DeleteTarget {
  scopeId: string
  environmentKey: string; pipelineKey: string; version: string; snapshotId: string
  environmentName?: string; pipelineName?: string
}
const props = defineProps<{ open: boolean; target: DeleteTarget | null }>()
const emit = defineEmits<{ 'update:open': [value: boolean] }>()
const store = useReleaseStore()
const auth = useAuthStore()
const target = ref<DeleteTarget | null>(null)
const reason = ref('')
const submitting = ref(false)
const error = ref('')
const stopped = ref(false)
const pending = ref<Readonly<ReleaseDeletePayload> | null>(null)
const uncertain = ref(false)
const startAttempt = useDialogAttempt(() => [props.open, props.target], () => {
  target.value = props.open && props.target ? { ...props.target } : null
  reason.value = ''; submitting.value = false; error.value = ''; stopped.value = false
  pending.value = null; uncertain.value = false
})
watch(() => props.open, open => {
  if (open && props.target) target.value = { ...props.target }
}, { immediate: true })
const canConfirm = computed(() => uncertain.value && pending.value?.reason === reason.value.trim())
const validTarget = computed(() => {
  const selected = target.value
  if (!selected || store.loading || store.error || store.activeError || !auth.authenticated) return false
  const row = store.items.find(item => item.meta.snapshot_id === selected.snapshotId && item.meta.version === selected.version)
  return Boolean(row && store.canDelete(row))
})
function close() { emit('update:open', false) }
async function submit(confirm = false) {
  if (submitting.value || stopped.value || !target.value || !auth.authenticated) return
  if (confirm && !canConfirm.value) return
  if (!confirm && !validTarget.value) return
  if (noteTooLong(reason.value)) { error.value = '备注最多500字'; return }
  const selected = { ...target.value }
  const current = startAttempt()
  submitting.value = true; error.value = ''; uncertain.value = false
  try {
    const payload = confirm && pending.value ? pending.value : Object.freeze({
      environment_key: selected.environmentKey, pipeline_key: selected.pipelineKey,
      expected_scope_id: selected.scopeId,
      version: selected.version, expected_snapshot_id: selected.snapshotId,
      request_id: createRequestId(), reason: reason.value.trim(),
    })
    pending.value = payload
    const outcome = await store.deleteVersion(payload)
    if (!current()) return
    if (outcome.refreshFailed) {
      store.error = '版本已删除，列表刷新失败，请刷新'
      toast.error(store.error)
    } else {
      toast.success('版本 ' + payload.version + ' 已删除')
    }
    close()
  } catch (cause) {
    if (!current() || isSessionError(cause)) return
    const kind = cause instanceof ApiError ? cause.errorType : ''
    const messages: Record<string, string> = {
      RELEASE_DELETE_ACTIVE: '该版本已生效，请先切换到其他版本',
      RELEASE_SNAPSHOT_MISMATCH: '目标版本信息已变化，请刷新后重新选择',
      RELEASE_REQUEST_CONFLICT: '本次请求信息冲突，请重新发起操作',
      RELEASE_VERSION_DELETED: '该版本已被删除',
      SCOPE_IDENTITY_CONFLICT: '该范围已重新创建，请刷新后重新选择。',
      SCOPE_DELETED: '该范围已删除，请刷新后重新选择。',
      SCOPE_INVALID_REQUEST: '范围信息不完整，请刷新环境数据。',
    }
    error.value = messages[kind] || (cause instanceof Error ? cause.message : '删除失败，请重试')
    const terminal = Boolean(messages[kind]) || (cause instanceof ApiError && cause.code === 404)
    if (terminal) {
      if (cause instanceof ApiError && cause.code === 404) error.value = '目标版本或范围不存在，请刷新后重新选择'
      stopped.value = true; pending.value = null
      await store.refreshScope(selected.environmentKey, selected.pipelineKey)
    } else {
      uncertain.value = Boolean(pending.value) && (!(cause instanceof ApiError) || cause.type === 'network' || cause.code >= 500)
    }
  } finally {
    if (current()) submitting.value = false
  }
}
</script>

<template>
  <!-- §2.1/§2.8: target first, impact second, optional note third; one destructive
       primary action. Existing spectrum tokens, 16/24px grouping, 40px controls. -->
  <Dialog :open="open" :modal="true" @update:open="emit('update:open', $event)">
    <DialogContent class="spectrum-delete-dialog" overlay-class="spectrum-delete-overlay">
      <DialogHeader>
        <DialogTitle class="flex items-center gap-2"><Trash2 :size="20" />删除版本 {{ target?.version }}</DialogTitle>
        <DialogDescription>删除后，固定使用此版本的应用将无法继续读取。底层文件暂时保留，不会释放磁盘空间，版本号也不能重新使用。</DialogDescription>
      </DialogHeader>
      <dl v-if="target" class="spectrum-delete-target">
        <dt>环境</dt><dd>{{ target.environmentName || target.environmentKey }} <code>{{ target.environmentKey }}</code></dd>
        <dt>渠道</dt><dd>{{ target.pipelineName || target.pipelineKey }} <code>{{ target.pipelineKey }}</code></dd>
        <dt>版本</dt><dd><code>{{ target.version }}</code></dd>
      </dl>
      <p v-if="error" class="text-sm text-destructive" role="alert">{{ error }}</p>
      <div class="space-y-2">
        <Label for="delete-reason">备注（选填）</Label>
        <Textarea id="delete-reason" v-model="reason" :disabled="submitting || stopped" :rows="2" placeholder="可补充删除说明，最多500字" />
      </div>
      <p v-if="canConfirm" class="text-xs text-muted-foreground" data-testid="delete-pending">删除结果尚未确认。点击“确认结果”核对首次提交；再次删除将视为新操作。备注：{{ displayNote(pending?.reason) }}。</p>
      <DialogFooter>
        <Button variant="outline" @click="close">取消</Button>
        <Button v-if="canConfirm" variant="secondary" :disabled="submitting || !auth.authenticated" @click="submit(true)">确认结果</Button>
        <Button v-if="!stopped" variant="destructive" :disabled="submitting || !validTarget" @click="submit(false)">
          <Loader2 v-if="submitting" :size="16" class="mr-2 animate-spin" />{{ submitting ? '正在删除…' : uncertain ? '按当前备注发起新删除' : '删除版本' }}
        </Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>
</template>
