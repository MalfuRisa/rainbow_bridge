<script setup lang="ts">
import { ref, computed } from 'vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from '@/components/ui/card'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Download, Loader2, Check, Info, AlertCircle } from 'lucide-vue-next'
import ExportTreeSelect from './ExportTreeSelect.vue'
import { transferApi, type ExportSelection, type ExportTreeEnvironment } from '@/lib/api/transfer'
import FormatSelector from './FormatSelector.vue'
import StatsDisplay from './StatsDisplay.vue'

type ExportFormat = 'zip' | 'tar.gz'

const props = defineProps<{
  exportTree: ExportTreeEnvironment[]
  isLoadingTree: boolean
}>()

const exportSelections = ref<ExportSelection[]>([])
const exportFormat = ref<ExportFormat>('zip')
const isExporting = ref(false)
const exportSuccess = ref(false)
const exportError = ref<string | null>(null)

const exportStats = computed(() => {
  let envCount = 0
  let pipeCount = 0
  let configCount = 0

  exportSelections.value.forEach((sel) => {
    const env = props.exportTree.find((e) => e.environment_key === sel.environment_key)
    if (!env) return

    if (!sel.pipeline_key) {
      envCount++
      pipeCount += env.pipelines.length
      configCount += env.pipelines.reduce((sum, p) => sum + p.config_count, 0)
    } else if (!sel.resource_keys || sel.resource_keys.length === 0) {
      const pipe = env.pipelines.find((p) => p.pipeline_key === sel.pipeline_key)
      if (pipe) {
        pipeCount++
        configCount += pipe.config_count
      }
    } else {
      configCount += sel.resource_keys.length
    }
  })

  return { envCount, pipeCount, configCount }
})

const validateExportSelections = (): boolean => {
  if (exportSelections.value.length === 0) {
    exportError.value = '请选择要导出的内容'
    return false
  }
  return true
}

const downloadFile = (blob: Blob, format: ExportFormat) => {
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = `rainbow-bridge-export-${Date.now()}.${format === 'tar.gz' ? 'tar.gz' : 'zip'}`
  document.body.appendChild(a)
  a.click()
  document.body.removeChild(a)
  URL.revokeObjectURL(url)
}

const handleExport = async () => {
  if (!validateExportSelections()) return

  isExporting.value = true
  exportSuccess.value = false
  exportError.value = null

  try {
    const blob = await transferApi.exportSelective({
      format: exportFormat.value,
      selections: exportSelections.value,
    })

    downloadFile(blob, exportFormat.value)

    exportSuccess.value = true
    setTimeout(() => (exportSuccess.value = false), 3000)
  } catch (err) {
    exportError.value = err instanceof Error ? err.message : '导出失败'
  } finally {
    isExporting.value = false
  }
}

const handleExportSelectionsChange = (selections: ExportSelection[]) => {
  exportSelections.value = selections
}

const handleFormatChange = (format: ExportFormat) => {
  exportFormat.value = format
}
</script>

<template>
  <div class="spectrum-transfer-layout">
    <div class="spectrum-transfer-content space-y-6">
      <Card class="spectrum-panel">
        <CardHeader class="spectrum-panel-header">
          <CardTitle class="text-lg">选择导出内容</CardTitle>
          <CardDescription>
            选择要导出的环境、渠道或配置项
          </CardDescription>
        </CardHeader>
        <CardContent class="spectrum-panel-content">
          <Alert v-if="exportError" variant="destructive" class="mb-4">
            <AlertCircle class="h-4 w-4" />
            <AlertTitle>错误</AlertTitle>
            <AlertDescription>{{ exportError }}</AlertDescription>
          </Alert>
          <ExportTreeSelect
            :data="exportTree"
            :loading="isLoadingTree"
            @change="handleExportSelectionsChange"
          />
        </CardContent>
      </Card>

      <FormatSelector :format="exportFormat" @change="handleFormatChange" />
    </div>

    <div class="spectrum-transfer-aside space-y-6">
      <Card class="spectrum-panel spectrum-transfer-summary">
        <CardHeader class="spectrum-panel-header">
          <p class="spectrum-eyebrow">EXPORT / 打包清单</p>
          <CardTitle class="text-lg">导出预览</CardTitle>
        </CardHeader>
        <CardContent class="spectrum-panel-content space-y-4">
          <StatsDisplay :format="exportFormat" :stats="exportStats" />

          <div class="pt-4 border-t">
            <Button
              class="w-full"
              @click="handleExport"
              :disabled="isExporting || exportSelections.length === 0"
            >
              <Loader2 v-if="isExporting" class="w-4 h-4 mr-2 animate-spin" />
              <Download v-else class="w-4 h-4 mr-2" />
              {{ isExporting ? '导出中...' : '导出数据' }}
            </Button>
          </div>

          <Alert v-if="exportSuccess" class="spectrum-result-success">
            <Check class="h-4 w-4" />
            <AlertTitle>导出成功</AlertTitle>
            <AlertDescription>
              文件已开始下载
            </AlertDescription>
          </Alert>
        </CardContent>
      </Card>

      <Alert>
        <Info class="h-4 w-4" />
        <AlertTitle>提示</AlertTitle>
        <AlertDescription>
          导出的数据包含完整的配置信息，请妥善保管，避免泄露敏感数据。
        </AlertDescription>
      </Alert>
    </div>
  </div>
</template>

