<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Download, Loader2, Check, RefreshCw } from 'lucide-vue-next'
import ExportTreeSelect from './ExportTreeSelect.vue'
import FormatSelector from './FormatSelector.vue'
import PublishedExportSource from './PublishedExportSource.vue'
import type { ExportSelection, ExportTreeEnvironment } from '@/lib/api/transfer'
import { transferBytes, type ExportFormat, type ExportSource, type ExportRequest } from '@/lib/api/transfer-export'
import { useTransferExport } from '@/composables/useTransferExport'

const props = defineProps<{ exportTree: ExportTreeEnvironment[]; isLoadingTree: boolean }>()
const emit = defineEmits<{ 'load-tree': [] }>()
const source = ref<ExportSource>('editor')
const format = ref<ExportFormat>('zip')
const selections = ref<ExportSelection[]>([])
const published = ref<ExportRequest | null>(null)
const refreshKey = ref(0)
const sources = [
  { value: 'editor' as const, label: '编辑区', description: '尚未发布的编辑区内容，可勾选多个范围。' },
  { value: 'active' as const, label: '当前生效版本', description: '当前生效的完整快照。' },
  { value: 'version' as const, label: '指定发布版本', description: '选择一个发布版本，包含尚未生效的版本。' },
]
const labels = { editor: '编辑区', active: '当前生效版本', version: '指定发布版本' }
const treeIdentity = computed(() => JSON.stringify(props.exportTree.map(env => [env.environment_id, env.pipelines.map(pipe => pipe.scope_id)])))
watch(treeIdentity, () => { selections.value = [] })
watch(source, () => { published.value = null }, { flush: 'sync' })
const request = computed<ExportRequest | null>(() => {
  if (source.value === 'editor') return selections.value.length ? { source: 'editor', format: format.value, selections: selections.value } : null
  return published.value?.source === source.value ? { ...published.value, format: format.value } : null
})
const exportState = reactive(useTransferExport(request))
function refresh() { if (source.value === 'editor') { emit('load-tree'); void exportState.loadPreview() } else refreshKey.value++ }
const notice = (warning: string) => warning.startsWith('external_references_not_included') ? '外部地址不包含在包内，导入后仍需外部地址可用。' : warning
</script>
<template>
  <!-- §2.1/§3.6: source → target → resolved preview → download. One primary action;
       existing Spectrum materials, 16/24 spacing and semantic warning tokens. -->
  <div class="spectrum-transfer-layout">
    <div class="spectrum-transfer-content space-y-6">
      <Card class="spectrum-panel">
        <CardHeader class="spectrum-panel-header"><CardTitle class="text-lg">导出来源</CardTitle></CardHeader>
        <CardContent class="spectrum-panel-content">
          <div class="spectrum-export-sources" role="group" aria-label="导出来源">
            <button v-for="item in sources" :key="item.value" type="button" :aria-pressed="source === item.value" @click="source = item.value"><strong>{{ item.label }}</strong><span>{{ item.description }}</span></button>
          </div>
        </CardContent>
      </Card>
      <Card class="spectrum-panel">
        <CardHeader class="spectrum-panel-header"><CardTitle class="text-lg">{{ source === 'editor' ? '选择编辑区内容' : '选择导出范围与版本' }}</CardTitle></CardHeader>
        <CardContent class="spectrum-panel-content">
          <ExportTreeSelect v-if="source === 'editor'" :key="treeIdentity" :data="exportTree" :loading="isLoadingTree" @change="selections = $event" />
          <PublishedExportSource v-else :source="source" :refresh-key="refreshKey" @change="published = $event" />
        </CardContent>
      </Card>
      <FormatSelector :format="format" @change="format = $event" />
    </div>
    <div class="spectrum-transfer-aside space-y-6">
      <Card class="spectrum-panel spectrum-transfer-summary">
        <CardHeader class="spectrum-panel-header"><p class="spectrum-eyebrow">TRANSFER / 迁移包</p><CardTitle class="text-lg">导出预览</CardTitle></CardHeader>
        <CardContent class="spectrum-panel-content space-y-6">
          <p v-if="exportState.previewing" role="status" class="text-sm text-muted-foreground flex items-center gap-2"><Loader2 :size="16" class="animate-spin" />核对导出内容…</p>
          <p v-else-if="exportState.error" role="alert" class="text-sm text-destructive">{{ exportState.error }}</p>
          <template v-else-if="exportState.preview">
            <div class="spectrum-export-version"><span>{{ labels[exportState.preview.resolved_source.kind] }}</span><strong>{{ exportState.preview.resolved_source.version || '当前编辑内容' }}</strong></div>
            <dl class="spectrum-export-counts"><div><dt>配置</dt><dd>{{ exportState.preview.counts.configs }}</dd></div><div><dt>资源</dt><dd>{{ exportState.preview.counts.assets }}</dd></div><div><dt>预计大小</dt><dd>{{ transferBytes(exportState.preview.estimated_bytes || exportState.preview.counts.bytes) }}</dd></div></dl>
            <p v-for="scope in exportState.preview.resolved_source.scopes" :key="scope.scope_id" class="text-xs text-muted-foreground">{{ scope.environment_name || scope.environment_key }} / {{ scope.pipeline_name || scope.pipeline_key }}</p>
            <p v-if="source === 'editor'" class="text-xs text-muted-foreground">预览不锁定编辑区，下载内容以实际导出时为准。</p>
            <ul v-if="exportState.preview.warnings?.length" class="spectrum-export-warnings"><li v-for="warning in exportState.preview.warnings" :key="warning">{{ notice(warning) }}</li></ul>
          </template>
          <p v-else class="text-sm text-muted-foreground">选择内容后，自动核对来源与数量。</p>
          <div class="space-y-3 border-t pt-4">
            <Button class="w-full" :disabled="!exportState.canExport" @click="exportState.download"><Loader2 v-if="exportState.exporting" :size="16" class="animate-spin mr-2" /><Download v-else :size="16" class="mr-2" />{{ exportState.exporting ? '导出中…' : '导出数据' }}</Button>
            <Button v-if="exportState.error" variant="outline" class="w-full" @click="refresh"><RefreshCw :size="14" class="mr-2" />刷新并核对来源</Button>
            <Button v-else-if="request" variant="ghost" class="w-full" :disabled="exportState.previewing || exportState.exporting" @click="exportState.loadPreview">重新预览</Button>
          </div>
          <p v-if="exportState.success" role="status" class="spectrum-result-success flex items-center gap-2 text-sm"><Check :size="16" />已发起迁移包下载</p>
        </CardContent>
      </Card>
      <div class="spectrum-transfer-package-note"><strong>导入后进入编辑区</strong><p>迁移包不会自动锁版、发布或切换生效。业务静态包用于部署，整实例备份用于恢复，三者不可互换。</p><p>导出内容可能含敏感配置，请妥善保管。</p></div>
    </div>
  </div>
</template>
<style scoped>
.spectrum-export-sources { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 12px; }
.spectrum-export-sources button { text-align: left; border: 1px solid var(--border); border-radius: 12px; padding: 16px; background: var(--spectrum-material-soft); transition: border-color 160ms, background-color 160ms; }
.spectrum-export-sources button[aria-pressed=true] { border-color: var(--spectrum-orange); background: var(--spectrum-caution-bg); box-shadow: inset 0 2px 0 var(--spectrum-orange); }
.spectrum-export-sources button:focus-visible { outline: 2px solid var(--ring); outline-offset: 4px; }
.spectrum-export-sources strong { display: block; font-size: 14px; }
.spectrum-export-sources span { display: block; font-size: 12px; color: var(--muted-foreground); line-height: 1.8; margin-top: 8px; }
.spectrum-export-counts { display: grid; grid-template-columns: 1fr 1fr; gap: 16px; }
.spectrum-export-counts dt { font-size: 12px; color: var(--spectrum-inverse-muted); }.spectrum-export-counts dd { font: 600 20px/1.5 Consolas, monospace; margin-top: 4px; }
.spectrum-export-warnings { padding: 16px; background: var(--spectrum-caution-bg); color: var(--spectrum-caution); border-radius: 8px; font-size: 12px; line-height: 1.8; }
.spectrum-transfer-summary .text-destructive { color: color-mix(in srgb, var(--spectrum-danger) 45%, var(--spectrum-inverse)); }
.spectrum-transfer-package-note { padding: 24px; font-size: 14px; }.spectrum-transfer-package-note p { margin-top: 12px; color: var(--muted-foreground); font-size: 12px; line-height: 1.8; }
</style>


<style>
.spectrum-export-version > span { display: block; color: var(--muted-foreground); font-size: 12px; }
.spectrum-export-version > strong { display: block; margin-top: 8px; font-size: 24px; line-height: 1.5; overflow-wrap: anywhere; }
.spectrum-export-version > p { color: var(--muted-foreground); font-size: 12px; line-height: 1.8; margin-top: 8px; }
.spectrum-transfer-summary .spectrum-export-version > span { color: var(--spectrum-inverse-muted); }
</style>
