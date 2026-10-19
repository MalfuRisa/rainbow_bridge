<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { Lock, LockOpen, History, ShieldCheck } from 'lucide-vue-next'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { useFreezeStore } from '@/stores/freeze'
import FreezeTransitionDialog from './FreezeTransitionDialog.vue'
import FreezeHistoryDialog from './FreezeHistoryDialog.vue'

const props = defineProps<{
  environmentKey: string
  pipelineKey: string
  environmentName?: string
  pipelineName?: string
}>()

const freezeStore = useFreezeStore()
const transitionOpen = ref(false)
const transitionMode = ref<'lock' | 'unlock'>('lock')
const historyOpen = ref(false)

const status = computed(() => freezeStore.statusFor(props.environmentKey, props.pipelineKey))
const locked = computed(() => status.value?.state === 'locked')
const hasScope = computed(() => !!props.environmentKey && !!props.pipelineKey)
const adminConfigured = computed(() => status.value?.admin_configured ?? freezeStore.adminConfigured)

watch(
  () => [props.environmentKey, props.pipelineKey],
  ([env, pipeline]) => {
    if (env && pipeline) {
      freezeStore.loadStatus(env, pipeline)
    }
  },
  { immediate: true }
)

const openLock = () => {
  transitionMode.value = 'lock'
  transitionOpen.value = true
}

const openUnlock = () => {
  transitionMode.value = 'unlock'
  transitionOpen.value = true
}

const lastChangeText = computed(() => {
  if (!status.value?.last_changed_at) return ''
  const time = new Date(status.value.last_changed_at)
  const rendered = Number.isNaN(time.getTime()) ? status.value.last_changed_at : time.toLocaleString()
  const reason = status.value.last_reason ? `,原因:${status.value.last_reason}` : ''
  const by = status.value.last_changed_by ? `(${status.value.last_changed_by})` : ''
  return `${rendered}${by}${reason}`
})
</script>

<template>
  <div class="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-3 rounded-lg border bg-card px-4 py-3">
    <div class="flex items-center gap-3 min-w-0">
      <ShieldCheck class="w-5 h-5 shrink-0" :class="locked ? 'text-destructive' : 'text-emerald-600'" />
      <div class="min-w-0">
        <div class="flex items-center gap-2">
          <span class="font-medium text-sm">
            {{ hasScope ? (locked ? '已冻结' : '可编辑') : '未选择范围' }}
          </span>
          <Badge v-if="locked" variant="destructive" class="px-2 py-0.5 text-xs">冻结中</Badge>
          <span v-if="hasScope" class="text-xs text-muted-foreground font-mono truncate">
            {{ environmentKey }}/{{ pipelineKey }}
          </span>
        </div>
        <p v-if="locked && lastChangeText" class="text-xs text-muted-foreground mt-0.5 truncate">
          最近操作:{{ lastChangeText }}
        </p>
        <p v-else-if="!locked && hasScope" class="text-xs text-muted-foreground mt-0.5">
          冻结后本范围的配置增删改、资源上传与渠道编辑将被拒绝;读取、预览与导出不受影响。
        </p>
      </div>
    </div>

    <div class="flex items-center gap-2 shrink-0">
      <span v-if="hasScope && !adminConfigured" class="text-xs text-amber-600 dark:text-amber-500">
        服务端未配置管理令牌,冻结操作不可用
      </span>
      <Button v-if="!locked" size="sm" variant="outline" :disabled="!hasScope || !adminConfigured" @click="openLock">
        <Lock class="w-4 h-4 mr-1" />
        锁版
      </Button>
      <Button v-else size="sm" variant="outline" :disabled="!adminConfigured" @click="openUnlock">
        <LockOpen class="w-4 h-4 mr-1" />
        解除冻结
      </Button>
      <Button size="sm" variant="ghost" :disabled="!hasScope" @click="historyOpen = true">
        <History class="w-4 h-4 mr-1" />
        操作记录
      </Button>
    </div>

    <FreezeTransitionDialog
      :open="transitionOpen"
      :mode="transitionMode"
      :environment-key="environmentKey"
      :pipeline-key="pipelineKey"
      :environment-name="environmentName"
      :pipeline-name="pipelineName"
      :status="status"
      @update:open="transitionOpen = $event"
    />
    <FreezeHistoryDialog
      :open="historyOpen"
      :environment-key="environmentKey"
      :pipeline-key="pipelineKey"
      @update:open="historyOpen = $event"
    />
  </div>
</template>