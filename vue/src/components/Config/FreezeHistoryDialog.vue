<script setup lang="ts">
import { ref, watch } from 'vue'
import { History } from 'lucide-vue-next'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { freezeApi, type FreezeEventItem } from '@/lib/api/freeze'

const props = defineProps<{
  open: boolean
  environmentKey: string
  pipelineKey: string
}>()

const emit = defineEmits<{
  (e: 'update:open', value: boolean): void
}>()

const items = ref<FreezeEventItem[]>([])
const hasMore = ref(false)
const nextBefore = ref('')
const loading = ref(false)
const errorMessage = ref('')

const formatTime = (value: string) => {
  if (!value) return ''
  const time = new Date(value)
  return Number.isNaN(time.getTime()) ? value : time.toLocaleString()
}

const load = async (beforeEventId = '') => {
  loading.value = true
  errorMessage.value = ''
  try {
    const data = await freezeApi.history(props.environmentKey, props.pipelineKey, 20, beforeEventId)
    items.value = beforeEventId ? [...items.value, ...data.items] : data.items
    hasMore.value = data.has_more
    nextBefore.value = data.next_before_event_id || ''
  } catch (error) {
    errorMessage.value = error instanceof Error ? error.message : '历史加载失败'
  } finally {
    loading.value = false
  }
}

watch(
  () => props.open,
  (open) => {
    if (open) {
      items.value = []
      hasMore.value = false
      nextBefore.value = ''
      load()
    }
  }
)
</script>

<template>
  <Dialog :open="open" @update:open="(value) => emit('update:open', value)">
    <DialogContent class="max-w-2xl max-h-[85vh] overflow-y-auto rounded-lg border-0 shadow-2xl">
      <DialogHeader>
        <DialogTitle class="text-xl font-semibold flex items-center gap-2">
          <History class="w-5 h-5" />
          冻结操作记录
        </DialogTitle>
        <DialogDescription class="text-muted-foreground font-mono">
          {{ environmentKey }}/{{ pipelineKey }}
        </DialogDescription>
      </DialogHeader>

      <div class="space-y-3 py-2">
        <p v-if="errorMessage" class="text-sm text-destructive">{{ errorMessage }}</p>
        <p v-else-if="!loading && items.length === 0" class="text-sm text-muted-foreground py-6 text-center">
          暂无操作记录
        </p>

        <div v-for="item in items" :key="item.event_id" class="rounded-md border p-3 text-sm space-y-1">
          <div class="flex items-center gap-2 flex-wrap">
            <Badge :variant="item.event_type === 'lock' ? 'destructive' : 'secondary'" class="px-2 py-0.5 text-xs">
              {{ item.event_type === 'lock' ? '锁定' : '解除' }}
            </Badge>
            <span class="text-xs text-muted-foreground font-mono">
              {{ item.from_state }} → {{ item.to_state }} · revision {{ item.revision }}
            </span>
            <span class="text-xs text-muted-foreground ml-auto">{{ formatTime(item.created_at) }}</span>
          </div>
          <div class="text-muted-foreground">原因:{{ item.reason || '(未填写)' }}</div>
          <div class="text-xs text-muted-foreground">操作者:{{ item.actor }}</div>
        </div>

        <div v-if="hasMore" class="text-center pt-2">
          <Button variant="outline" size="sm" :disabled="loading" @click="load(nextBefore)">
            {{ loading ? '加载中...' : '加载更早记录' }}
          </Button>
        </div>
      </div>
    </DialogContent>
  </Dialog>
</template>