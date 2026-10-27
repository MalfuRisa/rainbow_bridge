<script setup lang="ts">
import { ref, computed, watch, onMounted, onUnmounted } from 'vue'
import type { ImportSummary } from '@/lib/api/types'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { Checkbox } from '@/components/ui/checkbox'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogFooter } from '@/components/ui/dialog'
import { Upload, Loader2, Check, AlertCircle, X, FileArchive, FileWarning, Lock } from 'lucide-vue-next'
import ImportPreviewTree from './ImportPreviewTree.vue'
import { transferApi, type ExportSelection, type ImportPreviewData } from '@/lib/api/transfer'
import { useFreezeStore } from '@/stores/freeze'
import { isFreezeBlockedError, freezeErrorMessage } from '@/lib/api/error'
import { useEnvironmentStore } from '@/stores/environment'
import { useScopeSelection } from '@/composables/useScopeSelection'
import EnvironmentChannelSelector from '@/components/resources/EnvironmentChannelSelector.vue'
import { captureScope, type ScopeTarget } from '@/lib/scope'
import type { Environment } from '@/lib/types'
import { configApi } from '@/lib/api/config'

const emit = defineEmits<{
  (e: 'load-tree'): void
}>()

const freezeStore = useFreezeStore()
const environments = useEnvironmentStore()
const importMode = ref<'original' | 'target'>('original')
const { envPipelineModel, selectedScopeId, selectedEnvKey, selectedPipelineKey, initialize } = useScopeSelection(() => environments.environments)
onMounted(initialize)
const targetBindings = ref<Environment[]>([])
const fixedTarget = ref<ScopeTarget | null>(null)
let parseBatch = 0
onUnmounted(() => { parseBatch++ })

const importFile = ref<File | null>(null)
const importPreview = ref<ImportPreviewData | null>(null)
const importSelections = ref<ExportSelection[]>([])
const isParsing = ref(false)
const isImporting = ref(false)
const importSuccess = ref(false)
const importResult = ref<ImportSummary | null>(null)
const resourceMessage = computed(() => {
  const result = importResult.value?.resource_import
  if (!result) return '配置导入完成，资源状态未提供'
  if (result.status === 'partial') return '配置已提交，部分资源未恢复'
  if (result.status === 'complete') return '导入完成，资源恢复完整'
  return '配置导入完成（无资源恢复任务）'
})
const importError = ref<string | null>(null)
const importErrorIsFreeze = ref(false)
const overwriteConflicts = ref(false)
const showConfirmDialog = ref(false)

const fileInputRef = ref<HTMLInputElement | null>(null)
const dragging = ref(false)

// 预览后加载所有目标范围的冻结状态(仅展示;服务端仍会在提交时复查)
watch(importPreview, async (preview) => {
  if (!preview) return
  const scopes: { environmentKey: string; pipelineKey: string }[] = []
  for (const env of preview.environments) {
    for (const pipeline of env.pipelines) {
      scopes.push({ environmentKey: env.environment_key, pipelineKey: pipeline.pipeline_key })
    }
  }
  await freezeStore.loadMany(scopes)
})

const lockedTargets = computed(() => {
  if (importMode.value === 'target') return freezeStore.isLocked(selectedEnvKey.value, selectedPipelineKey.value) ? [{ label: `${selectedEnvKey.value}/${selectedPipelineKey.value}`, reason: '' }] : []
  if (!importPreview.value) return [] as { label: string; reason: string }[]
  const targets: { label: string; reason: string }[] = []
  for (const env of importPreview.value.environments) {
    for (const pipeline of env.pipelines) {
      if (freezeStore.isLocked(env.environment_key, pipeline.pipeline_key)) {
        targets.push({
          label: `${env.environment_key}/${pipeline.pipeline_key}`,
          reason: freezeStore.statusFor(env.environment_key, pipeline.pipeline_key)?.last_reason || '',
        })
      }
    }
  }
  return targets
})

const validateFile = (file: File): string | null => {
  const fileName = file.name.toLowerCase()
  if (importMode.value === 'target' && !selectedScopeId.value) return '请先选择目标环境和渠道。'
  if (importMode.value === 'target' && !fileName.endsWith('.zip')) return '指定目标渠道请使用 ZIP 迁移包。TAR.GZ 可按包内范围导入。'
  if (!fileName.endsWith('.zip') && !fileName.endsWith('.tar.gz') && !fileName.endsWith('.tgz')) {
    return '不支持的文件格式，请上传 .zip 或 .tar.gz 文件'
  }
  return null
}

const parseFile = async (file: File): Promise<ImportPreviewData | null> => {
  try {
    return await transferApi.importPreview(file)
  } catch (err) {
    throw new Error(err instanceof Error ? err.message : '文件解析失败')
  }
}

const processPreviewData = (preview: ImportPreviewData): ExportSelection[] => {
  const allSelections: ExportSelection[] = []
  preview.environments.forEach((env) => {
    allSelections.push({
      environment_key: env.environment_key,
      pipeline_key: '',
      resource_keys: [],
    })
  })
  return allSelections
}

const handleFileSelect = async (e: Event) => {
  const file = (e.target as HTMLInputElement).files?.[0]
  if (!file) return
  await processFile(file)
}

const handleDrop = async (event: DragEvent) => {
  dragging.value = false
  const file = event.dataTransfer?.files[0]
  if (file) await processFile(file)
}

const processFile = async (file: File) => {
  if (isParsing.value || isImporting.value) return

  const validationError = validateFile(file)
  if (validationError) {
    importError.value = validationError
    return
  }

  importFile.value = file
  importSuccess.value = false
  importResult.value = null
  importError.value = null
  importPreview.value = null
  targetBindings.value = []
  isParsing.value = true
  const batch = ++parseBatch
  try {
    await environments.fetchOverview()
    if (environments.error) throw new Error(environments.error)
    targetBindings.value = JSON.parse(JSON.stringify(environments.environments))
    fixedTarget.value = importMode.value === 'target' ? captureScope(selectedEnvKey.value, selectedPipelineKey.value) : null
    const preview = await parseFile(file)
    if (preview && fixedTarget.value) {
      const target = fixedTarget.value
      const existing = await configApi.list({ environmentKey: target.environmentKey, pipelineKey: target.pipelineKey, scopeId: target.scopeId })
      const aliases = new Set(existing.list.map(item => item.alias))
      let conflicts = 0
      for (const env of preview.environments) for (const pipeline of env.pipelines) for (const config of pipeline.configs) {
        config.status = aliases.has(config.alias) ? 'conflict' : 'new'
        if (config.status === 'conflict') conflicts++
      }
      preview.summary.conflict_count = conflicts
    }
    if (batch !== parseBatch) return
    if (preview) {
      importPreview.value = preview
      importSelections.value = processPreviewData(preview)
    }
  } catch (err) {
    if (batch === parseBatch) importError.value = err instanceof Error ? err.message : '文件解析失败'
  } finally {
    if (batch === parseBatch) isParsing.value = false
  }
}

const checkImportConflicts = (): boolean => {
  if (!importPreview.value) return false
  
  if (importPreview.value.summary.conflict_count > 0 && !overwriteConflicts.value && !showConfirmDialog.value) {
    showConfirmDialog.value = true
    return true
  }
  return false
}

const executeImport = async (): Promise<void> => {
  if (!importFile.value || !importPreview.value) return
  if (importMode.value === 'target') {
    if (!fixedTarget.value) throw new Error('请先选择目标渠道并重新解析文件。')
    if (!importFile.value.name.toLowerCase().endsWith('.zip')) throw new Error('指定目标渠道当前仅支持 ZIP，请使用 ZIP 迁移包。')
    importResult.value = await transferApi.importFile(importFile.value, fixedTarget.value, overwriteConflicts.value)
    return
  }
  const selections = importSelections.value.flatMap(selection => {
    const env = targetBindings.value.find(item => item.key === selection.environment_key)
    const previewEnv = importPreview.value!.environments.find(item => item.environment_key === selection.environment_key)
    const pipelines = selection.pipeline_key ? [selection.pipeline_key] : (previewEnv?.pipelines || []).map(item => item.pipeline_key)
    return pipelines.length ? pipelines.map(key => ({ ...selection, pipeline_key: key, expected_environment_id: env?.environmentId,
      expected_scope_id: env?.pipelines.find(item => item.key === key)?.scopeId })) : [{ ...selection, expected_environment_id: env?.environmentId }]
  })
  importResult.value = await transferApi.importSelective(importFile.value, selections, overwriteConflicts.value)
}

const handleImportSuccess = () => {
  importSuccess.value = true
  emit('load-tree')
}

const handleImport = async () => {
  if (!importFile.value || !importPreview.value || isImporting.value) return

  if (checkImportConflicts()) {
    return
  }

  isImporting.value = true
  const batch = parseBatch
  importSuccess.value = false
  importError.value = null
  importErrorIsFreeze.value = false

  try {
    await executeImport()
    if (batch !== parseBatch) return
    handleImportSuccess()
  } catch (err) {
    if (batch !== parseBatch) return
    if (isFreezeBlockedError(err)) {
      // 后端 409 展示实际受阻目标与原因,并刷新冻结状态;选择保持不丢
      importErrorIsFreeze.value = true
      importError.value = freezeErrorMessage(err)
      if (importPreview.value) {
        const scopes: { environmentKey: string; pipelineKey: string }[] = []
        for (const env of importPreview.value.environments) {
          for (const pipeline of env.pipelines) {
            scopes.push({ environmentKey: env.environment_key, pipelineKey: pipeline.pipeline_key })
          }
        }
        await freezeStore.loadMany(scopes)
      }
    } else {
      importError.value = err instanceof Error ? err.message : '导入失败'
    }
  } finally {
    isImporting.value = false
    showConfirmDialog.value = false
  }
}

const clearImport = () => {
  parseBatch++
  showConfirmDialog.value = false
  fixedTarget.value = null
  importSuccess.value = false
  importResult.value = null
  importFile.value = null
  importPreview.value = null
  importSelections.value = []
  importError.value = null
  importErrorIsFreeze.value = false
  overwriteConflicts.value = false
  if (fileInputRef.value) {
    fileInputRef.value.value = ''
  }
}

const handleImportSelectionsChange = (selections: ExportSelection[]) => {
  importSelections.value = selections
}
watch([importMode, () => importMode.value === 'target' ? selectedScopeId.value : ''], () => {
  parseBatch++
  if (!importFile.value) return
  isParsing.value = false
  importPreview.value = null; importSelections.value = []; fixedTarget.value = null; showConfirmDialog.value = false
  importError.value = '导入目标已变化，请重新解析文件并核对。'
}, { flush: 'sync' })
watch(() => JSON.stringify(environments.environments.map(env => [env.environmentId, env.pipelines.map(pipe => pipe.scopeId)])), () => {
  if ((!importPreview.value && !isParsing.value) || !targetBindings.value.length) return
  const changed = targetBindings.value.some(old => {
    const current = environments.environments.find(env => env.key === old.key)
    return current?.environmentId !== old.environmentId || old.pipelines.some(pipe => current?.pipelines.find(item => item.key === pipe.key)?.scopeId !== pipe.scopeId)
  })
  if (changed) { parseBatch++; isParsing.value = false; importPreview.value = null; fixedTarget.value = null; showConfirmDialog.value = false; importError.value = '范围已删除或重新创建，请重新解析文件后核对。' }
})
</script>

<template>
  <div class="spectrum-transfer-layout">
    <div class="spectrum-transfer-content space-y-6">
      <Button v-if="importFile && !importPreview && !isParsing" variant="outline" @click="processFile(importFile)">重新解析文件</Button>
      <Card class="spectrum-panel"><CardHeader class="spectrum-panel-header"><CardTitle class="text-lg">导入到编辑区</CardTitle></CardHeader><CardContent class="spectrum-panel-content space-y-4">
        <div class="flex gap-3"><Button :variant="importMode === 'original' ? 'default' : 'outline'" :disabled="isImporting" @click="importMode = 'original'">按包内范围</Button><Button :variant="importMode === 'target' ? 'default' : 'outline'" :disabled="isImporting" @click="importMode = 'target'">指定目标渠道</Button></div>
        <p class="text-sm text-muted-foreground">{{ importMode === 'original' ? '按包内环境和渠道导入，可选择配置；不存在的范围将新建。' : '将 ZIP 迁移包的全部配置导入一个渠道，可选择是否覆盖冲突项。' }}</p>
        <EnvironmentChannelSelector v-if="importMode === 'target'" v-model="envPipelineModel" :disabled="isImporting" />
        <p class="text-xs text-muted-foreground">迁移包仅更新编辑区，不自动锁版、发布或切换生效。业务静态包和整实例备份不可在此导入。</p>
      </CardContent></Card>
      <Card class="spectrum-panel">
        <CardHeader class="spectrum-panel-header">
          <CardTitle class="text-lg">上传文件</CardTitle>
        </CardHeader>
        <CardContent class="spectrum-panel-content">
          <Alert v-if="importError && !importFile" variant="destructive" class="mb-4"><AlertCircle :size="16" /><AlertTitle>无法选择文件</AlertTitle><AlertDescription>{{ importError }}</AlertDescription></Alert>
          <template v-if="!importFile">
            <label
              for="import-file"
              class="spectrum-import-dropzone"
              :class="{ 'is-dragging': dragging }"
              role="button"
              tabindex="0"
              aria-label="选择或拖入迁移包"
              @keydown.enter="fileInputRef?.click()"
              @keydown.space.prevent="fileInputRef?.click()"
              @dragover.prevent="dragging = true"
              @dragleave.prevent="dragging = false"
              @drop.prevent="handleDrop"
            >
              <Upload class="w-12 h-12 text-muted-foreground mb-4" />
              <span class="text-sm font-medium">
                点击或拖拽文件到此处
              </span>
              <span class="text-xs text-muted-foreground mt-1">
                支持 .zip 和 .tar.gz 文件
              </span>
              <input
                id="import-file"
                type="file"
                accept=".zip,.tar.gz,.tgz"
                class="hidden"
                ref="fileInputRef"
                @change="handleFileSelect"
              />
            </label>
          </template>
          <template v-else>
            <div class="space-y-4">
              <div class="flex items-center gap-4 p-4 bg-muted rounded-lg">
                <FileArchive class="w-10 h-10 text-muted-foreground" />
                <div class="flex-1 min-w-0">
                  <div class="font-medium truncate">{{ importFile.name }}</div>
                  <div class="text-sm text-muted-foreground">
                    {{ (importFile.size / 1024).toFixed(2) }} KB
                  </div>
                </div>
                <Button variant="ghost" size="icon" :disabled="isParsing || isImporting" aria-label="移除导入文件" @click="clearImport">
                  <X class="w-4 h-4" />
                </Button>
              </div>

              <div v-if="isParsing" class="flex items-center justify-center py-8">
                <Loader2 class="w-6 h-6 animate-spin text-muted-foreground" />
                <span class="ml-2 text-muted-foreground">解析文件中...</span>
              </div>

              <Alert v-if="importError && !importPreview" variant="destructive">
                <AlertCircle class="h-4 w-4" />
                <AlertTitle>无法继续导入</AlertTitle>
                <AlertDescription>{{ importError }}</AlertDescription>
              </Alert>
            </div>
          </template>
        </CardContent>
      </Card>

      <Card v-if="importPreview" class="spectrum-panel spectrum-section">
        <CardHeader class="spectrum-panel-header">
          <CardTitle class="text-lg">{{ importMode === 'target' ? '包内内容概览' : '选择导入内容' }}</CardTitle>
        </CardHeader>
        <CardContent class="spectrum-panel-content space-y-4">
          <Alert v-if="lockedTargets.length" variant="destructive">
            <Lock class="h-4 w-4" />
            <AlertTitle>部分目标已冻结</AlertTitle>
            <AlertDescription>
              <ul class="mt-1 space-y-0.5">
                <li v-for="target in lockedTargets" :key="target.label">
                  <span class="font-mono">{{ target.label }}</span>
                  <span v-if="target.reason">(原因:{{ target.reason }})</span>
                </li>
              </ul>
              导入这些目标会取消整次导入。请先解除冻结，或取消选择这些目标。
            </AlertDescription>
          </Alert>

          <div class="spectrum-import-stats">
            <div class="p-4 bg-muted rounded-lg text-center">
              <div class="spectrum-stat-value">
                {{ importPreview.summary.total_environments }}
              </div>
              <div class="text-sm text-muted-foreground">环境</div>
            </div>
            <div class="p-4 bg-muted rounded-lg text-center">
              <div class="spectrum-stat-value">
                {{ importPreview.summary.total_pipelines }}
              </div>
              <div class="text-sm text-muted-foreground">渠道</div>
            </div>
            <div class="p-4 bg-muted rounded-lg text-center">
              <div class="spectrum-stat-value">
                {{ importPreview.summary.total_configs }}
              </div>
              <div class="text-sm text-muted-foreground">配置</div>
            </div>
            <div class="p-4 bg-muted rounded-lg text-center">
              <div class="spectrum-stat-value">
                {{ importPreview.summary.total_assets || 0 }}
              </div>
              <div class="text-sm text-muted-foreground">资源</div>
            </div>
            <div class="p-4 bg-muted rounded-lg text-center">
              <div class="spectrum-stat-value">
                {{ importPreview.summary.conflict_count }}
              </div>
              <div class="text-sm text-muted-foreground">冲突</div>
            </div>
          </div>

          <div v-if="importMode === 'original'" :inert="isImporting"><ImportPreviewTree
            :data="importPreview.environments"
            @change="handleImportSelectionsChange"
          /></div>

          <div v-if="importPreview.summary.conflict_count > 0" class="flex items-center gap-2 p-3 bg-muted rounded-lg mt-4">
            <Checkbox
              id="overwrite"
              :disabled="isImporting"
              :model-value="overwriteConflicts"
              @update:model-value="(v: boolean) => overwriteConflicts = v"
            />
            <label for="overwrite" class="text-sm cursor-pointer">
              覆盖现有冲突数据
            </label>
          </div>
        </CardContent>
      </Card>
    </div>

    <div class="spectrum-transfer-aside space-y-6">
      <Card class="spectrum-panel spectrum-transfer-summary">
        <CardHeader class="spectrum-panel-header">
          <p class="spectrum-eyebrow">IMPORT / 导入清单</p>
          <CardTitle class="text-lg">导入操作</CardTitle>
        </CardHeader>
        <CardContent class="spectrum-panel-content space-y-4">
          <template v-if="importPreview">
            <div class="space-y-3">
              <div class="flex items-center justify-between text-sm">
                <span class="text-muted-foreground">文件格式</span>
                <Badge variant="outline">{{ importPreview.format }}</Badge>
              </div>
              <div class="flex items-center justify-between text-sm">
                <span class="text-muted-foreground">文件内配置</span>
                <span class="font-medium">
                  {{ importPreview.summary.total_configs }} 项
                </span>
              </div>
              <div class="flex items-center justify-between text-sm">
                <span class="text-muted-foreground">冲突项</span>
                <span
                  :class="importPreview.summary.conflict_count > 0 ? 'spectrum-state-pill is-frozen' : ''"
                >
                  {{ importPreview.summary.conflict_count }} 项
                </span>
              </div>
              <div class="flex items-center justify-between text-sm">
                <span class="text-muted-foreground">处理方式</span>
                <span class="font-medium">
                  {{ overwriteConflicts ? '覆盖' : '跳过' }}冲突
                </span>
              </div>
            </div>

            <div class="pt-4 border-t">
              <Button
                class="w-full"
                @click="handleImport"
                :disabled="isImporting || !importPreview || (importMode === 'original' ? importSelections.length === 0 : !fixedTarget) || lockedTargets.length > 0"
              >
                <Loader2 v-if="isImporting" class="w-4 h-4 mr-2 animate-spin" />
                <Upload v-else class="w-4 h-4 mr-2" />
                {{ isImporting ? '导入中...' : '开始导入' }}
              </Button>
            </div>

            <Alert v-if="importError && importPreview" variant="destructive">
              <Lock v-if="importErrorIsFreeze" class="h-4 w-4" />
              <AlertCircle v-else class="h-4 w-4" />
              <AlertTitle>{{ importErrorIsFreeze ? '导入被拒绝(目标已冻结)' : '导入失败' }}</AlertTitle>
              <AlertDescription>{{ importError }}</AlertDescription>
            </Alert>

            <Alert v-if="importSuccess" :class="importResult?.resource_import?.status === 'partial' ? 'spectrum-result-warning' : 'spectrum-result-success'">
              <AlertCircle v-if="importResult?.resource_import?.status === 'partial'" class="h-4 w-4" /><Check v-else class="h-4 w-4" />
              <AlertTitle>{{ resourceMessage }}</AlertTitle>
              <AlertDescription>
                本次写入 {{ importResult?.total ?? 0 }} 项配置。
                <template v-if="importResult?.resource_import">
                  资源：预期 {{ importResult.resource_import.expected }}，恢复 {{ importResult.resource_import.restored }}，
                  复用 {{ importResult.resource_import.reused }}，失败 {{ importResult.resource_import.failed }}。
                  <ul v-if="importResult.resource_import.issues?.length" class="mt-2 space-y-1">
                    <li v-for="issue in importResult.resource_import.issues" :key="issue.file_id + issue.path">
                      {{ issue.path || issue.file_id }}：{{ issue.reason }}
                    </li>
                  </ul>
                </template>
              </AlertDescription>
            </Alert>
          </template>
          <template v-else>
            <div class="text-center py-8 text-muted-foreground">
              <Upload class="w-12 h-12 mx-auto mb-4 opacity-50" />
              <p>请先上传配置文件</p>
            </div>
          </template>
        </CardContent>
      </Card>

      <Alert>
        <AlertCircle class="h-4 w-4" />
        <AlertTitle>注意事项</AlertTitle>
        <AlertDescription class="space-y-1">
          <p>导入修改编辑区，建议先保存所需内容。</p>
          <p>冲突项默认跳过；勾选覆盖后替换冲突项，其他配置保留。</p>
        </AlertDescription>
      </Alert>
    </div>
  </div>

  <Dialog :open="showConfirmDialog" @update:open="(v: boolean) => showConfirmDialog = v">
    <DialogContent>
      <DialogHeader>
        <DialogTitle class="flex items-center gap-2">
          <FileWarning class="w-5 h-5" />
          确认导入
        </DialogTitle>
        <DialogDescription>
          文件中有 {{ importPreview?.summary.conflict_count }} 项冲突。本次将{{ overwriteConflicts ? '覆盖' : '跳过' }}所选冲突项，是否继续？
        </DialogDescription>
      </DialogHeader>
      <DialogFooter>
        <Button variant="outline" @click="showConfirmDialog = false">
          取消
        </Button>
        <Button @click="handleImport" :disabled="isImporting">
          <Loader2 v-if="isImporting" class="w-4 h-4 mr-2 animate-spin" />
          {{ isImporting ? '导入中...' : '确认导入' }}
        </Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>
</template>

