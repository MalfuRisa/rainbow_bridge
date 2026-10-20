<script setup lang="ts">
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from '@/components/ui/card'
import { FileArchive } from 'lucide-vue-next'

type ExportFormat = 'zip' | 'tar.gz'

const { format } = defineProps<{
  format: ExportFormat
}>()

const emit = defineEmits<{
  (e: 'change', format: ExportFormat): void
}>()

const handleFormatChange = (newFormat: ExportFormat) => {
  emit('change', newFormat)
}
</script>

<template>
  <Card class="spectrum-panel spectrum-section">
    <CardHeader class="spectrum-panel-header">
      <CardTitle class="text-lg">导出格式</CardTitle>
      <CardDescription>选择导出文件的格式</CardDescription>
    </CardHeader>
    <CardContent class="spectrum-panel-content">
      <div class="spectrum-format-options" role="group" aria-label="导出格式">
        <button
          type="button"
          :aria-pressed="format === 'zip'"
          class="spectrum-format-option flex items-center rounded-lg"
          @click="handleFormatChange('zip')"
        >
          <FileArchive class="w-8 h-8" />
          <div>
            <div class="font-medium">ZIP</div>
            <div class="text-sm text-muted-foreground">推荐格式</div>
          </div>
        </button>
        <button
          type="button"
          :aria-pressed="format === 'tar.gz'"
          class="spectrum-format-option flex items-center rounded-lg"
          @click="handleFormatChange('tar.gz')"
        >
          <FileArchive class="w-8 h-8" />
          <div>
            <div class="font-medium">TAR.GZ</div>
            <div class="text-sm text-muted-foreground">通用压缩格式</div>
          </div>
        </button>
      </div>
    </CardContent>
  </Card>
</template>
