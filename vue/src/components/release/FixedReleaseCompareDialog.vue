<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { ArrowRight, ArrowRightLeft, Loader2 } from 'lucide-vue-next'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { useDialogAttempt } from '@/composables/useDialogAttempt'
import { useReleaseComparison } from '@/composables/useReleaseComparison'
import type { CompareTarget } from '@/lib/api/release-compare'
import type { ReleaseListItem } from '@/lib/api/release'
import ReleaseComparisonList from './ReleaseComparisonList.vue'
import ReleaseComparisonSummary from './ReleaseComparisonSummary.vue'

// §2.1/§2.8: direction and differences first, selection second. Read-only
// historical comparison has no publish primary action. Spectrum token surfaces,
// 8/16/24/32 spacing, existing Select/Dialog focus and reduced-motion behavior.
const props = defineProps<{ open: boolean; environmentKey: string; pipelineKey: string; scopeId: string;
  scopeName: string; items: ReleaseListItem[]; hasMore: boolean; listLoading: boolean; listError: string }>()
const emit = defineEmits<{ 'update:open': [value: boolean]; loadMore: []; publish: [target: CompareTarget] }>()
const baselineId = ref(''), targetId = ref('')
const selection = (id: string) => props.items.find(item => item.meta.snapshot_id === id)?.meta
const baseline = computed(() => selection(baselineId.value)), target = computed(() => selection(targetId.value))
let comparison: ReturnType<typeof useReleaseComparison>
const begin = useDialogAttempt(() => [props.open, props.scopeId, props.environmentKey, props.pipelineKey], () => {
  baselineId.value = ''; targetId.value = ''; comparison?.reset()
})
comparison = useReleaseComparison(() => ({ environmentKey: props.environmentKey, pipelineKey: props.pipelineKey, scopeId: props.scopeId }), begin)
const { result, error, loading, loadingMore, beforeConfigs, afterConfigs, detailsLoading, detailsError } = comparison
watch(() => props.open, open => {
  if (!open) return
  baselineId.value = props.items.find(item => item.is_active)?.meta.snapshot_id ?? props.items[1]?.meta.snapshot_id ?? props.items[0]?.meta.snapshot_id ?? ''
  targetId.value = props.items[0]?.meta.snapshot_id ?? ''
}, { immediate: true })
watch([baselineId, targetId], () => {
  comparison.reset()
  if (props.open && baseline.value && target.value) comparison.startFixed(baseline.value, target.value)
})
function swap() { [baselineId.value, targetId.value] = [targetId.value, baselineId.value] }
function retry() { if (baseline.value && target.value) comparison.startFixed(baseline.value, target.value) }
function publish() {
  if (!target.value || !result.value || error.value || loading.value) return
  emit('publish', { version: target.value.version, snapshot_id: target.value.snapshot_id })
}
</script>

<template>
  <Dialog :open="open" @update:open="value => emit('update:open', value)">
    <DialogContent class="fixed-release-compare" @pointer-down-outside="event => event.preventDefault()">
      <DialogHeader class="fixed-heading"><DialogTitle>版本比较</DialogTitle><DialogDescription>{{ scopeName }} · 从基准版本到目标版本</DialogDescription></DialogHeader>
      <div class="fixed-selectors">
        <div><label id="fixed-baseline-label">基准版本</label><Select v-model="baselineId"><SelectTrigger aria-labelledby="fixed-baseline-label"><SelectValue placeholder="选择基准版本" /></SelectTrigger><SelectContent><SelectItem v-for="item in items" :key="item.meta.snapshot_id" :value="item.meta.snapshot_id">{{ item.meta.version }}</SelectItem></SelectContent></Select></div>
        <Button variant="outline" size="icon" aria-label="交换比较方向" :disabled="!baseline || !target" @click="swap"><ArrowRightLeft :size="18" /></Button>
        <div><label id="fixed-target-label">目标版本</label><Select v-model="targetId"><SelectTrigger aria-labelledby="fixed-target-label"><SelectValue placeholder="选择目标版本" /></SelectTrigger><SelectContent><SelectItem v-for="item in items" :key="item.meta.snapshot_id" :value="item.meta.snapshot_id">{{ item.meta.version }}</SelectItem></SelectContent></Select></div>
      </div>
      <div class="fixed-body">
        <div v-if="hasMore || listError" class="fixed-catalog"><span>{{ listError || '还可加载更早的版本' }}</span><Button variant="outline" size="sm" :disabled="listLoading" @click="emit('loadMore')">{{ listLoading ? '读取中…' : '加载更多版本' }}</Button></div>
        <p v-if="loading" role="status" class="fixed-loading"><Loader2 :size="20" class="animate-spin" />正在比较配置…</p>
        <p v-else-if="error" role="alert" class="text-sm text-destructive">{{ error }} <Button variant="outline" size="sm" @click="retry">重新比较</Button></p>
        <template v-if="result">
          <div class="fixed-direction"><strong>{{ result.baseline?.version }}</strong><ArrowRight :size="20" /><strong>{{ result.target.version }}</strong><span>历史版本对照</span></div>
          <ReleaseComparisonSummary :summary="result.summary" />
          <p v-if="!result.has_changes" class="fixed-empty">无差异</p>
          <ReleaseComparisonList v-else :result="result" :before="beforeConfigs" :after="afterConfigs" :loading="detailsLoading" :error="detailsError" @load="comparison.loadDetails" />
          <div v-if="result.has_more" class="fixed-more"><span>已展示 {{ result.items.length }} / {{ result.total_changes }} 项变更</span><Button variant="outline" size="sm" :disabled="loadingMore" @click="comparison.more">{{ loadingMore ? '读取中…' : '加载更多差异' }}</Button></div>
        </template>
        <p v-else-if="!loading && !error" class="fixed-empty">请选择两个版本进行比较。</p>
      </div>
      <footer class="fixed-footer"><p>此处只比较历史内容，切换生效前需重新审阅当前差异。</p><div><Button variant="outline" @click="emit('update:open', false)">关闭</Button><Button variant="outline" :disabled="!result || !!error || loading || loadingMore" @click="publish">审阅目标版本发布 <ArrowRight :size="16" /></Button></div></footer>
    </DialogContent>
  </Dialog>
</template>
<style scoped>
:global(:root:has(.spectrum-shell) .workspace-dialog.fixed-release-compare) { display: flex; flex-direction: column; gap: 0; width: min(1024px, calc(100vw - 64px)); max-width: none; padding: 0; overflow: hidden; }
.fixed-heading { padding: 24px 32px; }
.fixed-selectors { display: grid; grid-template-columns: minmax(0, 1fr) 40px minmax(0, 1fr); gap: 24px; align-items: end; padding: 0 32px 24px; border-bottom: 1px solid var(--border); }
.fixed-selectors > div { display: grid; gap: 12px; min-width: 0; }
.fixed-selectors label { font-size: 12px; color: var(--muted-foreground); }
.fixed-body { padding: 24px 32px; overflow-y: auto; min-height: 0; display: grid; gap: 24px; overscroll-behavior: contain; }
.fixed-direction { display: flex; gap: 24px; align-items: center; padding: 24px; border-radius: 12px; background: var(--spectrum-material-context); }
.fixed-direction strong { font: 600 24px/1.3 monospace; overflow-wrap: anywhere; min-width: 0; }
.fixed-direction svg { color: var(--spectrum-link); flex-shrink: 0; }
.fixed-direction span { margin-left: auto; white-space: nowrap; font-size: 12px; color: var(--muted-foreground); }
.fixed-catalog, .fixed-more { display: flex; align-items: center; justify-content: center; gap: 16px; font-size: 12px; color: var(--muted-foreground); }
.fixed-loading, .fixed-empty { display: flex; justify-content: center; align-items: center; gap: 12px; padding: 32px; color: var(--muted-foreground); font-size: 14px; }
.fixed-footer { display: flex; align-items: center; justify-content: space-between; gap: 24px; padding: 24px 32px; border-top: 1px solid var(--border); background: var(--card); }
.fixed-footer p { font-size: 12px; color: var(--muted-foreground); line-height: 1.8; max-width: 32em; }
.fixed-footer > div { display: flex; gap: 12px; flex-shrink: 0; }
</style>
