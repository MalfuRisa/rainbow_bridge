<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { Copy, Check, RefreshCw, Download, Loader2, AlertTriangle, PackageOpen } from 'lucide-vue-next'
import SpectrumImagePreview from '@/components/spectrum/SpectrumImagePreview.vue'
import ConfigContentPreview from '@/components/Config/ConfigContentPreview.vue'
import { useRuntimeOverview } from '@/composables/useRuntimeOverview'
import { useScopeSelection } from '@/composables/useScopeSelection'
import { useActiveRelease } from '@/composables/useActiveRelease'
import { CONFIG_TYPE_META, type ConfigType } from '@/lib/types'
import { toast } from 'vue-sonner'
import type { ActiveReleaseView } from '@/composables/useActiveRelease'

const emit = defineEmits<{ viewChange: [value: ActiveReleaseView | null] }>()

const environmentStore = useRuntimeOverview()
const { state, view, errorMessage, exporting, exportError, load, reset, exportShown } = useActiveRelease()

const { selectedEnvKey: selectedEnv, selectedPipelineKey: selectedPipeline, selectedScopeId, initialize } = useScopeSelection(() => environmentStore.environments)
const copiedId = ref<string | null>(null)
const imagePreview = ref<{ url: string; name: string } | null>(null)

const environments = computed(() => environmentStore.environments)
const selectedEnvData = computed(() => environments.value.find((env) => env.key === selectedEnv.value))
const pipelines = computed(() => selectedEnvData.value?.pipelines || [])
const hasScope = computed(() => Boolean(selectedEnv.value && selectedPipeline.value))

/** 当前选择器显示的名称(不使用快照中的历史名称改写选择器)。 */
const scopeLabel = computed(() => {
  const envName = selectedEnvData.value?.name || selectedEnv.value
  const pipelineName = pipelines.value.find((item) => item.key === selectedPipeline.value)?.name || selectedPipeline.value
  return `${envName} / ${pipelineName}`
})

const configs = computed(() => view.value?.configs ?? [])
const canExport = computed(() => state.value === 'ready' && Boolean(view.value))
watch(view, (value) => {
  imagePreview.value = null
  emit('viewChange', value)
}, { immediate: true })
const activatedAtText = computed(() => {
  const value = view.value?.activatedAt
  if (!value) return '—'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '—'
  return date.toLocaleString('zh-CN', { hour12: false })
})

/** 范围变化(含清空)都开启新批次;未选齐范围不发请求。 */
watch([selectedEnv, selectedPipeline, selectedScopeId], ([environmentKey, pipelineKey, scopeId]) => {
  void load(environmentKey, pipelineKey, scopeId)
})

const handleEnvChange = (value: string) => {
  selectedEnv.value = value
}

const handlePipelineChange = (value: string) => {
  selectedPipeline.value = value
}

const handleRefresh = () => {
  if (hasScope.value) {
    void load(selectedEnv.value, selectedPipeline.value, selectedScopeId.value)
  }
}

const handleExport = async () => {
  if (!canExport.value) return
  await exportShown()
  if (!exportError.value) {
    toast.success(`已导出版本 ${view.value?.version ?? ''} 的静态包`)
  } else {
    toast.error(exportError.value)
  }
}

/** 复制本次快照的字符串原文;不做 JSON.parse/stringify,避免大整数失真。 */
const handleCopy = async (content: string, id: string) => {
  try { await navigator.clipboard.writeText(content) }
  catch { toast.error('复制失败，请选择配置原文后手动复制'); return }
  copiedId.value = id
  toast.success('已复制到剪贴板')
  setTimeout(() => {
    copiedId.value = null
  }, 2000)
}

const previewImage = (url: string, name: string) => {
  imagePreview.value = { url, name }
}

onMounted(async () => {
  await environmentStore.fetchOverview()
  initialize()
})

onUnmounted(() => {
  reset()
})
</script>

<template>
  <Card class="runtime-panel" :data-state="state">
    <CardHeader class="runtime-header">
      <div class="flex flex-wrap items-center justify-between gap-2">
        <div>
          <CardTitle class="runtime-title text-lg">当前生效配置</CardTitle>
        </div>
          <RouterLink v-if="view" class="spectrum-runtime-release-link" :to="{ path: '/releases', query: { env: view.environmentKey, pipeline: view.pipelineKey } }">
            <PackageOpen class="w-4 h-4 mr-1" />
            前往版本发布
          </RouterLink>
      </div>
    </CardHeader>
    <CardContent class="runtime-content space-y-4">
      <div class="runtime-toolbar flex flex-wrap items-end gap-4">
        <div class="runtime-scope-field">
          <label class="text-sm font-medium text-foreground">环境</label>
          <Select
            :model-value="selectedEnv"
            @update:model-value="handleEnvChange"
            :disabled="environmentStore.loading"
          >
            <SelectTrigger class="w-48" aria-label="环境">
              <SelectValue :placeholder="environmentStore.loading ? '加载中...' : '选择环境'" />
            </SelectTrigger>
            <SelectContent>
              <SelectItem v-for="env in environments" :key="env.key" :value="env.key">
                {{ env.name }}
              </SelectItem>
            </SelectContent>
          </Select>
        </div>

        <div class="runtime-scope-field">
          <label class="text-sm font-medium text-foreground">渠道</label>
          <Select
            :model-value="selectedPipeline"
            @update:model-value="handlePipelineChange"
            :disabled="!selectedEnv"
          >
            <SelectTrigger class="w-48" aria-label="渠道">
              <SelectValue placeholder="选择渠道" />
            </SelectTrigger>
            <SelectContent>
              <SelectItem v-for="pipeline in pipelines" :key="pipeline.key" :value="pipeline.key">
                {{ pipeline.name }}
              </SelectItem>
            </SelectContent>
          </Select>
        </div>

        <div class="runtime-toolbar-actions">
          <Button variant="outline" @click="handleRefresh" :disabled="!hasScope || state === 'loading'">
            <RefreshCw :class="['w-4 h-4 mr-2', { 'animate-spin': state === 'loading' }]" />
            刷新
          </Button>

          <Button variant="outline" @click="handleExport" :disabled="!canExport || exporting">
            <Loader2 v-if="exporting" class="w-4 h-4 mr-2 animate-spin" />
            <Download v-else class="w-4 h-4 mr-2" />
            导出此版本静态包<span v-if="view" class="ml-1 font-mono">{{ view.version }}</span>
          </Button>
        </div>
      </div>

      <!-- §2.1/§2.4: version first; only count, scope and real activation time support it. -->
      <Transition name="home-status">
        <div v-if="view" class="runtime-summary rounded-md border bg-muted/40 px-3 py-2 text-sm flex flex-wrap items-center gap-x-4 gap-y-1" role="status">
          <span class="runtime-summary-version flex items-center gap-2">
            <span class="text-muted-foreground">当前生效版本</span>
            <strong class="font-mono">{{ view.version }}</strong>
          </span>
          <span class="text-muted-foreground">配置 {{ configs.length }} 项</span>
          <span class="text-muted-foreground">范围：{{ scopeLabel }}</span>
          <span class="text-muted-foreground">生效时间：<time v-if="view.activatedAt && activatedAtText !== '—'" :datetime="view.activatedAt">{{ activatedAtText }}</time><span v-else>—</span></span>
        </div>
      </Transition>

      <div v-if="exportError" class="rounded-md border border-destructive/30 bg-destructive/5 px-3 py-2 text-sm text-destructive" data-testid="export-error">
        导出失败:{{ exportError }}
      </div>

      <div class="runtime-table-wrap border rounded-lg overflow-hidden" :aria-busy="state === 'loading'">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead class="w-48">名称</TableHead>
              <TableHead class="w-40">别名</TableHead>
              <TableHead class="w-24">类型</TableHead>
              <TableHead>内容</TableHead>
              <TableHead class="w-20">操作</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            <TableRow v-if="state === 'loading'">
              <TableCell colspan="5" class="text-center py-8" data-testid="loading-row">
                <RefreshCw class="w-5 h-5 animate-spin mx-auto text-muted-foreground" />
                <span class="text-sm text-muted-foreground mt-2 block">加载中...</span>
              </TableCell>
            </TableRow>
            <TableRow v-else-if="!hasScope">
              <TableCell colspan="5" class="text-center py-8 text-muted-foreground" data-testid="idle-row">
                请选择环境和渠道查看生效配置
              </TableCell>
            </TableRow>
            <TableRow v-else-if="state === 'not-active'">
              <TableCell colspan="5" class="text-center py-8 text-muted-foreground" data-testid="not-active-row">
                <AlertTriangle class="w-5 h-5 mx-auto mb-2 text-amber-500" />
                {{ errorMessage }}
                <div class="mt-2">
                  <RouterLink
                    :to="{ path: '/releases', query: { env: selectedEnv, pipeline: selectedPipeline } }"
                    class="text-primary hover:underline"
                  >
                    前往版本发布,创建版本并切换生效
                  </RouterLink>
                </div>
              </TableCell>
            </TableRow>
            <TableRow v-else-if="state === 'incomplete' || state === 'error'">
              <TableCell colspan="5" class="text-center py-8 text-muted-foreground" data-testid="error-row">
                <AlertTriangle class="w-5 h-5 mx-auto mb-2 text-destructive" />
                {{ errorMessage }}
                <div class="mt-2">
                  <Button variant="outline" size="sm" @click="handleRefresh">重试</Button>
                </div>
              </TableCell>
            </TableRow>
            <TableRow v-else-if="configs.length === 0">
              <TableCell colspan="5" class="text-center py-8 text-muted-foreground" data-testid="empty-version-row">
                本版本没有配置(版本 {{ view?.version }},仍可导出空版本静态包)
              </TableCell>
            </TableRow>
            <TableRow v-else v-for="config in configs" :key="config.resource_key" class="runtime-config-row">
              <TableCell class="font-mono text-sm">{{ config.name || config.alias }}</TableCell>
              <TableCell class="text-muted-foreground">{{ config.alias }}</TableCell>
              <TableCell>
                <Badge
                  variant="secondary"
                  class="runtime-type border"
                >
                  {{ CONFIG_TYPE_META[config.type as ConfigType]?.label || config.type }}
                </Badge>
              </TableCell>
              <TableCell class="runtime-raw-content font-mono text-sm max-w-xs">
                <ConfigContentPreview :config="config" @image-preview="previewImage" />
              </TableCell>
              <TableCell>
                <Button variant="ghost" size="icon" :aria-label="`复制 ${config.alias} 原文`" @click="handleCopy(config.content, config.resource_key)">
                  <Check v-if="copiedId === config.resource_key" class="w-4 h-4 text-green-600" />
                  <Copy v-else class="w-4 h-4" />
                </Button>
              </TableCell>
            </TableRow>
          </TableBody>
        </Table>
      </div>

    </CardContent>
  </Card>
  <SpectrumImagePreview :url="imagePreview?.url ?? null" :name="imagePreview?.name ?? ''" @close="imagePreview = null" />
</template>
