<script setup lang="ts">
import { ref, watch, onUnmounted } from 'vue'
import { displayNote } from '@/lib/operation-note'
import { sessionIdentity, sessionGeneration } from '@/lib/auth/session'
import { ApiError } from '@/lib/api/error'
import { Button } from '@/components/ui/button'
import { FileArchive, Loader2, ShieldCheck } from 'lucide-vue-next'
import { Badge } from '@/components/ui/badge'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { useReleaseStore } from '@/stores/release'
import type { ReleaseDetailData } from '@/lib/api/release'

const props = defineProps<{
  open: boolean
  environmentKey: string
  pipelineKey: string
  version: string
}>()

void props
const emit = defineEmits<{
  (e: 'update:open', value: boolean): void
}>()

const releaseStore = useReleaseStore()

const detail = ref<ReleaseDetailData | null>(null)
const loading = ref(false)
const errorMessage = ref('')

let epoch = 0
async function load() {
  const batch = ++epoch
  const identity = sessionGeneration()
  const { environmentKey, pipelineKey, version, open } = props
  detail.value = null; errorMessage.value = ''; loading.value = false
  if (!open || !version) return
  loading.value = true
  const current = () => batch === epoch && identity === sessionGeneration()
  try {
    const data = await releaseStore.detail(environmentKey, pipelineKey, version)
    if (current()) detail.value = data
  } catch (cause) {
    if (!current()) return
    errorMessage.value = cause instanceof ApiError && cause.code === 410 ? '该版本已被删除' : cause instanceof Error ? cause.message : '详情加载失败'
    if (cause instanceof ApiError && cause.code === 410) await releaseStore.refreshScope(environmentKey, pipelineKey)
  } finally { if (current()) loading.value = false }
}
watch(() => [props.open, props.environmentKey, props.pipelineKey, props.version, releaseStore.contentEpoch, sessionIdentity.value], load, { immediate: true })
onUnmounted(() => { epoch++ })
</script>

<template>
  <Dialog :open="open" @update:open="(value) => emit('update:open', value)">
    <DialogContent class="spectrum-release-detail max-w-3xl max-h-[85vh] overflow-y-auto rounded-lg border-0 shadow-2xl">
      <DialogHeader>
        <DialogTitle class="text-xl font-semibold font-mono">{{ version }}</DialogTitle>
        <DialogDescription class="text-muted-foreground">
          只读快照:内容与资源保存在创建时刻,不随编辑区变化。
        </DialogDescription>
      </DialogHeader><Button variant="outline" size="sm" :disabled="loading" @click="load">刷新详情</Button>

      <div class="space-y-4 py-2">
        <div v-if="loading" class="flex items-center justify-center py-8">
          <Loader2 class="w-6 h-6 animate-spin text-muted-foreground" />
        </div>
        <p v-else-if="errorMessage" class="text-sm text-destructive" role="alert">{{ errorMessage }}</p>

        <template v-else-if="detail">
          <div class="spectrum-release-detail-meta rounded-md border p-3 text-sm grid grid-cols-2 gap-2">
            <div><span class="text-muted-foreground">来源冻结 revision:</span><span class="font-mono">{{ detail.meta.source_freeze_revision }}</span></div>
            <div><span class="text-muted-foreground">创建时间:</span>{{ new Date(detail.meta.created_at).toLocaleString() }}</div>
            <div><span class="text-muted-foreground">配置:</span>{{ detail.meta.config_count ?? 0 }} 项</div>
            <div><span class="text-muted-foreground">资源:</span>{{ detail.meta.asset_count ?? 0 }} 项 / {{ detail.meta.asset_bytes ?? 0 }} 字节</div>
            <div class="col-span-2 break-all"><span class="text-muted-foreground">payload 摘要:</span><span class="font-mono text-xs">{{ detail.meta.payload_sha256 }}</span></div>
            <div class="col-span-2"><span class="text-muted-foreground">备注：</span>{{ displayNote(detail.meta.reason) }}</div>
            <div v-if="(detail.meta.external_references ?? []).length" class="col-span-2">
              <span class="text-muted-foreground">外部地址(内容不受冻结控制):</span>
              <ul class="list-disc list-inside text-xs font-mono">
                <li v-for="ref in detail.meta.external_references ?? []" :key="ref" class="break-all">{{ ref }}</li>
              </ul>
            </div>
          </div>

          <div>
            <div class="font-medium text-sm mb-2 flex items-center gap-2">
              <ShieldCheck class="w-4 h-4" />
              配置({{ (detail.configs ?? []).length }})
            </div>
            <div class="space-y-2">
              <div v-for="cfg in detail.configs ?? []" :key="cfg.resource_key" class="rounded-md border p-3 text-sm space-y-1">
                <div class="flex items-center gap-2 flex-wrap">
                  <span class="font-medium">{{ cfg.name }}</span>
                  <Badge variant="secondary" class="text-xs px-2 py-0.5">{{ cfg.type }}</Badge>
                  <span class="text-xs text-muted-foreground font-mono">{{ cfg.alias }}</span>
                  <Badge v-if="cfg.is_perm" variant="outline" class="text-xs">私有</Badge>
                </div>
                <pre class="text-xs bg-muted/60 rounded p-2 overflow-x-auto whitespace-pre-wrap break-all">{{ cfg.content }}</pre>
                <p v-if="cfg.description" class="text-xs text-muted-foreground">{{ cfg.description }}</p>
              </div>
            </div>
          </div>

          <div>
            <div class="font-medium text-sm mb-2 flex items-center gap-2">
              <FileArchive class="w-4 h-4" />
              资源清单({{ (detail.assets ?? []).length }})
            </div>
            <div class="space-y-1 text-xs">
              <div v-for="asset in detail.assets ?? []" :key="asset.asset_id" class="rounded border px-3 py-2 flex items-center gap-3">
                <span class="font-mono">{{ asset.file_name }}</span>
                <span class="text-muted-foreground">{{ asset.content_type }} · {{ asset.size }} 字节</span>
                <span class="font-mono text-muted-foreground ml-auto">{{ (asset.sha256 ?? '').slice(0, 12) }}…</span>
              </div>
            </div>
          </div>
        </template>
      </div>
    </DialogContent>
  </Dialog>
</template>
