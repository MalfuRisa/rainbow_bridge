<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { toast } from 'vue-sonner'
import { AlertCircle, Lock, PackagePlus } from 'lucide-vue-next'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { useFreezeStore } from '@/stores/freeze'
import { useReleaseStore } from '@/stores/release'
import { useAuthStore } from '@/stores/auth'
import { ApiError } from '@/lib/api/error'
import type { ReleaseCreatePayload, ReleaseResourceIssue } from '@/lib/api/release'

const props = defineProps<{
  open: boolean
  environmentKey: string
  pipelineKey: string
  environmentName?: string
  pipelineName?: string
}>()

const emit = defineEmits<{
  (e: 'update:open', value: boolean): void
  (e: 'created', version: string): void
}>()

const freezeStore = useFreezeStore()
const releaseStore = useReleaseStore()

const scopeSnapshot = ref({ environmentKey: '', pipelineKey: '', environmentName: '', pipelineName: '' })
const version = ref('')
const reason = ref('')
const auth = useAuthStore()
const submitting = ref(false)
const errorMessage = ref('')
const issues = ref<ReleaseResourceIssue[]>([])
const limitInfo = ref<{ item: string; limit: number; actual: number } | null>(null)
/** 首次提交的整份请求:确认已提交结果必须原样重发,业务 payload 与请求标识保持不变。 */
const pendingPayload = ref<ReleaseCreatePayload | null>(null)
const retryAvailable = ref(false)

const freezeStatus = computed(() => freezeStore.statusFor(scopeSnapshot.value.environmentKey, scopeSnapshot.value.pipelineKey))
const frozen = computed(() => freezeStatus.value?.state === 'locked')
const freezeRevision = computed(() => freezeStatus.value?.revision ?? '0')
const scopeLine = computed(() => {
  const { environmentKey, pipelineKey, environmentName, pipelineName } = scopeSnapshot.value
  return `${environmentName || environmentKey}(${environmentKey}) / ${pipelineName || pipelineKey}(${pipelineKey})`
})
/** 表单是否仍然等于首次提交的请求(范围/版本/原因未改)。 */
const retryMatchesPending = computed(() => {
  const pending = pendingPayload.value
  if (!pending) return false
  return (
    version.value.trim() === pending.version &&
    reason.value.trim() === pending.reason &&
    scopeSnapshot.value.environmentKey === pending.environment_key &&
    scopeSnapshot.value.pipelineKey === pending.pipeline_key
  )
})
/** 只有未改动原请求时才提供“确认结果”;改了字段即视为新操作。 */
const showRetry = computed(() => retryAvailable.value && retryMatchesPending.value)
const pendingFreezeRevision = computed(() => pendingPayload.value?.expected_freeze_revision ?? '')

const resetAttempt = () => {
  version.value = ''
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
        environmentKey: props.environmentKey,
        pipelineKey: props.pipelineKey,
        environmentName: props.environmentName || '',
        pipelineName: props.pipelineName || '',
      }
      resetAttempt()
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
  if (submitting.value || !auth.authenticated) return
  errorMessage.value = ''
  issues.value = []
  limitInfo.value = null
  // 确认已提交结果不依赖当前冻结状态;只有新建才要求已锁定
  const replaying = reuseRequestId && pendingPayload.value !== null
  if (!replaying && !frozen.value) {
    errorMessage.value = '该范围尚未锁定;请先在配置页使用“锁版”完成验收锁定'
    return
  }
  let payload: ReleaseCreatePayload
  if (replaying) {
    // 原样重发首次提交的整份请求(含当时的冻结 revision),不重新读取来源状态
    payload = pendingPayload.value as ReleaseCreatePayload
  } else {
    const trimmedVersion = version.value.trim()
    const trimmedReason = reason.value.trim()
    if (!trimmedVersion) {
      errorMessage.value = '请填写版本号(小写字母/数字开头,可含 . _ -)'
      return
    }
    if (!trimmedReason || trimmedReason.length > 500) {
      errorMessage.value = '请填写 1-500 字符的原因'
      return
    }
    payload = {
      environment_key: scopeSnapshot.value.environmentKey,
      pipeline_key: scopeSnapshot.value.pipelineKey,
      version: trimmedVersion,
      expected_freeze_revision: freezeRevision.value,
      request_id: crypto.randomUUID(),
      reason: trimmedReason,
    }
    pendingPayload.value = payload
  }
  submitting.value = true
  retryAvailable.value = false
  try {
    const result = await releaseStore.create(payload)
    toast.success(result.replayed ? '该请求已提交过:返回原已保存版本' : '版本已保存,尚未切换生效')
    emit('created', payload.version)
    emit('update:open', false)
  } catch (error) {
    if (error instanceof ApiError) {
      const payload = (error.payload || {}) as {
        resource_issues?: ReleaseResourceIssue[]
        limits?: { item: string; limit: number; actual: number }
        latest_state?: string
      }
      switch (error.errorType) {
        case 'RELEASE_SOURCE_NOT_FROZEN':
          errorMessage.value = '该范围尚未锁定或已解锁;请重新锁版后再创建'
          pendingPayload.value = null
          await freezeStore.loadStatus(scopeSnapshot.value.environmentKey, scopeSnapshot.value.pipelineKey)
          break
        case 'RELEASE_SOURCE_REVISION_CONFLICT':
          errorMessage.value = `冻结 revision 已变化:${error.message};已刷新最新状态,请确认后重试`
          pendingPayload.value = null
          await freezeStore.loadStatus(scopeSnapshot.value.environmentKey, scopeSnapshot.value.pipelineKey)
          break
        case 'RELEASE_VERSION_EXISTS':
          errorMessage.value = '该版本号已存在;版本不可覆盖,请更换版本号'
          pendingPayload.value = null
          break
        case 'RELEASE_REQUEST_CONFLICT':
          errorMessage.value = '该请求标识已用于不同内容;已生成新的请求标识,请确认后重试'
          pendingPayload.value = null
          break
        case 'RELEASE_RESOURCE_INVALID':
          issues.value = payload.resource_issues || []
          errorMessage.value = '引用预检未通过,请先处理下列资源'
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
      retryAvailable.value = true
      errorMessage.value = error instanceof Error ? `${error.message}(网络结果不确定时可重试本次请求)` : '创建失败'
    }
  } finally {
    submitting.value = false
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
          从已冻结范围保存一份不可变快照;创建不会自动切换生效版本,也不改变编辑区锁版。
        </DialogDescription>
      </DialogHeader>

      <div class="space-y-4 py-2">
        <div class="rounded-md border bg-muted/40 px-3 py-2 text-sm">
          <div class="text-xs text-muted-foreground">目标范围(打开时固定)</div>
          <div class="font-mono">{{ scopeLine }}</div>
          <div class="text-xs text-muted-foreground mt-1 flex items-center gap-1">
            <Lock v-if="frozen" class="w-3 h-3" />
            {{ frozen ? `编辑区已冻结,来源 revision:${freezeRevision}` : '编辑区未冻结:请先在配置页锁版' }}
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
          确认结果会原样重发首次提交的请求(版本 {{ pendingPayload?.version }},来源 revision {{ pendingFreezeRevision }},原因「{{ pendingPayload?.reason }}」),不要求当前仍处于锁定状态;再次点击主按钮则按当前输入提交为新请求。
        </div>

        <div class="space-y-2">
          <Label for="release-version" class="text-sm font-medium">版本号(不可修改、不可覆盖)</Label>
          <Input id="release-version" class="h-10 font-mono" v-model="version" placeholder="例如 v1.0" />
        </div>

        <div class="space-y-2">
          <Label for="release-reason" class="text-sm font-medium">原因(必填,1-500 字符)</Label>
          <Textarea id="release-reason" :rows="2" class="resize-none" v-model="reason" placeholder="例如:上线前验收完成" />
        </div>

      </div>

      <DialogFooter class="pt-2 border-t border-border">
        <Button variant="outline" class="h-10" @click="emit('update:open', false)">取消</Button>
        <Button v-if="showRetry" variant="secondary" class="h-10" :disabled="submitting || !auth.authenticated" @click="submit(true)">
          确认结果(同一请求标识)
        </Button>
        <Button class="h-10" :disabled="submitting || !auth.authenticated || (!frozen && !showRetry)" @click="submit(false)">
          {{ submitting ? '提交中...' : showRetry ? '按当前输入提交新请求' : '保存版本' }}
        </Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>
</template>