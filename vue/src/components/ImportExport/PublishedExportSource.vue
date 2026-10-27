<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { Button } from '@/components/ui/button'
import { Select, SelectTrigger, SelectValue, SelectContent, SelectItem } from '@/components/ui/select'
import EnvironmentChannelSelector from '@/components/resources/EnvironmentChannelSelector.vue'
import { useEnvironmentStore } from '@/stores/environment'
import { useScopeSelection } from '@/composables/useScopeSelection'
import { releaseApi, type ReleaseActiveInfo, type ReleaseListItem } from '@/lib/api/release'
import type { ExportRequest } from '@/lib/api/transfer-export'
import { Loader2, RefreshCw } from 'lucide-vue-next'

const props = defineProps<{ source: 'active' | 'version'; refreshKey: number }>()
const emit = defineEmits<{ change: [value: ExportRequest | null] }>()
const environments = useEnvironmentStore()
const { selectedEnvKey, selectedPipelineKey, selectedScopeId, envPipelineModel, initialize } = useScopeSelection(() => environments.environments)
const active = ref<ReleaseActiveInfo | null>(null)
const versions = ref<ReleaseListItem[]>([])
const version = ref('')
const loading = ref(false)
const error = ref('')
const cursor = ref('')
const hasMore = ref(false)
let batch = 0
onMounted(initialize)
onUnmounted(() => { batch++ })
const target = computed<ExportRequest | null>(() => {
  if (loading.value || error.value || !selectedScopeId.value) return null
  const snapshot = props.source === 'active' ? active.value : versions.value.find(item => item.meta.snapshot_id === version.value)?.meta
  if (!snapshot) return null
  const scope = { format: 'zip' as const, environment_key: selectedEnvKey.value, pipeline_key: selectedPipelineKey.value, expected_scope_id: selectedScopeId.value, expected_snapshot_id: snapshot.snapshot_id }
  return props.source === 'active' ? { ...scope, source: 'active' } : { ...scope, source: 'version', version: snapshot.version }
})
watch(target, value => emit('change', value), { immediate: true })
async function load(more = false) {
  const current = ++batch
  const env = selectedEnvKey.value; const pipeline = selectedPipelineKey.value
  loading.value = true; error.value = ''
  if (!more) { active.value = null; versions.value = []; version.value = ''; hasMore.value = false; cursor.value = '' }
  if (!env || !pipeline || !selectedScopeId.value) { loading.value = false; return }
  try {
    if (props.source === 'active') {
      const result = await releaseApi.active(env, pipeline)
      if (current === batch) active.value = result
    } else {
      const result = await releaseApi.list(env, pipeline, more ? cursor.value : '')
      if (current !== batch) return
      versions.value = [...versions.value, ...(result.items || [])]
      hasMore.value = result.has_more; cursor.value = result.next_before_id || ''
    }
  } catch (cause) { if (current === batch) error.value = cause instanceof Error ? cause.message : '版本读取失败' }
  finally { if (current === batch) loading.value = false }
}
watch([selectedEnvKey, selectedPipelineKey, selectedScopeId, () => props.source, () => props.refreshKey], () => { void load() }, { immediate: true })
</script>
<template>
  <div class="space-y-6">
    <EnvironmentChannelSelector v-model="envPipelineModel" />
    <p v-if="loading" class="flex items-center gap-2 text-sm text-muted-foreground" role="status"><Loader2 :size="16" class="animate-spin" />读取版本中…</p>
    <p v-else-if="error" class="text-sm text-destructive" role="alert">{{ error }}</p>
    <template v-else-if="selectedScopeId">
      <div v-if="source === 'active'" class="spectrum-export-version">
        <template v-if="active"><span>当前生效版本</span><strong>{{ active.version }}</strong><p>导出此版本的完整配置与引用资源。</p></template>
        <p v-else>当前范围尚无生效版本，请先在版本发布中切换生效。</p>
      </div>
      <div v-else class="space-y-3">
        <label class="text-sm font-medium" for="export-version">发布版本</label>
        <Select v-model="version"><SelectTrigger id="export-version" aria-label="发布版本"><SelectValue placeholder="选择要导出的版本" /></SelectTrigger><SelectContent><SelectItem v-for="item in versions" :key="item.meta.snapshot_id" :value="item.meta.snapshot_id">{{ item.meta.version }}{{ item.is_active ? ' · 当前生效' : '' }}</SelectItem></SelectContent></Select>
        <p v-if="!versions.length" class="text-sm text-muted-foreground">当前范围暂无可导出的发布版本。</p>
        <Button v-if="hasMore" variant="outline" size="sm" @click="load(true)">加载更多版本</Button>
      </div>
    </template>
    <Button variant="ghost" size="sm" :disabled="loading" @click="load()"><RefreshCw :size="14" class="mr-2" />刷新版本</Button>
  </div>
</template>
