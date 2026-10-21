<script setup lang="ts">
import { useDialogHistory } from '@/composables/useDialogHistory'
import { displayNote } from '@/lib/operation-note'
import { History } from 'lucide-vue-next'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { useReleaseStore } from '@/stores/release'
import type { ReleaseEventItem } from '@/lib/api/release'

const props = defineProps<{
  open: boolean
  environmentKey: string
  pipelineKey: string
}>()

const emit = defineEmits<{
  (e: 'update:open', value: boolean): void
}>()

const releaseStore = useReleaseStore()

const { items, hasMore, nextBefore, loading, errorMessage, load } = useDialogHistory<ReleaseEventItem>(
  () => [props.open, props.environmentKey, props.pipelineKey, releaseStore.contentEpoch] as const, (env, pipeline, before) => releaseStore.history(env, pipeline, before),
)
const formatTime = (value: string) => value ? new Date(value).toLocaleString() : '时间待确认'
const eventLabels = new Map([
  ['create', '创建版本'], ['activate', '切换生效版本'], ['delete', '删除版本'],
])
const eventLabel = (kind: string) => eventLabels.get(kind) || '未知操作'
</script>

<template>
  <Dialog :open="open" @update:open="(value) => emit('update:open', value)">
    <DialogContent class="max-w-2xl max-h-[85vh] overflow-y-auto rounded-lg border-0 shadow-2xl">
      <DialogHeader>
        <DialogTitle class="text-xl font-semibold flex items-center gap-2">
          <History class="w-5 h-5" />
          版本操作记录
        </DialogTitle>
        <DialogDescription class="text-muted-foreground font-mono">
          {{ environmentKey }}/{{ pipelineKey }}
        </DialogDescription>
      </DialogHeader>

      <div class="space-y-3 py-2">
        <p v-if="errorMessage" class="text-sm text-destructive">{{ errorMessage }}</p>
        <p v-else-if="!loading && items.length === 0" class="text-sm text-muted-foreground py-6 text-center">暂无操作记录</p>

        <div v-for="item in items" :key="item.event_id" class="rounded-md border p-3 text-sm space-y-1">
          <div class="flex items-center gap-2 flex-wrap">
            <Badge :variant="item.event_type === 'activate' ? 'default' : 'secondary'" class="px-2 py-0.5 text-xs">
              {{ eventLabel(item.event_type) }}
            </Badge>
            <span class="font-mono">{{ item.version }}</span>
            <span class="text-xs text-muted-foreground ml-auto">{{ formatTime(item.created_at) }}</span>
          </div>
          <div class="text-muted-foreground">备注：{{ displayNote(item.reason) }}</div>
          <div class="text-xs text-muted-foreground">操作者：{{ item.actor }}</div>
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
