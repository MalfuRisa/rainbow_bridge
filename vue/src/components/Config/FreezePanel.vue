<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { displayNote } from '@/lib/operation-note'
import { Lock, LockOpen, History, ShieldCheck, PackageCheck } from 'lucide-vue-next'
import { RouterLink } from 'vue-router'
import { Button } from '@/components/ui/button'
import { useAuthStore } from '@/stores/auth'
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
const auth = useAuthStore()

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
  const reason = `，备注：${displayNote(status.value.last_reason)}`
  const by = status.value.last_changed_by ? `(${status.value.last_changed_by})` : ''
  return `${rendered}${by}${reason}`
})
</script>

<template>
  <div class="config-freeze-panel" :class="{ 'is-frozen': locked }">
    <div class="flex items-center gap-3 min-w-0">
      <ShieldCheck class="w-5 h-5 shrink-0" />
      <div class="min-w-0">
        <div class="flex items-center gap-2">
          <span class="font-medium text-sm">
            {{ hasScope ? (locked ? '已冻结' : status ? '可编辑' : '状态待确认') : '未选择范围' }}
          </span>
          <span v-if="hasScope" class="text-xs text-muted-foreground font-mono truncate">
            {{ environmentKey }}/{{ pipelineKey }}
          </span>
        </div>
        <p v-if="locked && lastChangeText" class="text-xs mt-0.5 config-freeze-reason">
          最近操作:{{ lastChangeText }}
        </p>
        <p v-else-if="!locked && hasScope" class="text-xs mt-0.5 config-freeze-reason">
          冻结后本范围的配置增删改、资源上传与渠道编辑将被拒绝;读取、预览与导出不受影响。
        </p>
      </div>
    </div>

    <div class="config-freeze-actions">
      <Button v-if="!locked" size="sm" variant="outline" :disabled="!hasScope || !auth.authenticated" @click="openLock">
        <Lock class="w-4 h-4 mr-1" />
        锁版
      </Button>
      <Button v-else size="sm" variant="outline" :disabled="!auth.authenticated" @click="openUnlock">
        <LockOpen class="w-4 h-4 mr-1" />
        解除冻结
      </Button>
      <Button size="sm" variant="ghost" :disabled="!hasScope" @click="historyOpen = true">
        <History class="w-4 h-4 mr-1" />
        操作记录
      </Button>
      <RouterLink
        v-if="hasScope"
        :to="{ path: '/releases', query: { env: environmentKey, pipeline: pipelineKey } }"
      >
        <Button size="sm" variant="ghost">
          <PackageCheck class="w-4 h-4 mr-1" />
          版本发布
        </Button>
      </RouterLink>
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
