<script setup lang="ts">
import { ref } from 'vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { Checkbox } from '@/components/ui/checkbox'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Upload, Loader2, Check, AlertCircle, X, FileArchive, FileWarning } from 'lucide-vue-next'
import ImportPreviewTree from './ImportPreviewTree.vue'
import { transferApi, type ExportSelection, type ImportPreviewData } from '@/lib/api/transfer'

const emit = defineEmits<{
  (e: 'load-tree'): void
}>()

const importFile = ref<File | null>(null)
const importPreview = ref<ImportPreviewData | null>(null)
const importSelections = ref<ExportSelection[]>([])
const isParsing = ref(false)
const isImporting = ref(false)
const importSuccess = ref(false)
const importError = ref<string | null>(null)
const overwriteConflicts = ref(false)
const showConfirmDialog = ref(false)

const fileInputRef = ref<HTMLInputElement | null>(null)

const validateFile = (file: File): string | null => {
  const fileName = file.name.toLowerCase()
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

  const validationError = validateFile(file)
  if (validationError) {
    importError.value = validationError
    return
  }

  importFile.value = file
  importError.value = null
  importPreview.value = null
  isParsing.value = true

  try {
    const preview = await parseFile(file)
    if (preview) {
      importPreview.value = preview
      importSelections.value = processPreviewData(preview)
    }
  } catch (err) {
    importError.value = err instanceof Error ? err.message : '文件解析失败'
  } finally {
    isParsing.value = false
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
  
  await transferApi.importSelective(importFile.value, importSelections.value, overwriteConflicts.value)
}

const handleImportSuccess = () => {
  importSuccess.value = true
  setTimeout(() => {
    importSuccess.value = false
    clearImport()
    emit('load-tree')
  }, 3000)
}

const handleImport = async () => {
  if (!importFile.value || !importPreview.value) return

  if (checkImportConflicts()) {
    return
  }

  isImporting.value = true
  importSuccess.value = false
  importError.value = null

  try {
    await executeImport()
    handleImportSuccess()
  } catch (err) {
    importError.value = err instanceof Error ? err.message : '导入失败'
  } finally {
    isImporting.value = false
    showConfirmDialog.value = false
  }
}

const clearImport = () => {
  importFile.value = null
  importPreview.value = null
  importSelections.value = []
  importError.value = null
  overwriteConflicts.value = false
  if (fileInputRef.value) {
    fileInputRef.value.value = ''
  }
}

const handleImportSelectionsChange = (selections: ExportSelection[]) => {
  importSelections.value = selections
}
</script>

<template>
  <div class="grid grid-cols-1 lg:grid-cols-3 gap-6">
    <div class="lg:col-span-2 space-y-6">
      <Card>
        <CardHeader>
          <CardTitle class="text-lg">上传文件</CardTitle>
          <CardDescription>
            支持 ZIP 和 TAR.GZ 格式的配置备份文件
          </CardDescription>
        </CardHeader>
        <CardContent>
          <template v-if="!importFile">
            <label
              for="import-file"
              class="flex flex-col items-center justify-center w-full h-48 border-2 border-dashed rounded-lg cursor-pointer hover:border-primary hover:bg-muted/50 transition-colors"
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
                <FileArchive class="w-10 h-10 text-amber-500" />
                <div class="flex-1 min-w-0">
                  <div class="font-medium truncate">{{ importFile.name }}</div>
                  <div class="text-sm text-muted-foreground">
                    {{ (importFile.size / 1024).toFixed(2) }} KB
                  </div>
                </div>
                <Button variant="ghost" size="icon" @click="clearImport">
                  <X class="w-4 h-4" />
                </Button>
              </div>

              <div v-if="isParsing" class="flex items-center justify-center py-8">
                <Loader2 class="w-6 h-6 animate-spin text-muted-foreground" />
                <span class="ml-2 text-muted-foreground">解析文件中...</span>
              </div>

              <Alert v-if="importError" variant="destructive">
                <AlertCircle class="h-4 w-4" />
                <AlertTitle>解析失败</AlertTitle>
                <AlertDescription>{{ importError }}</AlertDescription>
              </Alert>
            </div>
          </template>
        </CardContent>
      </Card>

      <Card v-if="importPreview">
        <CardHeader>
          <CardTitle class="text-lg">选择导入内容</CardTitle>
          <CardDescription>
            选择要导入的环境、渠道或配置项
          </CardDescription>
        </CardHeader>
        <CardContent class="space-y-4">
          <div class="grid grid-cols-2 md:grid-cols-5 gap-4 mb-4">
            <div class="p-4 bg-muted rounded-lg text-center">
              <div class="text-2xl font-bold text-blue-600">
                {{ importPreview.summary.total_environments }}
              </div>
              <div class="text-sm text-muted-foreground">环境</div>
            </div>
            <div class="p-4 bg-muted rounded-lg text-center">
              <div class="text-2xl font-bold text-indigo-600">
                {{ importPreview.summary.total_pipelines }}
              </div>
              <div class="text-sm text-muted-foreground">渠道</div>
            </div>
            <div class="p-4 bg-muted rounded-lg text-center">
              <div class="text-2xl font-bold text-purple-600">
                {{ importPreview.summary.total_configs }}
              </div>
              <div class="text-sm text-muted-foreground">配置</div>
            </div>
            <div class="p-4 bg-muted rounded-lg text-center">
              <div class="text-2xl font-bold text-cyan-600">
                {{ importPreview.summary.total_assets || 0 }}
              </div>
              <div class="text-sm text-muted-foreground">资源</div>
            </div>
            <div class="p-4 bg-muted rounded-lg text-center">
              <div class="text-2xl font-bold text-amber-600">
                {{ importPreview.summary.conflict_count }}
              </div>
              <div class="text-sm text-muted-foreground">冲突</div>
            </div>
          </div>

          <ImportPreviewTree
            :data="importPreview.environments"
            @change="handleImportSelectionsChange"
          />

          <div v-if="importPreview.summary.conflict_count > 0" class="flex items-center gap-2 p-3 bg-muted rounded-lg mt-4">
            <Checkbox
              id="overwrite"
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

    <div class="space-y-6">
      <Card>
        <CardHeader>
          <CardTitle class="text-lg">导入操作</CardTitle>
        </CardHeader>
        <CardContent class="space-y-4">
          <template v-if="importPreview">
            <div class="space-y-3">
              <div class="flex items-center justify-between text-sm">
                <span class="text-muted-foreground">文件格式</span>
                <Badge variant="outline">{{ importPreview.format }}</Badge>
              </div>
              <div class="flex items-center justify-between text-sm">
                <span class="text-muted-foreground">待导入总数</span>
                <span class="font-medium">
                  {{ importPreview.summary.total_configs }} 项
                </span>
              </div>
              <div class="flex items-center justify-between text-sm">
                <span class="text-muted-foreground">冲突项</span>
                <span
                  :class="importPreview.summary.conflict_count > 0 ? 'text-amber-600 font-medium' : ''"
                >
                  {{ importPreview.summary.conflict_count }} 项
                </span>
              </div>
              <div class="flex items-center justify-between text-sm">
                <span class="text-muted-foreground">处理方式</span>
                <Badge :variant="overwriteConflicts ? 'destructive' : 'secondary'">
                  {{ overwriteConflicts ? '覆盖' : '跳过' }}冲突
                </Badge>
              </div>
            </div>

            <div class="pt-4 border-t">
              <Button
                class="w-full"
                @click="handleImport"
                :disabled="isImporting || importSelections.length === 0"
              >
                <Loader2 v-if="isImporting" class="w-4 h-4 mr-2 animate-spin" />
                <Upload v-else class="w-4 h-4 mr-2" />
                {{ isImporting ? '导入中...' : '开始导入' }}
              </Button>
            </div>

            <Alert v-if="importSuccess" class="border-emerald-500 bg-emerald-50 dark:bg-emerald-950">
              <Check class="h-4 w-4 text-emerald-600" />
              <AlertTitle class="text-emerald-600">导入成功</AlertTitle>
              <AlertDescription class="text-emerald-600">
                数据已成功导入系统
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
          <p>1. 导入前请确保已备份现有数据</p>
          <p>2. 冲突数据默认跳过；选择覆盖仅更新输入中的冲突项，其他配置保留</p>
          <p>3. 导入操作不可撤销</p>
        </AlertDescription>
      </Alert>
    </div>
  </div>

  <Dialog :open="showConfirmDialog" @update:open="(v: boolean) => showConfirmDialog = v">
    <DialogContent>
      <DialogHeader>
        <DialogTitle class="flex items-center gap-2">
          <FileWarning class="w-5 h-5 text-amber-500" />
          确认导入
        </DialogTitle>
        <DialogDescription>
          检测到 {{ importPreview?.summary.conflict_count }} 个冲突项，是否继续导入？
          未选择"覆盖冲突数据"时，冲突项将被跳过。
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

