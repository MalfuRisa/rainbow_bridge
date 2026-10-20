<script setup lang="ts">
import { computed, nextTick, ref } from 'vue'
import { Search, Pencil, Trash2, Loader2, Layers, ArrowRight, FileText } from 'lucide-vue-next'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogFooter } from '@/components/ui/dialog'
import { CONFIG_TYPE_META, type ConfigItem } from '@/lib/types'
import ConfigDialog from './ConfigDialog.vue'
import ConfigDetail from './ConfigDetail.vue'
import ConfigTypeIcon from './ConfigTypeIcon.vue'

const props = defineProps<{
  configs: ConfigItem[]; loading: boolean; error?: string | null
  selectedEnvKey: string; selectedPipelineKey: string
  selectedEnvironmentName: string | undefined; selectedPipelineName: string | undefined
  frozen?: boolean; frozenReason?: string
}>()
type ConfigPayload = { name: string; alias: string; type: string; content: string; description: string }
type SaveDone = (ok: boolean, message?: string) => void
const emit = defineEmits<{
  search: [value: string]
  create: [config: ConfigPayload, done: SaveDone]
  update: [config: ConfigPayload & { id: string }, done: SaveDone]
  delete: [id: string, done: SaveDone]
  imagePreview: [url: string, name: string]
  retry: []
}>()
const searchTerm = ref('')
const selectedId = ref('')
const isAddDialogOpen = ref(false)
const editingConfig = ref<ConfigItem | null>(null)
const deleteTarget = ref<ConfigItem | null>(null)
const deleting = ref(false)
const deleteError = ref('')
let editTrigger: HTMLElement | null = null
const hasScope = computed(() => Boolean(props.selectedEnvKey && props.selectedPipelineKey))
const filteredConfigs = computed(() => {
  const term = searchTerm.value.toLowerCase().trim()
  return props.configs.filter(config => !term || `${config.name}\n${config.alias}`.toLowerCase().includes(term))
})
const selectedConfig = computed(() => filteredConfigs.value.find(item => item.id === selectedId.value) || filteredConfigs.value[0])
const detailRegion = ref<HTMLElement | null>(null)

async function selectConfig(id: string) {
  selectedId.value = id
  if (!window.matchMedia('(max-width: 800px)').matches) return
  await nextTick()
  const reduceMotion = window.matchMedia('(prefers-reduced-motion: reduce)').matches
  detailRegion.value?.scrollIntoView({ behavior: reduceMotion ? 'instant' : 'smooth', block: 'start' })
}

function openEdit(config: ConfigItem, event?: MouseEvent) {
  editTrigger = (event?.currentTarget as HTMLElement | null) || document.activeElement as HTMLElement | null
  editingConfig.value = config
}
function closeForm(open: boolean) {
  isAddDialogOpen.value = open
  if (open) return
  editingConfig.value = null
  void nextTick(() => { if (editTrigger?.isConnected) editTrigger.focus(); editTrigger = null })
}
function resetForm() { closeForm(false) }
function createConfig(config: ConfigPayload, done: SaveDone) {
  emit('create', config, (ok, message) => {
    done(ok, message)
    if (ok) void nextTick(() => { selectedId.value = props.configs.find(item => item.alias === config.alias)?.id || '' })
  })
}
function requestDelete(config: ConfigItem) { deleteTarget.value = config; deleteError.value = '' }
function confirmDelete() {
  if (!deleteTarget.value || deleting.value || props.frozen) return
  deleting.value = true
  emit('delete', deleteTarget.value.id, (ok, message) => {
    deleting.value = false
    if (ok) deleteTarget.value = null
    else deleteError.value = message || '删除失败，请重试'
  })
}
</script>

<template>
  <!-- §2.1: selected content first; catalog second. Semantic table rows and
       Pencil/Trash controls retain stable, accessible regression entrypoints. -->
  <section class="config-workspace-section" aria-label="配置列表">
    <div class="config-list-heading"><div><h2>配置列表</h2><span v-if="hasScope">{{ configs.length }} 项配置<span v-if="searchTerm"> · 匹配 {{ filteredConfigs.length }} 项</span></span></div><span v-if="frozen" class="config-readonly-label">已冻结 · 只读</span></div>
    <div class="config-workspace" :class="{ 'has-content': hasScope && !loading && !error && selectedConfig }">
      <div class="config-catalog">
        <div class="config-catalog-tools">
          <label class="config-search"><Search :size="16" aria-hidden="true" /><Input v-model="searchTerm" placeholder="搜索配置..." aria-label="搜索配置名称或别名" :disabled="!hasScope" @update:model-value="emit('search', String($event))" /></label>
          <ConfigDialog :key="editingConfig?.id || 'create'" :open="isAddDialogOpen || Boolean(editingConfig)" :editing-config="editingConfig" :selected-env-key="selectedEnvKey" :selected-pipeline-key="selectedPipelineKey" :selected-environment-name="selectedEnvironmentName" :selected-pipeline-name="selectedPipelineName" :frozen="frozen" :frozen-reason="frozenReason" @update:open="closeForm" @update:editing-config="editingConfig = $event" @create="createConfig" @update="(config, done) => emit('update', config, done)" @reset="resetForm" />
        </div>
        <div v-if="!hasScope" class="config-empty-state"><Layers :size="32" /><h3>请先选择环境和渠道</h3><p>配置按环境和渠道管理。选择工作范围，查看和维护对应内容。</p></div>
        <div v-else-if="loading" class="config-empty-state" role="status"><Loader2 :size="28" class="animate-spin" /><p>正在加载配置…</p></div>
        <div v-else-if="error" class="config-empty-state" role="alert"><FileText :size="28" /><h3>配置加载失败</h3><p>{{ error }}</p><Button variant="outline" @click="emit('retry')">重试</Button></div>
        <div v-else-if="filteredConfigs.length === 0" class="config-empty-state"><Search v-if="searchTerm" :size="28" /><FileText v-else :size="28" /><h3>{{ searchTerm ? '未找到匹配的配置' : '当前环境和渠道下暂无配置' }}</h3><p>{{ searchTerm ? '尝试其他名称或别名，或清空搜索。' : '从第一条配置开始，选择适合内容的配置类型。' }}</p><Button v-if="searchTerm" variant="outline" @click="searchTerm = ''">清空搜索</Button></div>
        <table v-else class="config-catalog-table">
          <thead><tr><th>名称 / 别名</th><th>类型</th><th><span class="sr-only">操作</span></th></tr></thead>
          <tbody><tr v-for="config in filteredConfigs" :key="config.id" :class="{ 'is-selected': selectedConfig?.id === config.id }">
            <td><button class="config-select" :aria-pressed="selectedConfig?.id === config.id" @click="selectConfig(config.id)"><span class="config-item-mark"><ConfigTypeIcon :type="config.type" /></span><span><strong>{{ config.name }}</strong><code>{{ config.alias }}</code></span><ArrowRight :size="14" class="config-selected-arrow" aria-hidden="true" /></button></td>
            <td><span class="config-type-label">{{ CONFIG_TYPE_META[config.type]?.label || config.type }}</span></td>
            <td><div class="config-row-actions"><Button variant="ghost" size="icon" :disabled="frozen" :aria-label="`编辑 ${config.name}`" :title="frozen ? `已冻结：${frozenReason || '不可编辑配置'}` : '编辑配置'" @click="openEdit(config, $event)"><Pencil :size="15" /></Button><Button variant="ghost" size="icon" :disabled="loading || frozen" :aria-label="`删除 ${config.name}`" :title="frozen ? `已冻结：${frozenReason || '不可删除配置'}` : '删除配置'" @click="requestDelete(config)"><Trash2 :size="15" /></Button></div></td>
          </tr></tbody>
        </table>
        <div v-if="hasScope && !loading && !error" class="config-catalog-footer"><span>{{ selectedEnvironmentName }} / {{ selectedPipelineName }}</span><span>编辑区</span></div>
      </div>
      <div v-if="hasScope && !loading && !error && selectedConfig" ref="detailRegion" class="config-detail-region"><ConfigDetail :config="selectedConfig" :frozen="frozen" @edit="openEdit" @image-preview="(url, name) => emit('imagePreview', url, name)" /></div>
    </div>
    <Dialog :open="Boolean(deleteTarget)" @update:open="(open) => { if (!open && !deleting) deleteTarget = null }">
      <DialogContent class="spectrum-dialog"><DialogHeader><DialogTitle>删除配置</DialogTitle><DialogDescription>将删除 {{ deleteTarget?.name }}（{{ deleteTarget?.alias }}）。此操作不可撤销，已发布快照不受影响。</DialogDescription></DialogHeader><p v-if="frozen" class="text-sm text-destructive">该范围已冻结，无法删除配置。</p><p v-if="deleteError" role="alert" class="text-sm text-destructive">{{ deleteError }}</p><DialogFooter><Button variant="outline" :disabled="deleting" @click="deleteTarget = null">取消</Button><Button variant="destructive" :disabled="deleting || frozen" @click="confirmDelete">{{ deleting ? '正在删除…' : '确认删除' }}</Button></DialogFooter></DialogContent>
    </Dialog>
  </section>
</template>
