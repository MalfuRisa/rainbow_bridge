<script setup lang="ts">
import { ref, watch } from 'vue'
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

watch(
  () => [props.open, props.version],
  ([open]) => {
    const load = async () => {
      detail.value = null
      errorMessage.value = ''
      if (!open || !props.version) return
      loading.value = true
      try {
        detail.value = await releaseStore.detail(props.environmentKey, props.pipelineKey, props.version)
      } catch (error) {
        errorMessage.value = error instanceof Error ? error.message : '详情加载失败'
      } finally {
        loading.value = false
      }
    }
    load()
  },
  { immediate: true }
)
</script>

<template>
  <Dialog :open="open" @update:open="(value) => emit('update:open', value)">
    <DialogContent class="spectrum-release-detail max-w-3xl max-h-[85vh] overflow-y-auto rounded-lg border-0 shadow-2xl">
      <DialogHeader>
        <DialogTitle class="text-xl font-semibold font-mono">{{ version }}</DialogTitle>
        <DialogDescription class="text-muted-foreground">
          只读快照:内容与资源保存在创建时刻,不随编辑区变化。
        </DialogDescription>
      </DialogHeader>

      <div class="space-y-4 py-2">
        <div v-if="loading" class="flex items-center justify-center py-8">
          <Loader2 class="w-6 h-6 animate-spin text-muted-foreground" />
        </div>
        <p v-else-if="errorMessage" class="text-sm text-destructive">{{ errorMessage }}</p>

        <template v-else-if="detail">
          <div class="spectrum-release-detail-meta rounded-md border p-3 text-sm grid grid-cols-2 gap-2">
            <div><span class="text-muted-foreground">来源冻结 revision:</span><span class="font-mono">{{ detail.meta.source_freeze_revision }}</span></div>
            <div><span class="text-muted-foreground">创建时间:</span>{{ new Date(detail.meta.created_at).toLocaleString() }}</div>
            <div><span class="text-muted-foreground">配置:</span>{{ detail.meta.config_count ?? 0 }} 项</div>
            <div><span class="text-muted-foreground">资源:</span>{{ detail.meta.asset_count ?? 0 }} 项 / {{ detail.meta.asset_bytes ?? 0 }} 字节</div>
            <div class="col-span-2 break-all"><span class="text-muted-foreground">payload 摘要:</span><span class="font-mono text-xs">{{ detail.meta.payload_sha256 }}</span></div>
            <div class="col-span-2"><span class="text-muted-foreground">说明:</span>{{ detail.meta.reason }}</div>
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
